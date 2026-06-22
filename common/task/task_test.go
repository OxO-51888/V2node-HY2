package task

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTimeoutDoesNotReloadByDefault(t *testing.T) {
	reloadCh := make(chan struct{}, 1)
	executed := make(chan struct{})
	release := make(chan struct{})
	tk := &Task{
		Name:     "test",
		Interval: 10 * time.Millisecond,
		ReloadCh: reloadCh,
		Execute: func(ctx context.Context) error {
			close(executed)
			<-release
			return nil
		},
	}

	if err := tk.ExecuteWithTimeout(); err != nil {
		t.Fatalf("ExecuteWithTimeout() error = %v", err)
	}
	close(release)

	select {
	case <-executed:
	default:
		t.Fatal("task did not execute")
	}
	select {
	case <-reloadCh:
		t.Fatal("timeout should not request reload by default")
	default:
	}
}

func TestTimeoutKeepsExecutionLockUntilWorkerReturns(t *testing.T) {
	release := make(chan struct{})
	var started int32
	tk := &Task{
		Name:     "test",
		Interval: 10 * time.Millisecond,
		Execute: func(ctx context.Context) error {
			atomic.AddInt32(&started, 1)
			<-release
			return nil
		},
	}

	if err := tk.ExecuteWithTimeout(); err != nil {
		t.Fatalf("first ExecuteWithTimeout() error = %v", err)
	}
	if err := tk.ExecuteWithTimeout(); err != nil {
		t.Fatalf("second ExecuteWithTimeout() error = %v", err)
	}

	if got := atomic.LoadInt32(&started); got != 1 {
		t.Fatalf("started while first worker is still blocked = %d, want 1", got)
	}

	close(release)
	time.Sleep(20 * time.Millisecond)

	if err := tk.ExecuteWithTimeout(); err != nil {
		t.Fatalf("third ExecuteWithTimeout() error = %v", err)
	}
	if got := atomic.LoadInt32(&started); got != 2 {
		t.Fatalf("started after first worker returned = %d, want 2", got)
	}
}
