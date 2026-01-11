package proxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
)

const backendBody = "from-backend"

func newBackend(t *testing.T) (*httptest.Server, int32) {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, backendBody)
	}))
	t.Cleanup(backend.Close)

	u, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatalf("parse backend url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse backend port: %v", err)
	}
	return backend, int32(port)
}

func startProxy(t *testing.T) *proxyAdapter {
	t.Helper()
	p := NewProxyAdapter().(*proxyAdapter)
	if err := p.StartIfNotRunning(0); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

func get(t *testing.T, addr, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func TestProxyAdapter(t *testing.T) {
	t.Parallel()

	t.Run("루프백 주소에만 바인딩한다", func(t *testing.T) {
		t.Parallel()

		// given
		p := startProxy(t)

		// when
		addr := p.addr()

		// then
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("split addr %q: %v", addr, err)
		}
		if host != "127.0.0.1" {
			t.Fatalf("bound host = %q, want 127.0.0.1", host)
		}
	})

	t.Run("대소문자가 섞인 Host와 :80 포트가 있어도 백엔드로 전달한다", func(t *testing.T) {
		t.Parallel()

		// given
		_, backendPort := newBackend(t)
		p := startProxy(t)
		p.AddRoute("Web.Example.COM", backendPort)

		// when
		status, body := get(t, p.addr(), "WEB.example.com:80")

		// then
		if status != http.StatusOK || body != backendBody {
			t.Fatalf("got %d %q, want 200 %q", status, body, backendBody)
		}
	})

	t.Run("호스트 끝의 점 하나를 무시하고 백엔드로 전달한다", func(t *testing.T) {
		t.Parallel()

		// given
		_, backendPort := newBackend(t)
		p := startProxy(t)
		p.AddRoutes(map[string]int32{"web.example.com": backendPort})

		// when
		status, body := get(t, p.addr(), "web.example.com.")

		// then
		if status != http.StatusOK || body != backendBody {
			t.Fatalf("got %d %q, want 200 %q", status, body, backendBody)
		}
	})

	t.Run("등록되지 않은 호스트는 404를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		p := startProxy(t)

		// when
		status, _ := get(t, p.addr(), "unknown.example.com")

		// then
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", status)
		}
	})

	t.Run("삭제한 경로는 404를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		_, backendPort := newBackend(t)
		p := startProxy(t)
		p.AddRoute("web.example.com", backendPort)
		p.RemoveRoute("WEB.example.com")

		// when
		status, _ := get(t, p.addr(), "web.example.com")

		// then
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", status)
		}
	})

	t.Run("이미 실행 중이면 다시 시작해도 오류 없이 같은 주소를 유지한다", func(t *testing.T) {
		t.Parallel()

		// given
		p := startProxy(t)
		first := p.addr()

		// when
		err := p.StartIfNotRunning(0)

		// then
		if err != nil {
			t.Fatalf("second start error: %v", err)
		}
		if p.addr() != first {
			t.Fatalf("addr changed from %s to %s", first, p.addr())
		}
	})

	t.Run("Stop은 nil을 반환하고 두 번 호출해도 안전하다", func(t *testing.T) {
		t.Parallel()

		// given
		p := startProxy(t)
		firstErr := p.Stop()

		// when
		secondErr := p.Stop()

		// then
		if firstErr != nil || secondErr != nil {
			t.Fatalf("Stop errors: first=%v second=%v", firstErr, secondErr)
		}
	})

	t.Run("Stop 이후 포트가 닫힌다", func(t *testing.T) {
		t.Parallel()

		// given
		p := startProxy(t)
		listener := p.listener
		if err := p.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}

		// when
		conn, err := listener.Accept()

		// then
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected accept to fail after Stop")
		}
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("err = %v, want net.ErrClosed", err)
		}
	})

	t.Run("Stop 이후 다시 시작해도 그 전에 추가한 경로로 전달한다", func(t *testing.T) {
		t.Parallel()

		// given
		_, backendPort := newBackend(t)
		p := startProxy(t)
		if err := p.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}
		p.AddRoute("web.example.com", backendPort)
		if err := p.StartIfNotRunning(0); err != nil {
			t.Fatalf("restart: %v", err)
		}

		// when
		status, body := get(t, p.addr(), "web.example.com")

		// then
		if status != http.StatusOK || body != backendBody {
			t.Fatalf("got %d %q, want 200 %q", status, body, backendBody)
		}
	})

	t.Run("Stop은 등록된 경로를 비운다", func(t *testing.T) {
		t.Parallel()

		// given
		_, backendPort := newBackend(t)
		p := startProxy(t)
		p.AddRoute("web.example.com", backendPort)
		if err := p.Stop(); err != nil {
			t.Fatalf("stop: %v", err)
		}
		if err := p.StartIfNotRunning(0); err != nil {
			t.Fatalf("restart: %v", err)
		}

		// when
		status, _ := get(t, p.addr(), "web.example.com")

		// then
		if status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", status)
		}
	})

	t.Run("이미 사용 중인 포트면 시작 오류를 반환한다", func(t *testing.T) {
		t.Parallel()

		// given
		occupied, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer occupied.Close()
		port := int32(occupied.Addr().(*net.TCPAddr).Port)
		p := NewProxyAdapter().(*proxyAdapter)

		// when
		startErr := p.StartIfNotRunning(port)

		// then
		if startErr == nil {
			_ = p.Stop()
			t.Fatal("expected listen error")
		}
	})
}

func TestNormalizeHost(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Foo.Example.com":       "foo.example.com",
		"foo.example.com:80":    "foo.example.com",
		"FOO.example.com.:8080": "foo.example.com",
		"foo.example.com..":     "foo.example.com.",
	}
	for input, want := range cases {
		t.Run("호스트 "+input+"를 "+want+"로 정규화한다", func(t *testing.T) {
			t.Parallel()

			// given
			raw := input

			// when
			got := normalizeHost(raw)

			// then
			if got != want {
				t.Fatalf("normalizeHost(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}
