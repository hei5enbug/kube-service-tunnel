package dns

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestRegisterDNSTunnel(t *testing.T) {
	t.Run("이미 등록된 호스트 이름은 쿠버네티스를 호출하지 않고 거절한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx-a", "web", "default"); err != nil {
			t.Fatalf("first registration failed: %v", err)
		}

		// when
		err := manager.RegisterDNSTunnel(context.Background(), "ctx-b", "Web", "default")

		// then
		if err == nil || !strings.Contains(err.Error(), "web.default is already registered (context ctx-a)") {
			t.Fatalf("error = %v, want duplicate hostname owned by ctx-a", err)
		}
		if kubeFake.registerCalls != 1 {
			t.Fatalf("kube register calls = %d, want 1", kubeFake.registerCalls)
		}
	})

	t.Run("종료된 뒤에는 등록을 거절한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, _ := newTestManager()
		if err := manager.Cleanup(); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}

		// when
		err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default")

		// then
		if !errors.Is(err, errShuttingDown) {
			t.Fatalf("error = %v, want shutting down", err)
		}
		if kubeFake.registerCalls != 0 || hostsFake.has("web.default") {
			t.Fatalf("registration reached adapters after cleanup")
		}
	})

	t.Run("호스트 파일 추가에 실패하면 경로와 포워드를 되돌린다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		hostsFake.addErrs["web.default"] = errors.New("conflict")

		// when
		err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default")

		// then
		if err == nil || len(manager.GetAllDNSTunnels()) != 0 || proxyFake.hasRoute("web.default") {
			t.Fatalf("expected full rollback, err = %v", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 1 || got[0] != "key-web.default" {
			t.Fatalf("stopped forwards = %v", got)
		}
	})
}

func bulkTunnels() kube.RegisterAllResult {
	return kube.RegisterAllResult{
		Tunnels: []kube.ServiceTunnel{
			tunnelFor("ctx", "a", "ns", 40000),
			tunnelFor("ctx", "b", "ns", 40001),
		},
		Failures: []kube.ServiceRegistrationFailure{{Namespace: "ns", Service: "c", Reason: "no pod"}},
	}
}

func TestRegisterAllByContext(t *testing.T) {
	t.Run("프록시 시작에 실패하면 시작된 모든 포워드를 중지한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		kubeFake.bulkResult = bulkTunnels()
		proxyFake.startErr = errors.New("port busy")

		// when
		result, err := manager.RegisterAllByContext(context.Background(), "ctx", []string{"ns"})

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 2 {
			t.Fatalf("stopped forwards = %v, want 2", got)
		}
		if len(result.Registered) != 0 || len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("a.ns") {
			t.Fatalf("nothing should stay registered: %+v", result)
		}
		if len(result.Failures) != 3 || !strings.Contains(result.Failures[1].Reason, "port busy") {
			t.Fatalf("failures = %+v", result.Failures)
		}
	})

	t.Run("한 터널의 호스트 파일 추가가 실패해도 나머지는 유지한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		kubeFake.bulkResult = bulkTunnels()
		hostsFake.addErrs["a.ns"] = errors.New("conflict")

		// when
		result, err := manager.RegisterAllByContext(context.Background(), "ctx", []string{"ns"})

		// then
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Registered) != 1 || result.Registered[0].DNSURL != "b.ns" {
			t.Fatalf("registered = %+v, want only b.ns", result.Registered)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 1 || got[0] != "key-a.ns" {
			t.Fatalf("stopped forwards = %v, want only a.ns", got)
		}
		if proxyFake.hasRoute("a.ns") || !proxyFake.hasRoute("b.ns") || !hostsFake.has("b.ns") {
			t.Fatalf("route/hosts state is wrong")
		}
		if len(result.Failures) != 2 || result.Failures[1].Service != "a" {
			t.Fatalf("failures = %+v", result.Failures)
		}
	})

	t.Run("이미 등록된 호스트 이름 집합을 쿠버네티스 어댑터에 넘긴다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default"); err != nil {
			t.Fatalf("registration failed: %v", err)
		}

		// when
		_, err := manager.RegisterAllByContext(context.Background(), "ctx", nil)

		// then
		if err != nil || !kubeFake.bulkDNSSeen["web.default"] {
			t.Fatalf("err = %v, registeredDNS = %v", err, kubeFake.bulkDNSSeen)
		}
	})
}

func TestUnregisterDNSTunnel(t *testing.T) {
	t.Run("호스트 항목 제거에 실패하면 모든 상태를 그대로 둔다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default"); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		hostsFake.removeErr = errors.New("read-only")

		// when
		err := manager.UnregisterDNSTunnel("web.default")

		// then
		if err == nil {
			t.Fatal("expected error")
		}
		if len(manager.GetAllDNSTunnels()) != 1 || !proxyFake.hasRoute("web.default") || len(kubeFake.stoppedKeys()) != 0 {
			t.Fatalf("state changed despite hosts failure")
		}
	})

	t.Run("성공하면 호스트 항목, 경로, 포워드, 목록 항목을 모두 제거한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default"); err != nil {
			t.Fatalf("registration failed: %v", err)
		}

		// when
		err := manager.UnregisterDNSTunnel("web.default")

		// then
		if err != nil || len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") || proxyFake.hasRoute("web.default") {
			t.Fatalf("incomplete unregister, err = %v", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 1 {
			t.Fatalf("stopped forwards = %v", got)
		}
	})
}

func TestCleanup(t *testing.T) {
	t.Run("프록시 종료가 실패해도 호스트 항목을 지우고 오류를 합쳐 돌려준다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default"); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		proxyFake.stopErr = errors.New("proxy stuck")
		hostsFake.clearErr = errors.New("hosts stuck")

		// when
		err := manager.Cleanup()

		// then
		if err == nil || !strings.Contains(err.Error(), "proxy stuck") || !strings.Contains(err.Error(), "hosts stuck") {
			t.Fatalf("error = %v, want both failures", err)
		}
		if hostsFake.clearCalls != 1 || kubeFake.stopAllCalls != 1 || len(manager.GetAllDNSTunnels()) != 0 {
			t.Fatalf("cleanup did not run every step")
		}
	})

	t.Run("여러 번 호출해도 안전하다", func(t *testing.T) {
		// given
		manager, _, _, _ := newTestManager()
		_ = manager.Cleanup()

		// when
		err := manager.Cleanup()

		// then
		if err != nil {
			t.Fatalf("second cleanup error = %v", err)
		}
	})
}

func TestTunnelLost(t *testing.T) {
	t.Run("끊어진 포워드의 터널을 제거하고 핸들러에 알린다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		if err := manager.RegisterDNSTunnel(context.Background(), "ctx", "web", "default"); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		var gotTunnel DNSTunnel
		var gotErr error
		manager.SetTunnelLostHandler(func(tunnel DNSTunnel, err error) {
			gotTunnel, gotErr = tunnel, err
		})
		cause := errors.New("lost connection to pod")

		// when
		kubeFake.lostHandler("key-web.default", cause)

		// then
		if gotTunnel.DNSURL != "web.default" || !errors.Is(gotErr, cause) {
			t.Fatalf("handler got %+v, %v", gotTunnel, gotErr)
		}
		if len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.default") || proxyFake.hasRoute("web.default") {
			t.Fatalf("lost tunnel was not removed")
		}
	})

	t.Run("알 수 없는 포워드 키는 무시한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		called := false
		manager.SetTunnelLostHandler(func(DNSTunnel, error) { called = true })

		// when
		kubeFake.lostHandler("unknown", errors.New("x"))

		// then
		if called {
			t.Fatal("handler must not be called for unknown forward")
		}
	})
}
