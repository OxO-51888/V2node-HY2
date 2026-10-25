package task

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

type Task struct {
	Name               string
	Interval           time.Duration
	Execute            func(context.Context) error
	Access             sync.RWMutex
	ExecuteLock        sync.Mutex
	Running            bool
	ReloadCh           chan struct{}
	ReloadOnTimeout    bool
	ExitOnTimeout      bool
	ExitOnTimeoutAfter int32
	Timeout            time.Duration
	Stop               chan struct{}
	timeoutCount       atomic.Int32
	lifetime           context.Context
	cancel             context.CancelFunc
	closed             bool
	loopDone           chan struct{}
	workerDone         chan struct{}
}

func (t *Task) Start(first bool) error {
	t.Access.Lock()
	if t.closed {
		t.Access.Unlock()
		return context.Canceled
	}
	if t.Running {
		t.Access.Unlock()
		return nil
	}
	t.Running = true
	t.Stop = make(chan struct{})
	t.loopDone = make(chan struct{})
	stop, loopDone := t.Stop, t.loopDone
	t.Access.Unlock()
	go func() {
		defer close(loopDone)
		if first {
			if err := t.ExecuteWithTimeout(); err != nil {
				return
			}
		}

		timer := time.NewTimer(t.currentInterval())
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
			case <-stop:
				return
			}

			if err := t.ExecuteWithTimeout(); err != nil {
				log.Errorf("Task %s execution error: %v", t.Name, err)
				return
			}
			timer.Reset(t.currentInterval())
		}
	}()

	return nil
}

func (t *Task) currentInterval() time.Duration {
	t.Access.RLock()
	defer t.Access.RUnlock()
	return t.Interval
}

func (t *Task) UpdateInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	t.Access.Lock()
	oldInterval := t.Interval
	t.Interval = interval
	t.Access.Unlock()
	if oldInterval != interval {
		log.Infof("Task %s interval updated from %s to %s", t.Name, oldInterval, interval)
	}
}

func (t *Task) ExecuteWithTimeout() error {
	if !t.ExecuteLock.TryLock() {
		log.Warningf("Task %s previous execution still running, skip this interval", t.Name)
		if t.ExitOnTimeout && t.timeoutCount.Load() > 0 {
			t.handleExitOnTimeout(t.timeoutCount.Add(1))
		}
		return nil
	}
	t.Access.Lock()
	if t.closed {
		t.Access.Unlock()
		t.ExecuteLock.Unlock()
		return nil
	}
	if t.lifetime == nil {
		t.lifetime, t.cancel = context.WithCancel(context.Background())
	}
	lifetime := t.lifetime
	workerDone := make(chan struct{})
	t.workerDone = workerDone
	t.Access.Unlock()
	ctx, cancel := context.WithTimeout(lifetime, t.currentTimeout())
	defer cancel()
	done := make(chan error, 1)

	go func() {
		defer close(workerDone)
		defer t.ExecuteLock.Unlock()
		done <- t.Execute(ctx)
	}()

	select {
	case <-ctx.Done():
		if lifetime.Err() != nil {
			return nil
		}
		count := t.timeoutCount.Add(1)
		if t.ReloadOnTimeout && t.ReloadCh != nil {
			log.Errorf("Task %s execution timed out, reloading", t.Name)
			select {
			case t.ReloadCh <- struct{}{}:
			default:
			}
		} else if t.ExitOnTimeout {
			t.handleExitOnTimeout(count)
		} else {
			log.Errorf("Task %s execution timed out, will retry on next interval", t.Name)
		}
		return nil
	case err := <-done:
		t.timeoutCount.Store(0)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
}

func (t *Task) handleExitOnTimeout(count int32) {
	threshold := t.ExitOnTimeoutAfter
	if threshold <= 0 {
		threshold = 1
	}
	if count < threshold {
		log.Errorf("Task %s execution timed out %d consecutive time(s), waiting before systemd restart threshold %d", t.Name, count, threshold)
		return
	}
	log.Errorf("Task %s execution timed out %d consecutive time(s), exiting for systemd restart", t.Name, count)
	go func() {
		t.Access.RLock()
		lifetime := t.lifetime
		closed := t.closed
		t.Access.RUnlock()
		if closed {
			return
		}
		if lifetime != nil {
			select {
			case <-lifetime.Done():
				return
			case <-time.After(200 * time.Millisecond):
			}
		} else {
			time.Sleep(200 * time.Millisecond)
		}
		os.Exit(1)
	}()
}

func (t *Task) currentTimeout() time.Duration {
	t.Access.RLock()
	timeout := t.Timeout
	interval := t.Interval
	t.Access.RUnlock()
	if timeout > 0 {
		return timeout
	}
	return min(5*interval, 5*time.Minute)
}

func (t *Task) safeStop() {
	t.Access.Lock()
	t.closed = true
	if t.cancel != nil {
		t.cancel()
	}
	if t.Running {
		t.Running = false
		close(t.Stop)
	}
	t.Access.Unlock()
}

func (t *Task) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := t.CloseContext(ctx); err != nil {
		log.WithError(err).Errorf("Task %s did not stop", t.Name)
	}
}

// Cancel stops scheduling and cancels the current worker without waiting.
func (t *Task) Cancel() { t.safeStop() }

func (t *Task) CloseContext(ctx context.Context) error {
	t.safeStop()
	t.Access.RLock()
	loopDone, workerDone := t.loopDone, t.workerDone
	t.Access.RUnlock()
	for _, done := range []chan struct{}{loopDone, workerDone} {
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	log.Warningf("Task %s stopped", t.Name)
	return nil
}
