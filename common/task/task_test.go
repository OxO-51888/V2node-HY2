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

func TestCloseCancelsAndJoinsWorker(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	tk := &Task{Interval: time.Hour, Timeout: time.Hour, Execute: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return ctx.Err()
	}}
	if err := tk.Start(true); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan error, 1)
	go func() { done <- tk.CloseContext(context.Background()) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("worker not cancelled")
	}
	select {
	case <-done:
		t.Fatal("close returned before worker exit")
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
	if err := tk.ExecuteWithTimeout(); err != nil {
		t.Fatal(err)
	}
	if err := tk.Start(true); err == nil {
		t.Fatal("closed task restarted")
	}
}

func TestCloseTimeoutDoesNotPretendWorkerStopped(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	tk := &Task{Interval: time.Hour, Execute: func(context.Context) error { close(started); <-release; return nil }}
	_ = tk.Start(true)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if tk.CloseContext(ctx) == nil {
		t.Fatal("blocked worker accepted as stopped")
	}
	close(release)
	if err := tk.CloseContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
