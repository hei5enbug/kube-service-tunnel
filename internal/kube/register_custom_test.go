package kube

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func customAdapter(forwards *fakePortForwardClient, services ...Service) *kubeAdapter {
	byNamespace := map[string][]Service{}
	for _, svc := range services {
		byNamespace[svc.Namespace] = append(byNamespace[svc.Namespace], svc)
	}
	return newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, &fakeServiceClient{byNamespace: byNamespace}, &fakeNamespaceClient{}, forwards)
}

func TestServiceRefEffectiveHostname(t *testing.T) {
	tests := []struct {
		name string
		ref  ServiceRef
		want string
	}{
		{"호스트 이름이 없으면 서비스 DNS 이름을 쓴다", ServiceRef{Namespace: "App", Name: "Web"}, "web.app"},
		{"호스트 이름이 있으면 소문자로 바꿔 쓴다", ServiceRef{Namespace: "app", Name: "web", Hostname: "My.Local"}, "my.local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			ref := tt.ref

			// when
			got := ref.EffectiveHostname()

			// then
			if got != tt.want {
				t.Fatalf("hostname = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRegisterServicePortForwardLocalPort(t *testing.T) {
	t.Run("원하는 포트가 비어 있으면 그 포트를 쓰고 요청 포트를 기록한다", func(t *testing.T) {
		// given
		preferred := firstFreePortFrom(t, 45000)
		forwards := &fakePortForwardClient{}
		adapter := customAdapter(forwards, httpService("web", "app", selectorApp))
		usedPorts := map[int32]bool{}

		// when
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "ctx", ServiceRef{Namespace: "app", Name: "web", LocalPort: preferred}, usedPorts)

		// then
		if err != nil || tunnel.LocalPort != preferred || tunnel.RequestedLocalPort != preferred || tunnel.Service != "web" {
			t.Fatalf("tunnel = %+v, err = %v, want port %d", tunnel, err, preferred)
		}
		if len(forwards.started) != 1 || forwards.started[0].localPort != preferred || !usedPorts[preferred] {
			t.Fatalf("started = %+v, usedPorts = %v", forwards.started, usedPorts)
		}
	})

	t.Run("원하는 포트를 이미 점유했거나 다른 터널이 쓰면 자동 포트로 대체하고 요청 포트는 유지한다", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer listener.Close()
		busyPort := int32(listener.Addr().(*net.TCPAddr).Port)
		usedPort := firstFreePortFrom(t, 46000)

		tests := []struct {
			name      string
			preferred int32
			usedPorts map[int32]bool
		}{
			{"로컬에서 점유된 포트", busyPort, map[int32]bool{}},
			{"다른 터널이 쓰는 포트", usedPort, map[int32]bool{usedPort: true}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// given
				adapter := customAdapter(&fakePortForwardClient{}, httpService("web", "app", selectorApp))

				// when
				tunnel, err := adapter.RegisterServicePortForward(context.Background(), "ctx", ServiceRef{Namespace: "app", Name: "web", LocalPort: tt.preferred}, tt.usedPorts)

				// then
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if tunnel.LocalPort == tt.preferred || tunnel.LocalPort < minLocalPort || tunnel.RequestedLocalPort != tt.preferred {
					t.Fatalf("tunnel = %+v, want automatic fallback with requested %d kept", tunnel, tt.preferred)
				}
			})
		}
	})

	t.Run("선택한 파드의 UID를 포워딩에 묶는다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		pod := readyPod("web-1")
		pod.UID = "uid-5"
		adapter := customAdapter(forwards, httpService("web", "app", selectorApp))
		adapter.podClient = &fakePodClient{pods: []Pod{pod}}

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", ServiceRef{Namespace: "app", Name: "web"}, map[int32]bool{})

		// then
		if err != nil || len(forwards.started) != 1 || forwards.started[0].expectedUID != "uid-5" {
			t.Fatalf("err = %v, started = %+v, want one forward tied to uid-5", err, forwards.started)
		}
	})

	t.Run("사용자 지정 호스트 이름이 DNS 주소가 된다", func(t *testing.T) {
		// given
		adapter := customAdapter(&fakePortForwardClient{}, httpService("web", "app", selectorApp))

		// when
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "ctx", ServiceRef{Namespace: "app", Name: "web", Hostname: "Custom.Local"}, map[int32]bool{})

		// then
		if err != nil || tunnel.DNSURL != "custom.local" || tunnel.RequestedLocalPort != 0 {
			t.Fatalf("tunnel = %+v, err = %v", tunnel, err)
		}
	})
}

func TestRegisterServicesForContextCustomSettings(t *testing.T) {
	t.Run("사용자 지정 호스트 이름이 소유자 확인, 진행 상황, 기록되는 소유자, DNS 주소를 결정한다", func(t *testing.T) {
		// given
		adapter := customAdapter(&fakePortForwardClient{},
			httpService("web", "app", selectorApp), httpService("api", "app", selectorApp), httpService("db", "app", selectorApp))
		targets := []ServiceRef{
			{Namespace: "app", Name: "web", Hostname: "Shared.Local"},
			{Namespace: "app", Name: "api", Hostname: "shared.local"},
			{Namespace: "app", Name: "db", Hostname: "taken.local"},
		}
		owners := map[string]string{"taken.local": "other-ctx", "db.app": ""}
		var progress []RegistrationProgress

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", targets, map[int32]bool{}, owners,
			func(p RegistrationProgress) { progress = append(progress, p) })

		// then
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if len(result.Tunnels) != 1 || result.Tunnels[0].DNSURL != "shared.local" || result.Tunnels[0].Service != "web" {
			t.Fatalf("tunnels = %+v, want only web on shared.local", result.Tunnels)
		}
		wantSkipped := []ServiceRegistrationFailure{
			{Namespace: "app", Service: "api", Reason: "already registered (context ctx)"},
			{Namespace: "app", Service: "db", Reason: "already registered (context other-ctx)"},
		}
		if !reflect.DeepEqual(result.Skipped, wantSkipped) {
			t.Fatalf("skipped = %+v, want %+v", result.Skipped, wantSkipped)
		}
		wantProgress := []RegistrationProgress{
			{Done: 0, Total: 3, Current: "shared.local"},
			{Done: 1, Total: 3, Current: "shared.local"},
			{Done: 2, Total: 3, Current: "taken.local"},
			{Done: 3, Total: 3},
		}
		if !reflect.DeepEqual(progress, wantProgress) {
			t.Fatalf("progress = %+v, want %+v", progress, wantProgress)
		}
		if owners["shared.local"] != "" {
			t.Fatalf("caller owners map was mutated: %v", owners)
		}
	})

	t.Run("대상의 원하는 포트를 적용하고 다른 대상은 자동 포트를 쓴다", func(t *testing.T) {
		// given
		preferred := firstFreePortFrom(t, 47000)
		adapter := customAdapter(&fakePortForwardClient{}, httpService("web", "app", selectorApp), httpService("api", "app", selectorApp))
		targets := []ServiceRef{{Namespace: "app", Name: "web", LocalPort: preferred}, {Namespace: "app", Name: "api"}}

		// when
		result, err := adapter.RegisterServicesForContext(context.Background(), "ctx", targets, map[int32]bool{}, nil, nil)

		// then
		if err != nil || len(result.Tunnels) != 2 {
			t.Fatalf("err = %v, result = %+v", err, result)
		}
		if result.Tunnels[0].LocalPort != preferred || result.Tunnels[1].RequestedLocalPort != 0 || result.Tunnels[1].LocalPort < minLocalPort {
			t.Fatalf("tunnels = %+v", result.Tunnels)
		}
	})
}

func TestCheckLocalPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	busyPort := int32(listener.Addr().(*net.TCPAddr).Port)
	freePort := firstFreePortFrom(t, 48000)

	tests := []struct {
		name string
		port int32
		used map[int32]bool
		want string
	}{
		{"비어 있는 포트는 통과한다", freePort, nil, ""},
		{"0은 범위를 벗어난다", 0, nil, "local port 0 is out of range 1-65535"},
		{"음수는 범위를 벗어난다", -5, nil, "local port -5 is out of range 1-65535"},
		{"65535를 넘으면 범위를 벗어난다", 65536, nil, "local port 65536 is out of range 1-65535"},
		{"다른 터널이 쓰는 포트는 거절한다", freePort, map[int32]bool{freePort: true}, fmt.Sprintf("local port %d is used by another tunnel", freePort)},
		{"로컬에서 점유된 포트는 거절한다", busyPort, nil, fmt.Sprintf("local port %d is already in use on 127.0.0.1", busyPort)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			port, used := tt.port, tt.used

			// when
			err := CheckLocalPort(port, used)

			// then
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case tt.want != "" && (err == nil || err.Error() != tt.want):
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestNextFreeLocalPort(t *testing.T) {
	t.Run("사용 중으로 표시된 포트를 건너뛰고 40000 이상의 포트를 고른다", func(t *testing.T) {
		// given
		first := firstFreePortFrom(t, minLocalPort)

		// when
		got, err := NextFreeLocalPort(map[int32]bool{first: true})

		// then
		if err != nil || got == first || got < minLocalPort {
			t.Fatalf("port = %d, err = %v", got, err)
		}
	})
}

func TestStartForward(t *testing.T) {
	t.Run("포워딩을 열기 전에 파드를 한 번 읽어 그 UID를 포워딩에 묶는다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := customAdapter(forwards)
		pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app", UID: "uid-77"}}
		clientset := fake.NewClientset(&pod)
		adapter.newClient = func(string) (*rest.Config, kubernetes.Interface, error) { return &rest.Config{}, clientset, nil }

		// when
		_, err := adapter.StartForward(context.Background(), "ctx", "app", "web-1", 8080, 41234)

		// then
		if err != nil || len(forwards.started) != 1 || forwards.started[0].expectedUID != "uid-77" {
			t.Fatalf("err = %v, started = %+v, want one forward tied to uid-77", err, forwards.started)
		}
	})

	t.Run("파드를 읽지 못하면 포워딩을 열지 않고 오류를 돌려준다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := customAdapter(forwards)
		clientset := fake.NewClientset()
		adapter.newClient = func(string) (*rest.Config, kubernetes.Interface, error) { return &rest.Config{}, clientset, nil }

		// when
		key, err := adapter.StartForward(context.Background(), "ctx", "app", "web-1", 8080, 41234)

		// then
		if err == nil || key != "" || !strings.Contains(err.Error(), "read pod app/web-1") || len(forwards.started) != 0 {
			t.Fatalf("key = %q, err = %v, started = %+v, want a read pod error and no forward", key, err, forwards.started)
		}
	})

	t.Run("원격 포트와 로컬 포트를 올바른 순서로 포트 포워드 클라이언트에 전달한다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := customAdapter(forwards)

		// when
		key, err := adapter.StartForward(context.Background(), "ctx", "app", "web-1", 8080, 41234)

		// then
		if err != nil || key != BuildPortForwardKey("ctx", "app", "web-1", 8080, 41234) {
			t.Fatalf("key = %q, err = %v", key, err)
		}
		want := []fakeStartedForward{{namespace: "app", pod: "web-1", localPort: 41234, remotePort: 8080}}
		if !reflect.DeepEqual(forwards.started, want) {
			t.Fatalf("started = %+v, want %+v", forwards.started, want)
		}
	})

	t.Run("클라이언트 생성에 실패하면 포워드를 시작하지 않고 오류를 돌려준다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := customAdapter(forwards)
		adapter.newClient = failingClientFactory

		// when
		_, err := adapter.StartForward(context.Background(), "ctx", "app", "web-1", 80, 41234)

		// then
		if err == nil || len(forwards.started) != 0 {
			t.Fatalf("err = %v, started = %+v", err, forwards.started)
		}
	})
}
