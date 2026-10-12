package officialhy2

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
)

type recoveryServer struct {
	fail                 bool
	started, closed      chan struct{}
	startOnce, closeOnce sync.Once
}

func newRecoveryServer(fail bool) *recoveryServer {
	return &recoveryServer{fail: fail, started: make(chan struct{}), closed: make(chan struct{})}
}
func (s *recoveryServer) Serve() error {
	s.startOnce.Do(func() { close(s.started) })
	if s.fail {
		return errors.New("injected listener failure")
	}
	<-s.closed
	return nil
}
func (s *recoveryServer) Close() error { s.closeOnce.Do(func() { close(s.closed) }); return nil }

func awaitRecovery(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("listener recovery timed out")
	}
}

func TestServeLoopRecoversAfterRepeatedCreationFailure(t *testing.T) {
	first, recovered := newRecoveryServer(true), newRecoveryServer(false)
	n := &Node{server: first, stopCh: make(chan struct{}), serveDone: make(chan struct{})}
	var attempts atomic.Int32
	go n.runServeLoop(func() (server.Server, error) {
		if attempts.Add(1) <= 3 {
			return nil, errors.New("injected bind failure")
		}
		return recovered, nil
	}, time.Millisecond)
	t.Cleanup(func() { n.stop() })
	awaitRecovery(t, recovered.started)
	if attempts.Load() != 4 {
		t.Fatalf("attempts=%d", attempts.Load())
	}
	awaitRecovery(t, first.closed)
	n.stop()
	awaitRecovery(t, recovered.closed)
	awaitRecovery(t, n.serveDone)
}

func TestServeLoopStopDuringCreationClosesReplacement(t *testing.T) {
	created, release := make(chan struct{}), make(chan struct{})
	replacement := newRecoveryServer(false)
	n := &Node{stopCh: make(chan struct{}), serveDone: make(chan struct{})}
	go n.runServeLoop(func() (server.Server, error) { close(created); <-release; return replacement, nil }, time.Millisecond)
	awaitRecovery(t, created)
	stopped := make(chan struct{})
	go func() { n.stop(); close(stopped) }()
	awaitRecovery(t, n.stopCh)
	close(release)
	awaitRecovery(t, stopped)
	awaitRecovery(t, replacement.closed)
	select {
	case <-replacement.started:
		t.Fatal("replacement served after shutdown")
	default:
	}
}

func TestServeLoopStopInterruptsBackoff(t *testing.T) {
	n := &Node{stopCh: make(chan struct{}), serveDone: make(chan struct{})}
	var attempts atomic.Int32
	go n.runServeLoop(func() (server.Server, error) { attempts.Add(1); return nil, errors.New("fail") }, time.Hour)
	n.stop()
	awaitRecovery(t, n.serveDone)
	if attempts.Load() != 0 {
		t.Fatal("factory called after stop")
	}
	if nextRestartBackoff(20*time.Second) != 30*time.Second {
		t.Fatal("unbounded backoff")
	}
}
