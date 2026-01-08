package dns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const minimalKubeconfig = `apiVersion: v1
kind: Config
current-context: test
clusters:
- name: test
  cluster:
    server: https://127.0.0.1:1
contexts:
- name: test
  context:
    cluster: test
    user: test
users:
- name: test
  user:
    token: dummy
`

func registerDefault(t *testing.T, manager *DNSManager, name string) DNSTunnel {
	t.Helper()
	tunnel, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: name})
	if err != nil {
		t.Fatalf("registration of %s failed: %v", name, err)
	}
	return tunnel
}

func TestNewDNSManager(t *testing.T) {
	t.Run("유효한 kubeconfig 파일이면 클러스터에 접속하지 않고 매니저를 만든다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "config")
		if err := os.WriteFile(path, []byte(minimalKubeconfig), 0o600); err != nil {
			t.Fatalf("write kubeconfig: %v", err)
		}

		// when
		manager, err := NewDNSManager(path)

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if manager == nil || len(manager.GetAllDNSTunnels()) != 0 {
			t.Fatalf("expected an empty manager, got %+v", manager)
		}
	})

	t.Run("kubeconfig 파일이 없으면 감싼 오류를 반환한다", func(t *testing.T) {
		// given
		path := filepath.Join(t.TempDir(), "missing")

		// when
		manager, err := NewDNSManager(path)

		// then
		if manager != nil {
			t.Fatal("expected nil manager")
		}
		if err == nil || !strings.Contains(err.Error(), "create kube adapter") || !strings.Contains(err.Error(), path) {
			t.Fatalf("error = %v, want wrapped kubeconfig-not-found error", err)
		}
	})
}

func TestRegisterDNSTunnelBranches(t *testing.T) {
	t.Run("컨텍스트, 서비스 이름, 네임스페이스 중 하나라도 비어 있으면 거절한다", func(t *testing.T) {
		tests := []struct {
			name        string
			contextName string
			target      kube.ServiceRef
		}{
			{"컨텍스트 이름이 비어 있다", "", kube.ServiceRef{Namespace: "default", Name: "web"}},
			{"서비스 이름이 비어 있다", "ctx", kube.ServiceRef{Namespace: "default"}},
			{"네임스페이스가 비어 있다", "ctx", kube.ServiceRef{Name: "web"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// given
				manager, kubeFake, _, _ := newTestManager()

				// when
				_, err := manager.RegisterDNSTunnel(context.Background(), tt.contextName, tt.target)

				// then
				if err == nil || !strings.Contains(err.Error(), "are required") {
					t.Fatalf("error = %v, want required-arguments error", err)
				}
				if kubeFake.registerCalls != 0 {
					t.Fatalf("kube register calls = %d, want 0", kubeFake.registerCalls)
				}
			})
		}
	})

	t.Run("쿠버네티스 어댑터가 실패하면 그 오류를 돌려주고 아무것도 등록하지 않는다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		cause := errors.New("no pod")
		kubeFake.registerFn = func(string, kube.ServiceRef) (kube.ServiceTunnel, error) {
			return kube.ServiceTunnel{}, cause
		}

		// when
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"})

		// then
		if !errors.Is(err, cause) {
			t.Fatalf("error = %v, want %v", err, cause)
		}
		if len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") || proxyFake.hasRoute("web.default") {
			t.Fatal("nothing should be registered")
		}
	})

	t.Run("프록시 시작에 실패하면 포워드를 멈추고 시작 오류와 중지 오류를 함께 돌려준다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		proxyFake.startErr = errors.New("port busy")
		kubeFake.stopErr = errors.New("stop stuck")

		// when
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"})

		// then
		if err == nil || !strings.Contains(err.Error(), "start proxy server: port busy") || !strings.Contains(err.Error(), "stop stuck") {
			t.Fatalf("error = %v, want both start and stop failures", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 1 || got[0] != "key-web.default" {
			t.Fatalf("stopped forwards = %v", got)
		}
		if len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") {
			t.Fatal("nothing should be registered")
		}
	})
}

func TestUnregisterDNSTunnelBranches(t *testing.T) {
	t.Run("DNS URL이 비어 있으면 거절한다", func(t *testing.T) {
		// given
		manager, _, _, _ := newTestManager()

		// when
		err := manager.UnregisterDNSTunnel("")

		// then
		if err == nil || !strings.Contains(err.Error(), "DNS URL is required") {
			t.Fatalf("error = %v, want required error", err)
		}
	})

	t.Run("등록되지 않은 DNS URL이면 찾을 수 없다는 오류를 반환한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()

		// when
		err := manager.UnregisterDNSTunnel("ghost.default")

		// then
		if err == nil || !strings.Contains(err.Error(), "tunnel not found for DNS URL: ghost.default") {
			t.Fatalf("error = %v, want not-found error", err)
		}
		if len(kubeFake.stoppedKeys()) != 0 {
			t.Fatal("no forward should be stopped")
		}
	})

	t.Run("포워드 중지에 실패해도 목록 항목은 지우고 감싼 오류를 반환한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		registerDefault(t, manager, "web")
		cause := errors.New("stop stuck")
		kubeFake.stopErr = cause

		// when
		err := manager.UnregisterDNSTunnel("web.default")

		// then
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "stop port forward") {
			t.Fatalf("error = %v, want wrapped stop failure", err)
		}
		if len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") || proxyFake.hasRoute("web.default") {
			t.Fatal("tunnel state should be removed despite stop failure")
		}
	})

	t.Run("포워드가 이미 없어졌다면 오류 없이 성공한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		registerDefault(t, manager, "web")
		kubeFake.stopErr = kube.ErrPortForwardNotFound

		// when
		err := manager.UnregisterDNSTunnel("web.default")

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("여러 터널 중 하나를 지우면 나머지는 순서대로 남는다", func(t *testing.T) {
		// given
		manager, _, _, _ := newTestManager()
		registerDefault(t, manager, "a")
		registerDefault(t, manager, "b")
		registerDefault(t, manager, "c")

		// when
		err := manager.UnregisterDNSTunnel("b.default")

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		remaining := manager.GetAllDNSTunnels()
		if len(remaining) != 2 || remaining[0].DNSURL != "a.default" || remaining[1].DNSURL != "c.default" {
			t.Fatalf("remaining = %+v, want a.default and c.default", remaining)
		}
	})
}

func TestTunnelLostBranches(t *testing.T) {
	t.Run("호스트 항목 제거가 실패하면 원인에 정리 실패를 덧붙여 핸들러에 알린다", func(t *testing.T) {
		// given
		manager, kubeFake, _, proxyFake := newTestManager()
		registerDefault(t, manager, "web")
		hostsFake := manager.hostsFileAdapter.(*fakeHosts)
		hostsFake.removeErr = errors.New("read-only")
		var gotErr error
		manager.SetTunnelLostHandler(func(_ DNSTunnel, err error) { gotErr = err })
		cause := errors.New("lost connection to pod")

		// when
		kubeFake.lostHandler("key-web.default", cause)

		// then
		if !errors.Is(gotErr, cause) || !strings.Contains(gotErr.Error(), "cleanup failed: remove hosts entry: read-only") {
			t.Fatalf("handler error = %v, want cause with cleanup failure", gotErr)
		}
		if len(manager.GetAllDNSTunnels()) != 0 || proxyFake.hasRoute("web.default") {
			t.Fatal("lost tunnel should still be removed")
		}
	})

	t.Run("핸들러가 없어도 끊어진 터널은 제거한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		registerDefault(t, manager, "web")

		// when
		kubeFake.lostHandler("key-web.default", errors.New("lost"))

		// then
		if len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") || proxyFake.hasRoute("web.default") {
			t.Fatal("lost tunnel should be removed without a handler")
		}
	})
}

func TestStopForwards(t *testing.T) {
	t.Run("중지에 실패한 포워드마다 DNS URL을 붙여 오류를 합친다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		cause := errors.New("stop stuck")
		kubeFake.stopErr = cause
		tunnels := []kube.ServiceTunnel{tunnelFor("ctx", "a", "ns", 40000), tunnelFor("ctx", "b", "ns", 40001)}

		// when
		err := manager.stopForwards(tunnels)

		// then
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "stop port forward a.ns") || !strings.Contains(err.Error(), "stop port forward b.ns") {
			t.Fatalf("error = %v, want one wrapped failure per forward", err)
		}
	})

	t.Run("이미 없어진 포워드는 오류로 치지 않는다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		kubeFake.stopErr = kube.ErrPortForwardNotFound

		// when
		err := manager.stopForwards([]kube.ServiceTunnel{tunnelFor("ctx", "a", "ns", 40000)})

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
