package kubetest

import (
	"testing"
	"time"
)

// signal wakes waiters when guarded state changes: fire closes the current channel and installs a new one.
// All access happens with Server.mu held.
type signal struct {
	ch chan struct{}
}

func newSignal() signal { return signal{ch: make(chan struct{})} }

func (g *signal) fire() {
	close(g.ch)
	g.ch = make(chan struct{})
}

// waitFor blocks until condition, evaluated with s.mu held, is true. It fails tb when the timeout passes first.
func (s *Server) waitFor(tb testing.TB, g *signal, condition func() bool, what string) {
	tb.Helper()
	timer := time.NewTimer(s.waitTimeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		satisfied, changed := condition(), g.ch
		s.mu.Unlock()
		if satisfied {
			return
		}
		select {
		case <-changed:
		case <-timer.C:
			tb.Fatalf("kubetest: timed out waiting for %s", what)
		}
	}
}
