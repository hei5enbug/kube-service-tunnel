package dns

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func TestRegisterDNSTunnel(t *testing.T) {
	t.Run("이미 등록된 호스트 이름은 쿠버네티스를 호출하지 않고 거절한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx-a", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
			t.Fatalf("first registration failed: %v", err)
		}

		// when
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx-b", kube.ServiceRef{Namespace: "default", Name: "Web"})

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
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"})

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
		_, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"})

		// then
		if err == nil || len(manager.GetAllDNSTunnels()) != 0 || proxyFake.hasRoute("web.default") {
			t.Fatalf("expected full rollback, err = %v", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 1 || got[0] != "key-web.default" {
			t.Fatalf("stopped forwards = %v", got)
		}
	})
}

func bulkTargets() []kube.ServiceRef {
	return []kube.ServiceRef{{Namespace: "ns", Name: "a"}, {Namespace: "ns", Name: "b"}}
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

func TestRegisterServices(t *testing.T) {
	t.Run("프록시 시작에 실패하면 시작된 모든 포워드를 중지한다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		kubeFake.bulkResult = bulkTunnels()
		proxyFake.startErr = errors.New("port busy")

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

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
		result, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

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

	t.Run("이미 등록된 호스트 이름과 소유 컨텍스트의 맵, 대상 목록을 쿠버네티스 어댑터에 넘긴다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx-a", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
			t.Fatalf("registration failed: %v", err)
		}
		targets := []kube.ServiceRef{{Namespace: "ns", Name: "a"}}

		// when
		_, err := manager.RegisterServices(context.Background(), "ctx-b", targets, nil)

		// then
		if err != nil || !reflect.DeepEqual(kubeFake.bulkOwners, map[string]string{"web.default": "ctx-a"}) {
			t.Fatalf("err = %v, owners = %v, want web.default owned by ctx-a", err, kubeFake.bulkOwners)
		}
		if kubeFake.bulkContext != "ctx-b" || !reflect.DeepEqual(kubeFake.bulkTargets, targets) {
			t.Fatalf("context = %q, targets = %v, want ctx-b and given targets", kubeFake.bulkContext, kubeFake.bulkTargets)
		}
	})

	t.Run("건너뜀과 부적합 분류를 결과에 그대로 전달한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		skipped := kube.ServiceRegistrationFailure{Namespace: "ns", Service: "s", Reason: "already registered (context x)"}
		ineligible := kube.ServiceRegistrationFailure{Namespace: "ns", Service: "i", Reason: kube.ReasonNoSelector}
		kubeFake.bulkResult = kube.RegisterAllResult{
			Tunnels:    []kube.ServiceTunnel{tunnelFor("ctx", "a", "ns", 40000)},
			Skipped:    []kube.ServiceRegistrationFailure{skipped},
			Ineligible: []kube.ServiceRegistrationFailure{ineligible},
		}

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

		// then
		if err != nil || len(result.Registered) != 1 {
			t.Fatalf("err = %v, registered = %+v, want one tunnel", err, result.Registered)
		}
		if !reflect.DeepEqual(result.Skipped, kubeFake.bulkResult.Skipped) || !reflect.DeepEqual(result.Ineligible, kubeFake.bulkResult.Ineligible) {
			t.Fatalf("skipped = %+v, ineligible = %+v, want propagated categories", result.Skipped, result.Ineligible)
		}
	})

	t.Run("프록시 시작에 실패해도 건너뜀과 부적합 분류는 유지한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, proxyFake := newTestManager()
		kubeFake.bulkResult = bulkTunnels()
		kubeFake.bulkResult.Skipped = []kube.ServiceRegistrationFailure{{Namespace: "ns", Service: "s", Reason: "already registered (context x)"}}
		kubeFake.bulkResult.Ineligible = []kube.ServiceRegistrationFailure{{Namespace: "ns", Service: "i", Reason: kube.ReasonNoHTTPPort}}
		proxyFake.startErr = errors.New("port busy")

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

		// then
		if err != nil || len(result.Skipped) != 1 || len(result.Ineligible) != 1 {
			t.Fatalf("err = %v, result = %+v, want categories kept", err, result)
		}
	})

	t.Run("어댑터가 오류를 내면 시작된 포워드를 중지하고 분류된 결과와 오류를 함께 돌려준다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		kubeFake.bulkResult = bulkTunnels()
		kubeFake.bulkResult.Skipped = []kube.ServiceRegistrationFailure{{Namespace: "ns", Service: "s", Reason: "already registered (context x)"}}
		kubeFake.bulkErr = context.Canceled

		// when
		result, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

		// then
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if got := kubeFake.stoppedKeys(); len(got) != 2 {
			t.Fatalf("stopped forwards = %v, want 2", got)
		}
		if len(result.Registered) != 0 || len(result.Failures) != 1 || len(result.Skipped) != 1 || len(manager.GetAllDNSTunnels()) != 0 {
			t.Fatalf("result = %+v, want no registrations and categories kept", result)
		}
	})

	t.Run("진행 상황 콜백을 어댑터에 그대로 전달한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		kubeFake.bulkProgress = []kube.RegistrationProgress{{Done: 0, Total: 1, Current: "a.ns"}, {Done: 1, Total: 1}}
		var got []kube.RegistrationProgress

		// when
		_, err := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), func(p kube.RegistrationProgress) { got = append(got, p) })

		// then
		if err != nil || !reflect.DeepEqual(got, kubeFake.bulkProgress) {
			t.Fatalf("err = %v, progress = %+v, want %+v", err, got, kubeFake.bulkProgress)
		}
	})

	t.Run("컨텍스트 이름이 없거나 종료된 뒤에는 어댑터를 호출하지 않고 거절한다", func(t *testing.T) {
		// given
		manager, kubeFake, _, _ := newTestManager()
		_, emptyErr := manager.RegisterServices(context.Background(), "", bulkTargets(), nil)
		if err := manager.Cleanup(); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}

		// when
		_, closedErr := manager.RegisterServices(context.Background(), "ctx", bulkTargets(), nil)

		// then
		if emptyErr == nil || !errors.Is(closedErr, errShuttingDown) || kubeFake.bulkContext != "" {
			t.Fatalf("emptyErr = %v, closedErr = %v, context seen = %q", emptyErr, closedErr, kubeFake.bulkContext)
		}
	})
}

func TestUnregisterDNSTunnel(t *testing.T) {
	t.Run("호스트 항목 제거에 실패하면 모든 상태를 그대로 둔다", func(t *testing.T) {
		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
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
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
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
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
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
		if _, err := manager.RegisterDNSTunnel(context.Background(), "ctx", kube.ServiceRef{Namespace: "default", Name: "web"}); err != nil {
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
