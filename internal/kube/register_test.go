package kube

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func httpService(name, namespace string, selector map[string]string, ports ...ServicePort) Service {
	if len(ports) == 0 {
		ports = []ServicePort{{Name: "http", Port: 80, Protocol: "TCP"}}
	}
	return Service{Name: name, Namespace: namespace, ClusterIP: "10.0.0.1", Type: "ClusterIP", Ports: ports, Selector: selector}
}

func refs(pairs ...string) []ServiceRef {
	targets := make([]ServiceRef, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		targets = append(targets, ServiceRef{Namespace: pairs[i], Name: pairs[i+1]})
	}
	return targets
}

func TestRegisterServicesForContext(t *testing.T) {
	t.Run("대상을 주어진 순서대로 처리하고 진행 상황을 정확한 순서로 알린다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app":   {httpService("web", "app", selectorApp), httpService("api", "app", selectorApp)},
			"other": {httpService("Cache", "other", selectorApp)},
		}}
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		usedPorts := map[int32]bool{}
		var progress []RegistrationProgress
		targets := refs("other", "Cache", "app", "web", "app", "api")

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", targets, usedPorts, nil,
			func(p RegistrationProgress) { progress = append(progress, p) })

		// then
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		gotOrder := make([]string, 0, len(result.Tunnels))
		for _, tunnel := range result.Tunnels {
			gotOrder = append(gotOrder, tunnel.DNSURL)
			if tunnel.ForwardKey == "" || !usedPorts[tunnel.LocalPort] {
				t.Fatalf("tunnel = %+v, usedPorts = %v, want key and marked port", tunnel, usedPorts)
			}
		}
		if !reflect.DeepEqual(gotOrder, []string{"cache.other", "web.app", "api.app"}) {
			t.Fatalf("tunnel order = %v, want target order", gotOrder)
		}
		wantProgress := []RegistrationProgress{
			{Done: 0, Total: 3, Current: "cache.other"},
			{Done: 1, Total: 3, Current: "web.app"},
			{Done: 2, Total: 3, Current: "api.app"},
			{Done: 3, Total: 3},
		}
		if !reflect.DeepEqual(progress, wantProgress) {
			t.Fatalf("progress = %+v, want %+v", progress, wantProgress)
		}
	})

	t.Run("대상이 분류별로 나뉘고 이미 등록된 호스트명은 소유 컨텍스트와 함께 건너뛴다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app": {
				httpService("web", "app", selectorApp),
				httpService("api", "app", selectorApp, ServicePort{Port: 8080, Protocol: "TCP"}),
				httpService("db", "app", selectorApp, ServicePort{Port: 5432, Protocol: "TCP"}),
				httpService("ext", "app", nil),
			},
		}}
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		owners := map[string]string{"api.app": "prod"}
		targets := refs("app", "web", "app", "api", "app", "db", "app", "ext", "app", "ghost")

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", targets, map[int32]bool{}, owners, nil)

		// then
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(result.Tunnels) != 1 || result.Tunnels[0].DNSURL != "web.app" || len(forwards.started) != 1 {
			t.Fatalf("tunnels = %+v, started = %+v, want only web.app forwarded", result.Tunnels, forwards.started)
		}
		assertFailures(t, result.Failures, map[string]string{"app/ghost": "not found"})
		assertFailures(t, result.Skipped, map[string]string{"app/api": "already registered (context prod)"})
		assertFailures(t, result.Ineligible, map[string]string{"app/db": ReasonNoHTTPPort, "app/ext": ReasonNoSelector})
		if len(owners) != 1 {
			t.Fatalf("owners = %v, want caller map untouched", owners)
		}
	})

	t.Run("같은 서비스가 두 번 들어오면 두 번째는 건너뛴다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}}
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", refs("app", "web", "app", "web"), map[int32]bool{}, nil, nil)

		// then
		if err != nil || len(result.Tunnels) != 1 || len(forwards.started) != 1 {
			t.Fatalf("err = %v, tunnels = %+v, want one tunnel", err, result.Tunnels)
		}
		assertFailures(t, result.Skipped, map[string]string{"app/web": "already registered (context ctx)"})
	})

	t.Run("네임스페이스 조회에 실패하면 그 네임스페이스의 모든 대상이 실패로 기록되고 다른 네임스페이스는 계속 처리한다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{
			byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}},
			errs:        map[string]error{"broken": errors.New("forbidden")},
		}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, &fakePortForwardClient{})
		targets := refs("broken", "a", "app", "web", "broken", "b")

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", targets, map[int32]bool{}, nil, nil)

		// then
		if err != nil || len(result.Tunnels) != 1 {
			t.Fatalf("err = %v, tunnels = %+v, want nil and one tunnel", err, result.Tunnels)
		}
		assertFailures(t, result.Failures, map[string]string{"broken/a": "forbidden", "broken/b": "forbidden"})
	})

	t.Run("컨텍스트가 취소되면 지금까지의 결과와 취소 오류를 돌려주고 멈춘다", func(t *testing.T) {
		// given
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		services := &fakeServiceClient{byNamespace: map[string][]Service{
			"app": {httpService("web", "app", selectorApp), httpService("api", "app", selectorApp)},
		}}
		forwards := &fakePortForwardClient{startFn: func(string) error { cancel(); return nil }}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		var progress []RegistrationProgress

		// when
		result, err := adapter.RegisterServicesForContext(ctx, "ctx", refs("app", "web", "app", "api"), map[int32]bool{}, nil,
			func(p RegistrationProgress) { progress = append(progress, p) })

		// then
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if len(result.Tunnels) != 1 || result.Tunnels[0].DNSURL != "web.app" || len(forwards.started) != 1 {
			t.Fatalf("tunnels = %+v, want only the first target", result.Tunnels)
		}
		if len(progress) != 1 || progress[0].Current != "web.app" {
			t.Fatalf("progress = %+v, want only the first target announced", progress)
		}
	})

	t.Run("대상이 비어 있으면 적합한 서비스가 없다는 오류를 낸다", func(t *testing.T) {
		// given
		adapter := newTestAdapter(&fakePodClient{}, &fakeServiceClient{}, &fakeNamespaceClient{}, &fakePortForwardClient{})
		called := false

		// when
		_, err := adapter.RegisterServicesForContext(context.Background(), "ctx", nil, map[int32]bool{}, nil,
			func(RegistrationProgress) { called = true })

		// then
		if !errors.Is(err, ErrNoEligibleServices) || err.Error() != "no eligible services found" || called {
			t.Fatalf("err = %v, progress called = %v, want no eligible services found without progress", err, called)
		}
	})

	t.Run("포워딩 시작이 실패한 서비스는 실패로 기록하고 로컬 포트를 점유 처리하지 않는다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}}
		forwards := &fakePortForwardClient{startFn: func(string) error { return errors.New("dial failed") }}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		usedPorts := map[int32]bool{}

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", refs("app", "web"), usedPorts, nil, nil)

		// then
		if err != nil || len(result.Tunnels) != 0 || len(usedPorts) != 0 {
			t.Fatalf("err = %v, tunnels = %+v, used = %v, want nil, none, none", err, result.Tunnels, usedPorts)
		}
		assertFailures(t, result.Failures, map[string]string{"app/web": "dial failed"})
	})

	t.Run("클라이언트를 만들지 못하면 오류를 돌려준다", func(t *testing.T) {
		// given
		adapter := newTestAdapter(&fakePodClient{}, &fakeServiceClient{}, &fakeNamespaceClient{}, &fakePortForwardClient{})
		adapter.newClient = func(string) (*rest.Config, kubernetes.Interface, error) { return nil, nil, errors.New("no kubeconfig") }

		// when
		_, err := adapter.RegisterServicesForContext(context.Background(), "ctx", refs("app", "web"), map[int32]bool{}, nil, nil)

		// then
		if err == nil || !strings.Contains(err.Error(), "no kubeconfig") {
			t.Fatalf("err = %v, want client factory error", err)
		}
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

	t.Run("셀렉터가 없는 서비스는 파드를 찾지 않고 셀렉터 없음 오류를 낸다", func(t *testing.T) {
		// given
		services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("ext", "app", nil)}}}
		pods := &fakePodClient{}
		adapter := newTestAdapter(pods, services, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", "ext", "app", map[int32]bool{})

		// then
		if !errors.Is(err, errServiceHasNoSelector) || pods.calls != 0 {
			t.Fatalf("err = %v, pod lookups = %d, want no selector error without pod lookup", err, pods.calls)
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
