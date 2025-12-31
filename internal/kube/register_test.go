package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func httpService(name, namespace string, selector map[string]string, ports ...ServicePort) Service {
	if len(ports) == 0 {
		ports = []ServicePort{{Name: "http", Port: 80, Protocol: "TCP"}}
	}
	return Service{Name: name, Namespace: namespace, ClusterIP: "10.0.0.1", Type: "ClusterIP", Ports: ports, Selector: selector}
}

func TestRegisterAllServicesForContext(t *testing.T) {
	t.Run("이미 등록된 호스트명은 실패로 기록하고 포워딩하지 않으며 나머지 오류도 모두 보고한다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{
			byNamespace: map[string][]Service{
				"app": {
					httpService("web", "app", selectorApp),
					httpService("api", "app", selectorApp, ServicePort{Port: 8080, Protocol: "TCP"}),
					httpService("db", "app", selectorApp, ServicePort{Port: 5432, Protocol: "TCP"}),
					httpService("ext", "app", nil),
					httpService("dns", "app", selectorApp, ServicePort{Port: 80, Protocol: "UDP"}),
				},
			},
			errs: map[string]error{"broken": errors.New("forbidden")},
		}
		pods := &fakePodClient{pods: []Pod{readyPod("web-1")}}
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(pods, services, &fakeNamespaceClient{}, forwards)
		usedPorts := map[int32]bool{}
		registered := map[string]bool{"api.app": true}

		// when
		result, err := adapter.RegisterAllServicesForContext(context.Background(), "ctx", []string{"app", "broken"}, usedPorts, registered)

		// then
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(result.Tunnels) != 1 || result.Tunnels[0].DNSURL != "web.app" || result.Tunnels[0].ForwardKey == "" {
			t.Fatalf("tunnels = %+v, want only web.app with a forward key", result.Tunnels)
		}
		if len(forwards.started) != 1 || forwards.started[0].pod != "web-1" {
			t.Fatalf("started = %+v, want exactly one forward for web-1", forwards.started)
		}
		if !usedPorts[result.Tunnels[0].LocalPort] {
			t.Fatalf("usedPorts = %v, want local port %d marked", usedPorts, result.Tunnels[0].LocalPort)
		}
		assertFailures(t, result.Failures, map[string]string{
			"app/api": "already registered",
			"app/ext": "service has no selector",
			"broken/": "forbidden",
		})
	})

	t.Run("네임스페이스를 주지 않으면 시스템 네임스페이스를 뺀 목록을 새로 조회한다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}}
		namespaces := &fakeNamespaceClient{nonSystem: []string{"app"}}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, namespaces, &fakePortForwardClient{})

		// when
		result, err := adapter.RegisterAllServicesForContext(context.Background(), "ctx", nil, map[int32]bool{}, nil)

		// then
		if err != nil || !namespaces.nonSystemCalled || len(result.Tunnels) != 1 {
			t.Fatalf("err = %v, called = %v, tunnels = %+v, want nil, true, 1 tunnel", err, namespaces.nonSystemCalled, result.Tunnels)
		}
	})

	t.Run("네임스페이스 목록 조회에 실패하면 설정 오류로 반환한다", func(t *testing.T) {
		// given
		namespaces := &fakeNamespaceClient{err: errors.New("unauthorized")}
		adapter := newTestAdapter(&fakePodClient{}, &fakeServiceClient{}, namespaces, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterAllServicesForContext(context.Background(), "ctx", nil, map[int32]bool{}, nil)

		// then
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("err = %v, want namespace listing error", err)
		}
	})

	t.Run("시도할 서비스도 실패도 없으면 적합한 서비스가 없다는 오류를 낸다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app": {httpService("db", "app", selectorApp, ServicePort{Port: 5432, Protocol: "TCP"})},
		}}
		adapter := newTestAdapter(&fakePodClient{}, services, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterAllServicesForContext(context.Background(), "ctx", []string{"app"}, map[int32]bool{}, nil)

		// then
		if !errors.Is(err, ErrNoEligibleServices) || err.Error() != "no eligible services found" {
			t.Fatalf("err = %v, want no eligible services found", err)
		}
	})

	t.Run("포워딩 시작이 실패한 서비스는 실패로 기록하고 로컬 포트를 점유 처리하지 않는다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}}
		forwards := &fakePortForwardClient{startFn: func(string) error { return errors.New("dial failed") }}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		usedPorts := map[int32]bool{}

		// when
		result, err := adapter.RegisterAllServicesForContext(context.Background(), "ctx", []string{"app"}, usedPorts, nil)

		// then
		if err != nil || len(result.Tunnels) != 0 || len(usedPorts) != 0 {
			t.Fatalf("err = %v, tunnels = %+v, used = %v, want nil, none, none", err, result.Tunnels, usedPorts)
		}
		assertFailures(t, result.Failures, map[string]string{"app/web": "dial failed"})
	})
}

func assertFailures(t *testing.T, got []ServiceRegistrationFailure, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("failures = %+v, want %d entries", got, len(want))
	}
	keys := make([]string, 0, len(got))
	for _, f := range got {
		key := f.Namespace + "/" + f.Service
		keys = append(keys, key)
		reason, ok := want[key]
		if !ok || !strings.Contains(f.Reason, reason) {
			t.Fatalf("failure %q reason = %q, want it to contain %q (all: %v)", key, f.Reason, reason, keys)
		}
	}
}

func TestRegisterServicePortForward(t *testing.T) {
	t.Run("지원하는 HTTP 포트가 없으면 지원 포트 목록을 담아 오류를 낸다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app": {httpService("db", "app", selectorApp, ServicePort{Port: 5432, Protocol: "TCP"})},
		}}
		adapter := newTestAdapter(&fakePodClient{}, services, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", "db", "app", map[int32]bool{})

		// then
		if !errors.Is(err, errNoSupportedHTTPPort) || !strings.Contains(err.Error(), "80, 8080, 3000, 8000, 9000/TCP") {
			t.Fatalf("err = %v, want unsupported HTTP port error", err)
		}
	})

	t.Run("서비스가 최신 목록에 없으면 찾을 수 없다는 오류를 낸다", func(t *testing.T) {
		// given
		adapter := newTestAdapter(&fakePodClient{}, &fakeServiceClient{}, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", "ghost", "app", map[int32]bool{})

		// then
		if err == nil || !strings.Contains(err.Error(), "app/ghost") {
			t.Fatalf("err = %v, want not found error naming the service", err)
		}
	})

	t.Run("8080 포트 서비스도 svc.ns 호스트명의 터널을 만든다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app": {httpService("api", "app", selectorApp, ServicePort{Port: 8080, TargetPortName: "http", Protocol: "TCP"})},
		}}
		pods := &fakePodClient{pods: []Pod{readyPod("api-1", PodPort{Name: "http", ContainerPort: 9999})}}
		adapter := newTestAdapter(pods, services, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "ctx", "api", "app", map[int32]bool{})

		// then
		if err != nil || tunnel.DNSURL != "api.app" || tunnel.RemotePort != 9999 || tunnel.Pod != "api-1" || tunnel.ForwardKey == "" {
			t.Fatalf("tunnel = %+v, err = %v, want api.app to api-1:9999 with a key", tunnel, err)
		}
	})
}

func TestUnregisterServicePortForward(t *testing.T) {
	t.Run("없는 키를 해제하면 ErrPortForwardNotFound를 그대로 전달한다", func(t *testing.T) {
		// given
		adapter := newTestAdapter(&fakePodClient{}, &fakeServiceClient{}, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		err := adapter.UnregisterServicePortForward("missing")

		// then
		if !errors.Is(err, ErrPortForwardNotFound) {
			t.Fatalf("err = %v, want ErrPortForwardNotFound", err)
		}
	})
}
