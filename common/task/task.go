package task

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

type Task struct {
	Name      string
	Interval  time.Duration
	Execute   func(context.Context) error
	Access    sync.RWMutex
	Running   bool
	ReloadCh  chan struct{}
	Stop      chan struct{}
	Executing atomic.Bool
}

func (t *Task) Start(first bool) error {
	t.Access.Lock()
	if t.Running {
		t.Access.Unlock()
		return nil
	}
	t.Running = true
	t.Stop = make(chan struct{})
	t.Access.Unlock()
	go func() {
		timer := time.NewTimer(t.Interval)
		defer timer.Stop()
		consecutiveErrors := 0
		if first {
			if err := t.ExecuteWithTimeout(); err != nil {
				consecutiveErrors++
				log.Errorf("Task %s execution error: %v", t.Name, err)
			}
		}

		for {
			timer.Reset(t.Interval)
			select {
			case <-timer.C:
				// continue
			case <-t.Stop:
				return
			}

			if err := t.ExecuteWithTimeout(); err != nil {
				consecutiveErrors++
				log.Errorf("Task %s execution error: %v", t.Name, err)
				if consecutiveErrors >= 3 {
					log.Errorf("Task %s failed %d times, requesting reload", t.Name, consecutiveErrors)
					t.requestReload()
					consecutiveErrors = 0
				}
				continue
			}
			consecutiveErrors = 0
		}
	}()

	return nil
}

func (t *Task) ExecuteWithTimeout() error {
	if !t.Executing.CompareAndSwap(false, true) {
		log.Warningf("Task %s previous execution is still running, skip this tick", t.Name)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), min(5*t.Interval, 5*time.Minute))
	defer cancel()
	done := make(chan error, 1)

	go func() {
		defer t.Executing.Store(false)
		done <- t.Execute(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Errorf("Task %s execution timed out, reloading", t.Name)
		t.requestReload()
		return nil
	case err := <-done:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil
		}
		return err
	}
}

func (t *Task) requestReload() {
	if t.ReloadCh == nil {
		log.Error("Reload failed: reload channel is empty")
		return
	}
	select {
	case t.ReloadCh <- struct{}{}:
	default:
	}
}

func (t *Task) safeStop() {
	t.Access.Lock()
	if t.Running {
		t.Running = false
		close(t.Stop)
	}
	t.Access.Unlock()
}

func (t *Task) Close() {
	t.safeStop()
	log.Warningf("Task %s stopped", t.Name)
}
