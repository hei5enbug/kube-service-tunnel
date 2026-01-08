package proxy

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

const waitTimeout = 5 * time.Second

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before timeout")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *proxyAdapter) running() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.server != nil
}

type failingCloseListener struct {
	net.Listener
	closeErr  error
	accepting chan struct{}
	once      *sync.Once
}

func (l failingCloseListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	return l.Listener.Accept()
}

func (l failingCloseListener) Close() error {
	_ = l.Listener.Close()
	return l.closeErr
}

func TestAddr(t *testing.T) {
	t.Run("시작하기 전에는 빈 주소를 반환한다", func(t *testing.T) {
		// given
		p := NewProxyAdapter().(*proxyAdapter)

		// when
		addr := p.addr()

		// then
		if addr != "" {
			t.Fatalf("addr = %q, want empty", addr)
		}
	})

	t.Run("Stop 이후에는 빈 주소를 반환한다", func(t *testing.T) {
		// given
		p := startProxy(t)
		if err := p.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}

		// when
		addr := p.addr()

		// then
		if addr != "" {
			t.Fatalf("addr = %q, want empty", addr)
		}
	})
}

func TestServeUnexpectedExit(t *testing.T) {
	t.Run("리스너가 예기치 않게 닫히면 실행 상태를 비우고 다시 시작할 수 있다", func(t *testing.T) {
		// given
		p := startProxy(t)
		if err := p.listener.Close(); err != nil {
			t.Fatalf("close listener: %v", err)
		}

		// when
		waitFor(t, func() bool { return !p.running() })
		restartErr := p.StartIfNotRunning(0)

		// then
		if restartErr != nil {
			t.Fatalf("restart: %v", restartErr)
		}
		if p.addr() == "" {
			t.Fatal("expected a new listener after restart")
		}
	})

	t.Run("예기치 않게 종료된 뒤에도 Stop은 오류 없이 끝난다", func(t *testing.T) {
		// given
		p := startProxy(t)
		_ = p.listener.Close()
		waitFor(t, func() bool { return !p.running() })

		// when
		err := p.Stop()

		// then
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	})
}

func TestMarkStopped(t *testing.T) {
	t.Run("현재 서버가 아닌 서버가 종료되면 실행 상태를 유지한다", func(t *testing.T) {
		// given
		p := startProxy(t)
		addr := p.addr()

		// when
		p.markStopped(&http.Server{})

		// then
		if !p.running() || p.addr() != addr {
			t.Fatal("running state should stay untouched for a stale server")
		}
	})

	t.Run("현재 서버가 종료되면 서버, 리스너, 완료 채널을 비운다", func(t *testing.T) {
		// given
		p := startProxy(t)
		server, done := p.server, p.done
		t.Cleanup(func() {
			_ = server.Close()
			<-done
		})

		// when
		p.markStopped(server)

		// then
		if p.running() || p.addr() != "" || p.done != nil {
			t.Fatal("running state should be cleared")
		}
	})
}

func TestStopCloseFailure(t *testing.T) {
	t.Run("서버 종료가 실패하면 감싼 오류를 반환하고 실행 상태를 비운다", func(t *testing.T) {
		// given
		real, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		cause := errors.New("close stuck")
		accepting := make(chan struct{})
		listener := failingCloseListener{Listener: real, closeErr: cause, accepting: accepting, once: &sync.Once{}}
		server := &http.Server{}
		done := make(chan struct{})
		p := NewProxyAdapter().(*proxyAdapter)
		p.server, p.listener, p.done = server, listener, done
		go p.serve(server, listener, done)
		<-accepting

		// when
		stopErr := p.Stop()

		// then
		if !errors.Is(stopErr, cause) || !strings.Contains(stopErr.Error(), "close server") {
			t.Fatalf("stop error = %v, want wrapped close failure", stopErr)
		}
		if p.running() {
			t.Fatal("running state should be cleared despite close failure")
		}
	})
}
