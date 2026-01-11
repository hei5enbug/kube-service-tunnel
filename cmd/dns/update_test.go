package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func registerWeb(t *testing.T, manager *DNSManager, target kube.ServiceRef) DNSTunnel {
	t.Helper()
	if target.Name == "" {
		target = kube.ServiceRef{Namespace: "default", Name: "web"}
	}
	tunnel, err := manager.RegisterDNSTunnel(context.Background(), "ctx", target)
	if err != nil {
		t.Fatalf("registration failed: %v", err)
	}
	return tunnel
}

func TestUpdateTunnel(t *testing.T) {
	t.Parallel()

	t.Run("호스트 이름만 바꾸면 새 항목과 경로를 만들고 이전 것을 지우며 포워드는 그대로 둔다", func(t *testing.T) {
		t.Parallel()

		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", " Web.Local. ", 0)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if updated.DNSURL != "web.local" || updated.LocalPort != old.LocalPort || updated.ForwardKey != old.ForwardKey {
			t.Fatalf("updated = %+v", updated)
		}
		if !hostsFake.has("web.local") || hostsFake.has("web.default") || !proxyFake.hasRoute("web.local") || proxyFake.hasRoute("web.default") {
			t.Fatalf("hosts/proxy not switched to web.local")
		}
		if len(kubeFake.startedForwards()) != 0 || len(kubeFake.stoppedKeys()) != 0 {
			t.Fatalf("forwards touched: started %v, stopped %v", kubeFake.startedForwards(), kubeFake.stoppedKeys())
		}
		if got := manager.GetAllDNSTunnels(); !reflect.DeepEqual(got, []DNSTunnel{updated}) {
			t.Fatalf("tunnels = %+v, want [%+v]", got, updated)
		}
	})

	t.Run("포트만 바꾸면 같은 파드에 새 포워드를 열고 경로를 옮긴 뒤 이전 포워드를 멈춘다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.default", port)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		wantStart := []startedForward{{contextName: "ctx", namespace: "default", pod: old.Pod, remotePort: old.RemotePort, localPort: port}}
		if got := kubeFake.startedForwards(); !reflect.DeepEqual(got, wantStart) {
			t.Fatalf("started = %+v, want %+v", got, wantStart)
		}
		if updated.LocalPort != port || updated.RequestedLocalPort != port || updated.ForwardKey == old.ForwardKey {
			t.Fatalf("updated = %+v", updated)
		}
		if proxyFake.routePort("web.default") != port || !hostsFake.has("web.default") {
			t.Fatalf("route port = %d, hosts entry = %v", proxyFake.routePort("web.default"), hostsFake.has("web.default"))
		}
		if got := kubeFake.stoppedKeys(); !reflect.DeepEqual(got, []string{old.ForwardKey}) {
			t.Fatalf("stopped = %v, want only the old forward", got)
		}
	})

	t.Run("호스트 이름과 포트를 함께 바꾼다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "app.local", port)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if updated.DNSURL != "app.local" || updated.LocalPort != port || updated.RequestedLocalPort != port {
			t.Fatalf("updated = %+v", updated)
		}
		if !hostsFake.has("app.local") || hostsFake.has("web.default") || proxyFake.routePort("app.local") != port || proxyFake.hasRoute("web.default") {
			t.Fatalf("hosts/proxy not switched")
		}
		if got := kubeFake.stoppedKeys(); !reflect.DeepEqual(got, []string{old.ForwardKey}) {
			t.Fatalf("stopped = %v", got)
		}
	})

	t.Run("빈 호스트 이름과 0 포트는 기본값으로 되돌린다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{Namespace: "default", Name: "web", Hostname: "custom.local", LocalPort: port})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "custom.local", "", 0)

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if updated.DNSURL != "web.default" || updated.RequestedLocalPort != 0 || updated.LocalPort == old.LocalPort || updated.LocalPort < 40000 {
			t.Fatalf("updated = %+v, want default host and an automatic port", updated)
		}
		if !hostsFake.has("web.default") || hostsFake.has("custom.local") || proxyFake.routePort("web.default") != updated.LocalPort || proxyFake.hasRoute("custom.local") {
			t.Fatalf("hosts/proxy not reset")
		}
		if got := kubeFake.stoppedKeys(); !reflect.DeepEqual(got, []string{old.ForwardKey}) {
			t.Fatalf("stopped = %v", got)
		}
	})

	t.Run("요청 포트가 없던 터널을 0 포트로 갱신하면 포트를 유지한다", func(t *testing.T) {
		t.Parallel()

		// given
		manager, kubeFake, _, _ := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", 0)

		// then
		if err != nil || updated.LocalPort != old.LocalPort || len(kubeFake.startedForwards()) != 0 {
			t.Fatalf("updated = %+v, err = %v, started = %v", updated, err, kubeFake.startedForwards())
		}
	})

	t.Run("바뀌는 것이 없으면 어댑터를 건드리지 않고 그대로 돌려준다", func(t *testing.T) {
		t.Parallel()

		// given
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		hostsFake.removeErr = errors.New("must not be called")

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "WEB.default.", 0)

		// then
		if err != nil || updated != old {
			t.Fatalf("updated = %+v, err = %v, want unchanged %+v", updated, err, old)
		}
		if len(kubeFake.startedForwards()) != 0 || len(kubeFake.stoppedKeys()) != 0 || proxyFake.routePort("web.default") != old.LocalPort {
			t.Fatalf("adapters touched")
		}
	})

	t.Run("현재 포트와 같은 포트를 요청하면 포워드를 옮기지 않고 요청 포트만 기록한다", func(t *testing.T) {
		t.Parallel()

		// given
		manager, kubeFake, _, _ := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.default", old.LocalPort)

		// then
		if err != nil || updated.LocalPort != old.LocalPort || updated.RequestedLocalPort != old.LocalPort || updated.ForwardKey != old.ForwardKey {
			t.Fatalf("updated = %+v, err = %v", updated, err)
		}
		if len(kubeFake.startedForwards()) != 0 || len(kubeFake.stoppedKeys()) != 0 {
			t.Fatalf("forwards touched")
		}
		if got := manager.GetAllDNSTunnels(); !reflect.DeepEqual(got, []DNSTunnel{updated}) {
			t.Fatalf("tunnels = %+v", got)
		}
	})

	t.Run("갱신해도 목록에서의 위치를 유지한다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, _, _, _ := newTestManager()
		registerWeb(t, manager, kube.ServiceRef{Namespace: "default", Name: "web", LocalPort: 40000})
		registerWeb(t, manager, kube.ServiceRef{Namespace: "default", Name: "api", LocalPort: 40001})

		// when
		_, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)

		// then
		got := manager.GetAllDNSTunnels()
		if err != nil || len(got) != 2 || got[0].DNSURL != "web.local" || got[1].DNSURL != "api.default" {
			t.Fatalf("tunnels = %+v, err = %v", got, err)
		}
	})
}

func TestUpdateTunnelRefusals(t *testing.T) {
	t.Parallel()

	t.Run("검증에 실패하면 아무것도 바꾸지 않고 오류를 낸다", func(t *testing.T) {
		t.Parallel()

		busy, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = busy.Close() })
		busyPort := int32(busy.Addr().(*net.TCPAddr).Port)

		tests := []struct {
			name     string
			dnsURL   string
			hostname string
			port     int32
			want     string
		}{
			{"없는 터널", "ghost.default", "", 0, "tunnel not found for DNS URL: ghost.default"},
			{"잘못된 호스트 이름", "web.default", "bad_name", 0, "hostname label"},
			{"다른 터널이 쓰는 호스트 이름", "web.default", "API.default", 0, "api.default is already registered (context ctx)"},
			{"범위를 벗어난 포트", "web.default", "web.default", 70000, "out of range"},
			{"프록시 포트", "web.default", "web.default", 80, "local port 80 is used by the proxy"},
			{"다른 터널이 쓰는 포트", "web.default", "web.default", 40001, "local port 40001 is used by another tunnel"},
			{"로컬에서 점유된 포트", "web.default", "web.default", busyPort, fmt.Sprintf("local port %d is already in use on 127.0.0.1", busyPort)},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				// given
				manager, kubeFake, hostsFake, proxyFake := newTestManager()
				registerWeb(t, manager, kube.ServiceRef{Namespace: "default", Name: "web", LocalPort: 40000})
				registerWeb(t, manager, kube.ServiceRef{Namespace: "default", Name: "api", LocalPort: 40001})
				before := manager.GetAllDNSTunnels()

				// when
				_, err := manager.UpdateTunnel(context.Background(), tt.dnsURL, tt.hostname, tt.port)

				// then
				if err == nil || !strings.Contains(err.Error(), tt.want) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.want)
				}
				if !reflect.DeepEqual(manager.GetAllDNSTunnels(), before) || len(kubeFake.startedForwards()) != 0 ||
					len(kubeFake.stoppedKeys()) != 0 || !hostsFake.has("web.default") || !proxyFake.hasRoute("web.default") {
					t.Fatalf("state changed despite refusal")
				}
			})
		}
	})

	t.Run("종료된 뒤에는 갱신을 거절한다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, _, _ := newTestManager()
		registerWeb(t, manager, kube.ServiceRef{})
		if err := manager.Cleanup(); err != nil {
			t.Fatalf("cleanup failed: %v", err)
		}

		// when
		_, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)

		// then
		if !errors.Is(err, errShuttingDown) || len(kubeFake.startedForwards()) != 0 {
			t.Fatalf("err = %v, want shutting down without new forwards", err)
		}
	})
}

func TestUpdateTunnelFailures(t *testing.T) {
	t.Parallel()

	t.Run("새 포워드 시작에 실패하면 감싼 오류를 내고 모든 것을 그대로 둔다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		cause := errors.New("pod gone")
		kubeFake.startFn = func(string, int32, int32) (string, error) { return "", cause }

		// when
		_, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)

		// then
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), fmt.Sprintf("start port forward on 127.0.0.1:%d", port)) {
			t.Fatalf("err = %v, want wrapped start failure", err)
		}
		if got := manager.GetAllDNSTunnels(); !reflect.DeepEqual(got, []DNSTunnel{old}) {
			t.Fatalf("tunnels = %+v, want unchanged", got)
		}
		if hostsFake.has("web.local") || !hostsFake.has("web.default") || proxyFake.hasRoute("web.local") || proxyFake.routePort("web.default") != old.LocalPort {
			t.Fatalf("hosts/proxy changed")
		}
		if len(kubeFake.stoppedKeys()) != 0 {
			t.Fatalf("stopped = %v, want none", kubeFake.stoppedKeys())
		}
	})

	t.Run("호스트 항목 추가에 실패하면 새 포워드를 멈추고 모든 것을 그대로 둔다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		hostsFake.addErrs["web.local"] = errors.New("conflict")

		// when
		_, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)

		// then
		if err == nil || !strings.Contains(err.Error(), "conflict") {
			t.Fatalf("err = %v, want hosts failure", err)
		}
		if got := kubeFake.stoppedKeys(); !reflect.DeepEqual(got, []string{fmt.Sprintf("fwd-web-pod-%d", port)}) {
			t.Fatalf("stopped = %v, want only the new forward", got)
		}
		if got := manager.GetAllDNSTunnels(); !reflect.DeepEqual(got, []DNSTunnel{old}) {
			t.Fatalf("tunnels = %+v, want unchanged", got)
		}
		if !hostsFake.has("web.default") || proxyFake.hasRoute("web.local") || proxyFake.routePort("web.default") != old.LocalPort {
			t.Fatalf("hosts/proxy changed")
		}
	})

	t.Run("호스트 이름만 바꾸다 항목 추가에 실패하면 포워드를 건드리지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		manager, kubeFake, hostsFake, _ := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		hostsFake.addErrs["web.local"] = errors.New("conflict")

		// when
		_, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", 0)

		// then
		if err == nil || len(kubeFake.stoppedKeys()) != 0 || !reflect.DeepEqual(manager.GetAllDNSTunnels(), []DNSTunnel{old}) {
			t.Fatalf("err = %v, stopped = %v", err, kubeFake.stoppedKeys())
		}
	})

	t.Run("이전 설정 정리에 실패해도 갱신은 유지하고 갱신된 터널과 ErrUpdateCleanup을 함께 돌려준다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		hostsFake.removeErr = errors.New("read-only")
		kubeFake.stopErr = errors.New("stuck")

		// when
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)

		// then
		if !errors.Is(err, ErrUpdateCleanup) || !strings.Contains(err.Error(), "read-only") || !strings.Contains(err.Error(), "stuck") {
			t.Fatalf("err = %v, want ErrUpdateCleanup with both causes", err)
		}
		if updated.DNSURL != "web.local" || updated.LocalPort != port || updated.ForwardKey == old.ForwardKey {
			t.Fatalf("updated = %+v", updated)
		}
		if got := manager.GetAllDNSTunnels(); !reflect.DeepEqual(got, []DNSTunnel{updated}) {
			t.Fatalf("tunnels = %+v, want the updated record", got)
		}
		if proxyFake.routePort("web.local") != port || proxyFake.hasRoute("web.default") {
			t.Fatalf("proxy routes not switched")
		}
	})
}

func TestUpdateTunnelLostCallback(t *testing.T) {
	t.Parallel()

	t.Run("이전 포워드 키의 끊김 알림은 갱신된 터널을 지우지 않는다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		old := registerWeb(t, manager, kube.ServiceRef{})
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)
		if err != nil {
			t.Fatalf("update failed: %v", err)
		}
		called := false
		manager.SetTunnelLostHandler(func(DNSTunnel, error) { called = true })

		// when
		kubeFake.lostHandler(old.ForwardKey, errors.New("closed"))

		// then
		if called || !reflect.DeepEqual(manager.GetAllDNSTunnels(), []DNSTunnel{updated}) || !hostsFake.has("web.local") || !proxyFake.hasRoute("web.local") {
			t.Fatalf("old forward callback removed the updated tunnel")
		}
	})

	t.Run("새 포워드 키가 끊기면 갱신된 호스트 이름의 항목과 경로를 지운다", func(t *testing.T) {
		t.Parallel()

		// given
		port := freePort(t)
		manager, kubeFake, hostsFake, proxyFake := newTestManager()
		registerWeb(t, manager, kube.ServiceRef{})
		updated, err := manager.UpdateTunnel(context.Background(), "web.default", "web.local", port)
		if err != nil {
			t.Fatalf("update failed: %v", err)
		}
		var got DNSTunnel
		manager.SetTunnelLostHandler(func(tunnel DNSTunnel, _ error) { got = tunnel })

		// when
		kubeFake.lostHandler(updated.ForwardKey, errors.New("closed"))

		// then
		if got.DNSURL != "web.local" || len(manager.GetAllDNSTunnels()) != 0 || hostsFake.has("web.local") || proxyFake.hasRoute("web.local") {
			t.Fatalf("lost updated tunnel not cleaned up: %+v", got)
		}
	})
}
