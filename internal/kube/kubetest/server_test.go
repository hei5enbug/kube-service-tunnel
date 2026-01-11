package kubetest

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

func TestServer_Lists(t *testing.T) {
	t.Parallel()
	web := ClusterIPService("app", "web", map[string]string{"app": "web"})

	t.Run("네임스페이스를 설정한 순서대로 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Namespaces: []string{"default", "kube-system", "app"}})

		// when
		list, err := newClientset(t, server).CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})

		// then
		var got []string
		for _, namespace := range list.Items {
			got = append(got, namespace.Name)
		}
		if err != nil || !reflect.DeepEqual(got, []string{"default", "kube-system", "app"}) {
			t.Fatalf("namespaces = %v, %v", got, err)
		}
	})

	t.Run("서비스는 요청한 네임스페이스의 것만 돌려주고 없는 네임스페이스는 빈 목록이다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Services: map[string][]corev1.Service{"app": {web}}})
		services := newClientset(t, server).CoreV1()

		// when
		inApp, appErr := services.Services("app").List(context.Background(), metav1.ListOptions{})
		inOther, otherErr := services.Services("other").List(context.Background(), metav1.ListOptions{})

		// then
		if appErr != nil || len(inApp.Items) != 1 || inApp.Items[0].Name != "web" {
			t.Fatalf("app services = %+v, %v", inApp.Items, appErr)
		}
		if otherErr != nil || len(inOther.Items) != 0 {
			t.Fatalf("other services = %+v, %v, want none", inOther.Items, otherErr)
		}
	})

	t.Run("파드는 레이블 셀렉터에 맞는 것만 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {
			RunningPod("app", "web-1", map[string]string{"app": "web"}, true),
			RunningPod("app", "db-1", map[string]string{"app": "db"}, true),
		}}})

		// when
		list, err := newClientset(t, server).CoreV1().Pods("app").List(context.Background(), metav1.ListOptions{LabelSelector: "app=web"})

		// then
		if err != nil || len(list.Items) != 1 || list.Items[0].Name != "web-1" {
			t.Fatalf("pods = %+v, %v, want only web-1", list.Items, err)
		}
	})

	t.Run("해석할 수 없는 레이블 셀렉터는 빈 목록을 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})

		// when
		response, err := http.Get(server.URL() + "/api/v1/namespaces/app/pods?labelSelector=%21%21%21")

		// then
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer response.Body.Close()
		var list corev1.PodList
		decodeErr := json.NewDecoder(response.Body).Decode(&list)
		if response.StatusCode != http.StatusOK || decodeErr != nil || list.Kind != "PodList" || len(list.Items) != 0 {
			t.Fatalf("status = %d, list = %+v, decode err = %v, want an empty pod list", response.StatusCode, list, decodeErr)
		}
	})

	t.Run("이름으로 파드 하나를 조회하고 없으면 404를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})
		pods := newClientset(t, server).CoreV1().Pods("app")

		// when
		found, foundErr := pods.Get(context.Background(), "web-1", metav1.GetOptions{})
		_, missingErr := pods.Get(context.Background(), "ghost", metav1.GetOptions{})

		// then
		if foundErr != nil || found.Name != "web-1" || found.UID != "uid-web-1" {
			t.Fatalf("pod = %+v, %v", found, foundErr)
		}
		if !apierrors.IsNotFound(missingErr) {
			t.Fatalf("missing err = %v, want NotFound", missingErr)
		}
	})
}

func TestServer_Config(t *testing.T) {
	t.Parallel()

	t.Run("서버는 설정을 복사해 가지므로 이후 설정을 바꿔도 응답이 달라지지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		pods := map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}
		server := NewServer(t, Config{Pods: pods})

		// when
		pods["app"][0].Name = "renamed"
		_, found := server.GetPod("app", "web-1")

		// then
		if !found {
			t.Fatal("pod web-1 disappeared after the caller changed its config")
		}
	})

	t.Run("알 수 없는 경로는 404를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})

		// when
		response, err := http.Get(server.URL() + "/healthz")

		// then
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", response.StatusCode)
		}
	})
}

func TestServer_Failures(t *testing.T) {
	t.Parallel()

	t.Run("설정한 경로는 정상 응답 대신 지정한 상태를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Namespaces: []string{"app"}, Failures: map[string]int{"/api/v1/namespaces": http.StatusInternalServerError}})

		// when
		_, err := newClientset(t, server).CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})

		// then
		if !apierrors.IsInternalError(err) {
			t.Fatalf("err = %v, want internal error", err)
		}
	})

	t.Run("FailPath로 실패를 걸었다가 RestorePath로 되돌린다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Namespaces: []string{"app"}})
		namespaces := newClientset(t, server).CoreV1().Namespaces()

		// when
		server.FailPath("/api/v1/namespaces", http.StatusForbidden)
		_, failing := namespaces.List(context.Background(), metav1.ListOptions{})
		server.RestorePath("/api/v1/namespaces")
		list, restored := namespaces.List(context.Background(), metav1.ListOptions{})

		// then
		if !apierrors.IsForbidden(failing) {
			t.Fatalf("failing err = %v, want forbidden", failing)
		}
		if restored != nil || len(list.Items) != 1 {
			t.Fatalf("restored list = %+v, %v, want one namespace", list.Items, restored)
		}
	})
}

func TestServer_Requests(t *testing.T) {
	t.Parallel()

	t.Run("받은 요청을 경로와 쿼리로 기록하고 접미사와 조각으로 거를 수 있다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Namespaces: []string{"app"}})
		pods := newClientset(t, server).CoreV1().Pods("app")

		// when
		_, _ = pods.List(context.Background(), metav1.ListOptions{LabelSelector: "app=web"})
		_, _ = pods.List(context.Background(), metav1.ListOptions{})
		_, _ = newClientset(t, server).CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})

		// then
		if got := server.Requests(); len(got) != 3 {
			t.Fatalf("requests = %v, want 3", got)
		}
		if got := server.RequestsTo("/pods"); len(got) != 2 {
			t.Fatalf("pod requests = %v, want 2", got)
		}
		if got := server.RequestsWith("/pods", "labelSelector=app%3Dweb"); len(got) != 1 {
			t.Fatalf("selector requests = %v, want 1", got)
		}
	})
}

func TestKubeconfig(t *testing.T) {
	t.Parallel()

	t.Run("컨텍스트마다 서버를 가리키고 현재 컨텍스트를 지정한다", func(t *testing.T) {
		t.Parallel()
		// given
		servers := map[string]string{"dev": "http://127.0.0.1:1", "prod": "http://127.0.0.1:2"}

		// when
		path := WriteKubeconfig(t, "prod", servers)

		// then
		config, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load kubeconfig: %v", err)
		}
		if config.CurrentContext != "prod" || len(config.Contexts) != 2 {
			t.Fatalf("config = current %q, %d contexts, want prod and 2", config.CurrentContext, len(config.Contexts))
		}
		if cluster := config.Clusters[config.Contexts["dev"].Cluster]; cluster.Server != servers["dev"] {
			t.Fatalf("dev server = %q, want %q", cluster.Server, servers["dev"])
		}
		if user := config.Contexts["prod"].AuthInfo; user != "user-prod" {
			t.Fatalf("prod user = %q, want user-prod", user)
		}
	})

	t.Run("서버의 Kubeconfig는 그 서버를 가리키는 단일 컨텍스트를 만든다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})

		// when
		path := server.Kubeconfig("dev")

		// then
		config, err := clientcmd.LoadFromFile(path)
		if err != nil {
			t.Fatalf("load kubeconfig: %v", err)
		}
		if config.CurrentContext != "dev" || config.Clusters["cluster-dev"].Server != server.URL() {
			t.Fatalf("config = current %q, server %q, want dev and %q", config.CurrentContext, config.Clusters["cluster-dev"].Server, server.URL())
		}
	})
}

func TestObjects(t *testing.T) {
	t.Parallel()

	t.Run("서비스에 포트를 주지 않으면 80에서 8080으로 가는 http 포트를 갖는다", func(t *testing.T) {
		t.Parallel()
		// given
		selector := map[string]string{"app": "web"}

		// when
		service := ClusterIPService("app", "web", selector)

		// then
		ports := service.Spec.Ports
		if len(ports) != 1 || ports[0].Name != "http" || ports[0].Port != 80 || ports[0].TargetPort.IntValue() != 8080 {
			t.Fatalf("ports = %+v, want http 80 to 8080", ports)
		}
		if service.Namespace != "app" || service.Spec.Type != corev1.ServiceTypeClusterIP || !reflect.DeepEqual(service.Spec.Selector, selector) {
			t.Fatalf("service = %+v", service)
		}
	})

	t.Run("서비스에 포트를 주면 그 포트만 쓴다", func(t *testing.T) {
		t.Parallel()
		// given
		custom := corev1.ServicePort{Name: "grpc", Port: 9000}

		// when
		service := ClusterIPService("app", "web", nil, custom)

		// then
		if len(service.Spec.Ports) != 1 || service.Spec.Ports[0].Name != "grpc" {
			t.Fatalf("ports = %+v, want only grpc", service.Spec.Ports)
		}
	})

	t.Run("파드의 준비 여부가 Ready 조건에 반영된다", func(t *testing.T) {
		t.Parallel()
		for _, ready := range []bool{true, false} {
			// given
			want := corev1.ConditionFalse
			if ready {
				want = corev1.ConditionTrue
			}

			// when
			pod := RunningPod("app", "web-1", nil, ready)

			// then
			if pod.Status.Phase != corev1.PodRunning || pod.Status.Conditions[0].Status != want || pod.UID != "uid-web-1" || pod.ResourceVersion != "100" {
				t.Fatalf("pod(ready=%v) = %+v", ready, pod.Status)
			}
		}
	})
}
