package kube

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// newAPIAdapter builds a real adapter whose single context "dev" talks to a fake API server serving one
// web service backed by one ready pod, with port forwards answered by an echoing SPDY server.
func newAPIAdapter(t *testing.T) (KubeAdapterInterface, *spdyForwardServer, *fakeAPI) {
	t.Helper()
	forwardServer := newSPDYForwardServer(t)
	api := newFakeAPI(t, apiFixture{
		namespaces: []string{"app", "kube-system"},
		services: map[string][]corev1.Service{"app": {
			clusterIPService("app", "web", map[string]string{"app": "web"}),
			clusterIPService("app", "ext", nil),
		}},
		pods: map[string][]corev1.Pod{"app": {
			runningCorePod("app", "web-1", map[string]string{"app": "web"}, true),
		}},
		forward: forwardServer,
	})
	adapter, err := NewKubeAdapter(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))
	if err != nil {
		t.Fatalf("NewKubeAdapter() error = %v", err)
	}
	t.Cleanup(adapter.StopAllPortForwards)
	return adapter, forwardServer, api
}

func TestNewKubeAdapter(t *testing.T) {
	t.Run("kubeconfig 파일이 없으면 오류를 낸다", func(t *testing.T) {
		// given
		missing := filepath.Join(t.TempDir(), "missing")

		// when
		adapter, err := NewKubeAdapter(missing)

		// then
		if err == nil || adapter != nil || !strings.Contains(err.Error(), "kubeconfig file not found") {
			t.Fatalf("NewKubeAdapter() = %v, %v, want not found error", adapter, err)
		}
	})

	t.Run("경로가 비어 있고 홈 디렉터리를 알 수 없으면 오류를 낸다", func(t *testing.T) {
		// given
		if os.Geteuid() == 0 {
			t.Skip("requires a non-root process")
		}
		t.Setenv("HOME", "")

		// when
		adapter, err := NewKubeAdapter("")

		// then
		if err == nil || adapter != nil {
			t.Fatalf("NewKubeAdapter() = %v, %v, want error", adapter, err)
		}
	})

	t.Run("만들어진 어댑터가 컨텍스트와 네임스페이스와 서비스를 실제 클라이언트로 조회한다", func(t *testing.T) {
		// given
		adapter, _, _ := newAPIAdapter(t)
		ctx := context.Background()

		// when
		current, currentErr := adapter.CurrentContext(ctx)
		contexts, contextsErr := adapter.ListContexts(ctx)
		namespaces, namespacesErr := adapter.ListNamespaces(ctx, "dev")
		services, servicesErr := adapter.ListServices(ctx, "app", "dev")

		// then
		if currentErr != nil || current != "dev" {
			t.Fatalf("CurrentContext() = %q, %v, want dev", current, currentErr)
		}
		if contextsErr != nil || !reflect.DeepEqual(contexts, []Context{{Name: "dev", Cluster: "cluster-dev", User: "user-dev"}}) {
			t.Fatalf("ListContexts() = %+v, %v", contexts, contextsErr)
		}
		if namespacesErr != nil || !reflect.DeepEqual(namespaces, []string{"app", "kube-system"}) {
			t.Fatalf("ListNamespaces() = %v, %v", namespaces, namespacesErr)
		}
		if servicesErr != nil || len(services) != 2 || services[0].Name != "web" {
			t.Fatalf("ListServices() = %+v, %v, want web and ext", services, servicesErr)
		}
	})
}

func TestKubeAdapter_RealPortForward(t *testing.T) {
	t.Run("서비스를 등록하면 로컬 포트가 파드로 이어지고 해제하면 포트가 풀린다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		local := firstFreePortFrom(t, minLocalPort)

		// when
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "dev",
			ServiceRef{Namespace: "app", Name: "web", LocalPort: local}, map[int32]bool{})

		// then
		if err != nil {
			t.Fatalf("RegisterServicePortForward() error = %v", err)
		}
		if tunnel.Pod != "web-1" || tunnel.LocalPort != local || tunnel.RemotePort != 8080 || tunnel.DNSURL != "web.app" {
			t.Fatalf("tunnel = %+v, want web.app on local %d to web-1:8080", tunnel, local)
		}
		if requests := api.requestsWith("/pods", "labelSelector="); len(requests) != 1 || !strings.Contains(requests[0], "labelSelector=app%3Dweb") {
			t.Fatalf("pod requests = %v, want one selector lookup for app=web", requests)
		}
		assertEcho(t, local)

		if err := adapter.UnregisterServicePortForward(tunnel.ForwardKey); err != nil {
			t.Fatalf("UnregisterServicePortForward() error = %v", err)
		}
		waitUntil(t, func() bool { return isLocalPortFree(local) }, "local port to be released after unregister")
	})

	t.Run("StartForward는 지정한 파드와 포트로 포워딩을 열고 StopAllPortForwards가 모두 닫는다", func(t *testing.T) {
		// given
		adapter, _, _ := newAPIAdapter(t)
		local := firstFreePortFrom(t, minLocalPort)

		// when
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		adapter.StopAllPortForwards()

		// then
		if err != nil || key != BuildPortForwardKey("dev", "app", "web-1", 8080, local) {
			t.Fatalf("StartForward() = %q, %v", key, err)
		}
		waitUntil(t, func() bool { return isLocalPortFree(local) }, "local port to be released after stop all")
		if err := adapter.UnregisterServicePortForward(key); !errors.Is(err, ErrPortForwardNotFound) {
			t.Fatalf("UnregisterServicePortForward() = %v, want ErrPortForwardNotFound", err)
		}
	})

	t.Run("파드 연결이 끊기면 등록한 핸들러가 포워딩 키로 호출된다", func(t *testing.T) {
		// given
		adapter, forwardServer, _ := newAPIAdapter(t)
		lost := make(chan string, 1)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- key })
		local := firstFreePortFrom(t, minLocalPort)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}

		// when
		forwardServer.dropConnections()

		// then
		select {
		case got := <-lost:
			if got != key {
				t.Fatalf("lost key = %q, want %q", got, key)
			}
		case <-time.After(testWait):
			t.Fatal("lost handler was not called")
		}
	})

	t.Run("존재하지 않는 컨텍스트로 StartForward하면 오류를 낸다", func(t *testing.T) {
		// given
		adapter, _, _ := newAPIAdapter(t)

		// when
		key, err := adapter.StartForward(context.Background(), "ghost", "app", "web-1", 8080, 40000)

		// then
		if err == nil || key != "" {
			t.Fatalf("StartForward() = %q, %v, want error", key, err)
		}
	})
}

func TestRegisterServicePortForward_Failures(t *testing.T) {
	services := &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}}
	target := ServiceRef{Namespace: "app", Name: "web"}

	t.Run("서비스 목록 조회가 실패하면 목록 오류로 감싼다", func(t *testing.T) {
		// given
		cause := errors.New("api down")
		failing := &fakeServiceClient{errs: map[string]error{"app": cause}}
		adapter := newTestAdapter(&fakePodClient{}, failing, &fakeNamespaceClient{}, &fakePortForwardClient{})

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", target, map[int32]bool{})

		// then
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "list services") {
			t.Fatalf("err = %v, want wrapped list services error", err)
		}
	})

	t.Run("클라이언트를 만들 수 없으면 포워딩을 시작하지 않고 오류를 낸다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		adapter.newClient = failingClientFactory

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", target, map[int32]bool{})

		// then
		if err == nil || len(forwards.started) != 0 {
			t.Fatalf("err = %v, started = %+v, want error without forwards", err, forwards.started)
		}
	})

	t.Run("파드 조회가 실패하면 서비스 이름을 담은 오류를 낸다", func(t *testing.T) {
		// given
		cause := errors.New("pods unavailable")
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{err: cause}, services, &fakeNamespaceClient{}, forwards)

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", target, map[int32]bool{})

		// then
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), "app/web") || len(forwards.started) != 0 {
			t.Fatalf("err = %v, started = %+v, want wrapped pod error naming app/web", err, forwards.started)
		}
	})

	t.Run("사용할 수 있는 로컬 포트가 없으면 포워딩을 시작하지 않고 오류를 낸다", func(t *testing.T) {
		// given
		forwards := &fakePortForwardClient{}
		adapter := newTestAdapter(&fakePodClient{pods: []Pod{readyPod("web-1")}}, services, &fakeNamespaceClient{}, forwards)
		used := make(map[int32]bool)
		for port := int32(minLocalPort); port <= maxLocalPort; port++ {
			used[port] = true
		}

		// when
		_, err := adapter.RegisterServicePortForward(context.Background(), "ctx", target, used)

		// then
		if err == nil || !strings.Contains(err.Error(), "find available port") || len(forwards.started) != 0 {
			t.Fatalf("err = %v, started = %+v, want find available port error", err, forwards.started)
		}
	})
}

func assertEcho(t *testing.T, port int32) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))), testWait)
	if err != nil {
		t.Fatalf("dial forwarded port: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(testWait))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("echo = %q, %v, want ping", buf, err)
	}
}

func waitUntil(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
