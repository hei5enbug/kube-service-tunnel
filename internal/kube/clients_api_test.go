package kube

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestNamespaceClient_ListNamespaces(t *testing.T) {
	t.Run("API 서버의 네임스페이스를 시스템 네임스페이스까지 모두 돌려준다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{namespaces: []string{"default", "kube-system", "app"}})
		client, err := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))
		if err != nil {
			t.Fatalf("NewNamespaceClient() error = %v", err)
		}

		// when
		got, err := client.ListNamespaces(context.Background(), "dev")

		// then
		want := []string{"default", "kube-system", "app"}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("ListNamespaces() = %v, %v, want %v", got, err, want)
		}
	})

	t.Run("컨텍스트 이름이 다르면 해당 컨텍스트의 서버에서 조회한다", func(t *testing.T) {
		// given
		dev := newFakeAPI(t, apiFixture{namespaces: []string{"dev-ns"}})
		prod := newFakeAPI(t, apiFixture{namespaces: []string{"prod-ns"}})
		client, _ := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": dev.URL(), "prod": prod.URL()}))

		// when
		got, err := client.ListNamespaces(context.Background(), "prod")

		// then
		if err != nil || !reflect.DeepEqual(got, []string{"prod-ns"}) {
			t.Fatalf("ListNamespaces() = %v, %v, want [prod-ns]", got, err)
		}
	})

	t.Run("네임스페이스가 없으면 빈 목록을 돌려준다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{})
		client, _ := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.ListNamespaces(context.Background(), "dev")

		// then
		if err != nil || len(got) != 0 {
			t.Fatalf("ListNamespaces() = %v, %v, want empty", got, err)
		}
	})

	t.Run("API 서버가 오류를 돌려주면 목록 조회 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces": 500}})
		client, _ := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.ListNamespaces(context.Background(), "dev")

		// then
		if err == nil || got != nil || !strings.Contains(err.Error(), "list namespaces") {
			t.Fatalf("ListNamespaces() = %v, %v, want list namespaces error", got, err)
		}
	})

	t.Run("API 서버가 인가를 거부하면 목록 조회 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces": 403}})
		client, _ := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		_, err := client.ListNamespaces(context.Background(), "dev")

		// then
		if err == nil || !strings.Contains(err.Error(), "list namespaces") {
			t.Fatalf("err = %v, want list namespaces error", err)
		}
	})

	t.Run("kubeconfig를 읽지 못하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		client, _ := NewNamespaceClient(filepath.Join(t.TempDir(), "missing"))

		// when
		got, err := client.ListNamespaces(context.Background(), "dev")

		// then
		if err == nil || got != nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("ListNamespaces() = %v, %v, want load error", got, err)
		}
	})

	t.Run("없는 컨텍스트를 지정하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{})
		client, _ := NewNamespaceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		_, err := client.ListNamespaces(context.Background(), "ghost")

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("err = %v, want load error", err)
		}
	})

	t.Run("설정으로 클라이언트를 만들 수 없으면 생성 오류를 낸다", func(t *testing.T) {
		// given
		raw := "apiVersion: v1\nkind: Config\ncurrent-context: dev\n" +
			"clusters:\n- name: c\n  cluster:\n    server: https://127.0.0.1:1\n    certificate-authority-data: bm90IGEgY2VydGlmaWNhdGU=\n" +
			"users:\n- name: u\n  user: {}\n" +
			"contexts:\n- name: dev\n  context:\n    cluster: c\n    user: u\n"
		client, _ := NewNamespaceClient(writeRawKubeconfig(t, raw))

		// when
		_, err := client.ListNamespaces(context.Background(), "dev")

		// then
		if err == nil || !strings.Contains(err.Error(), "create kubernetes client") {
			t.Fatalf("err = %v, want create client error", err)
		}
	})
}

func TestServiceClient_ListServices(t *testing.T) {
	services := map[string][]corev1.Service{"app": {
		clusterIPService("app", "web", map[string]string{"app": "web"}),
		clusterIPService("app", "named", map[string]string{"app": "named"},
			corev1.ServicePort{Name: "http", Port: 8080, TargetPort: intstr.FromString("web"), Protocol: corev1.ProtocolTCP}),
		{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ClusterIP: "10.0.0.2"}},
		{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.3"}},
		{Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: corev1.ClusterIPNone}},
	}}

	t.Run("NodePort와 LoadBalancer와 헤드리스 서비스를 제외한 서비스만 변환해 돌려준다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{services: services})
		client, err := NewServiceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))
		if err != nil {
			t.Fatalf("NewServiceClient() error = %v", err)
		}

		// when
		got, err := client.ListServices(context.Background(), "app", "dev")

		// then
		if err != nil || len(got) != 2 {
			t.Fatalf("ListServices() = %+v, %v, want 2 services", got, err)
		}
		if got[0].Name != "web" || got[0].Namespace != "app" || got[0].Type != "ClusterIP" || got[0].Selector["app"] != "web" ||
			len(got[0].Ports) != 1 || got[0].Ports[0].TargetPort != 8080 {
			t.Fatalf("first service = %+v, want web with numeric target port 8080", got[0])
		}
		if got[1].Ports[0].TargetPortName != "web" {
			t.Fatalf("second service = %+v, want named target port", got[1])
		}
	})

	t.Run("서비스가 없는 네임스페이스는 빈 목록을 돌려준다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{services: services})
		client, _ := NewServiceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.ListServices(context.Background(), "empty", "dev")

		// then
		if err != nil || len(got) != 0 {
			t.Fatalf("ListServices() = %+v, %v, want empty", got, err)
		}
	})

	t.Run("API 서버가 오류를 돌려주면 네임스페이스를 담은 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces/app/services": 500}})
		client, _ := NewServiceClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.ListServices(context.Background(), "app", "dev")

		// then
		if err == nil || got != nil || !strings.Contains(err.Error(), "list services in namespace app") {
			t.Fatalf("ListServices() = %+v, %v, want list services error", got, err)
		}
	})

	t.Run("kubeconfig를 읽지 못하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		client, _ := NewServiceClient(filepath.Join(t.TempDir(), "missing"))

		// when
		_, err := client.ListServices(context.Background(), "app", "dev")

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("err = %v, want load error", err)
		}
	})
}

func TestPodClient_ListPods(t *testing.T) {
	pods := map[string][]corev1.Pod{"app": {
		runningCorePod("app", "web-1", map[string]string{"app": "web"}, true),
		runningCorePod("app", "db-1", map[string]string{"app": "db"}, false),
	}}

	t.Run("네임스페이스의 모든 파드를 변환해 돌려준다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{pods: pods})
		client, err := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))
		if err != nil {
			t.Fatalf("NewPodClient() error = %v", err)
		}

		// when
		got, err := client.ListPods(context.Background(), "app", "dev")

		// then
		if err != nil || len(got) != 2 {
			t.Fatalf("ListPods() = %+v, %v, want 2 pods", got, err)
		}
		if got[0].Name != "web-1" || !got[0].Ready || got[0].Status != "Running" || got[0].Ports[0].ContainerPort != 8080 || got[0].Labels["app"] != "web" {
			t.Fatalf("first pod = %+v, want ready web-1 with port 8080", got[0])
		}
		if got[1].Name != "db-1" || got[1].Ready {
			t.Fatalf("second pod = %+v, want not ready db-1", got[1])
		}
	})

	t.Run("API 서버가 오류를 돌려주면 네임스페이스를 담은 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces/app/pods": 500}})
		client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.ListPods(context.Background(), "app", "dev")

		// then
		if err == nil || got != nil || !strings.Contains(err.Error(), "list pods in namespace app") {
			t.Fatalf("ListPods() = %+v, %v, want list pods error", got, err)
		}
	})

	t.Run("kubeconfig를 읽지 못하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		client, _ := NewPodClient(filepath.Join(t.TempDir(), "missing"))

		// when
		_, err := client.ListPods(context.Background(), "app", "dev")

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("err = %v, want load error", err)
		}
	})
}

func TestPodClient_FindMatchingPods(t *testing.T) {
	pods := map[string][]corev1.Pod{"app": {
		runningCorePod("app", "web-1", map[string]string{"app": "web", "tier": "front"}, true),
		runningCorePod("app", "web-2", map[string]string{"app": "web", "tier": "back"}, true),
		runningCorePod("app", "db-1", map[string]string{"app": "db"}, true),
	}}

	tests := []struct {
		name     string
		selector map[string]string
		want     []string
	}{
		{"셀렉터에 맞는 파드만 돌려준다", map[string]string{"app": "web"}, []string{"web-1", "web-2"}},
		{"모든 레이블이 일치하는 파드만 돌려준다", map[string]string{"app": "web", "tier": "back"}, []string{"web-2"}},
		{"맞는 파드가 없으면 빈 목록을 돌려준다", map[string]string{"app": "cache"}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// given
			api := newFakeAPI(t, apiFixture{pods: pods})
			client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

			// when
			got, err := client.FindMatchingPods(context.Background(), "app", "dev", tt.selector)

			// then
			var names []string
			for _, pod := range got {
				names = append(names, pod.Name)
			}
			if err != nil || !reflect.DeepEqual(names, tt.want) {
				t.Fatalf("FindMatchingPods() = %v, %v, want %v", names, err, tt.want)
			}
			if requests := api.requestsTo("/pods"); len(requests) != 1 || !strings.Contains(requests[0], "labelSelector=") {
				t.Fatalf("requests = %v, want one pod list with a label selector", requests)
			}
		})
	}

	t.Run("API 서버가 오류를 돌려주면 셀렉터 조회 오류를 낸다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces/app/pods": 500}})
		client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		got, err := client.FindMatchingPods(context.Background(), "app", "dev", map[string]string{"app": "web"})

		// then
		if err == nil || got != nil || !strings.Contains(err.Error(), "list pods with selector in namespace app") {
			t.Fatalf("FindMatchingPods() = %+v, %v, want selector list error", got, err)
		}
	})

	t.Run("kubeconfig를 읽지 못하면 로드 오류를 낸다", func(t *testing.T) {
		// given
		client, _ := NewPodClient(filepath.Join(t.TempDir(), "missing"))

		// when
		_, err := client.FindMatchingPods(context.Background(), "app", "dev", map[string]string{"app": "web"})

		// then
		if err == nil || !strings.Contains(err.Error(), "load kubeconfig") {
			t.Fatalf("err = %v, want load error", err)
		}
	})

	t.Run("올바르지 않은 레이블 값은 셀렉터 생성 오류를 낸다", func(t *testing.T) {
		// given
		client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": "http://127.0.0.1:1"}))

		// when
		_, err := client.FindMatchingPods(context.Background(), "app", "dev", map[string]string{"app": "not valid!"})

		// then
		if err == nil || !strings.Contains(err.Error(), "create label selector") {
			t.Fatalf("err = %v, want label selector error", err)
		}
	})
}

func TestFindPodAndPortForService_RealPodClient(t *testing.T) {
	t.Run("API 서버의 준비된 파드와 이름 있는 대상 포트를 해석한다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{pods: map[string][]corev1.Pod{"app": {
			runningCorePod("app", "web-1", map[string]string{"app": "web"}, false),
			runningCorePod("app", "web-2", map[string]string{"app": "web"}, true),
		}}})
		client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		pod, port, err := FindPodAndPortForService(context.Background(), client, "app", "dev",
			map[string]string{"app": "web"}, &ServicePort{Port: 80, TargetPortName: "http"})

		// then
		if err != nil || pod.Name != "web-2" || port != 8080 {
			t.Fatalf("pod = %+v, port = %d, err = %v, want web-2 on 8080", pod, port, err)
		}
	})

	t.Run("파드 조회가 실패하면 파드 검색 오류로 감싼다", func(t *testing.T) {
		// given
		api := newFakeAPI(t, apiFixture{failures: map[string]int{"/api/v1/namespaces/app/pods": 500}})
		client, _ := NewPodClient(writeKubeconfig(t, "dev", map[string]string{"dev": api.URL()}))

		// when
		_, _, err := FindPodAndPortForService(context.Background(), client, "app", "dev",
			map[string]string{"app": "web"}, &ServicePort{Port: 80})

		// then
		if err == nil || !strings.Contains(err.Error(), "find matching pods") {
			t.Fatalf("err = %v, want find matching pods error", err)
		}
	})
}
