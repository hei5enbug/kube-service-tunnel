package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var selectorApp = map[string]string{"app": "web"}

func TestFindPodAndPortForService_PortResolution(t *testing.T) {
	pod := readyPod("web-1", PodPort{Name: "metrics", ContainerPort: 9090}, PodPort{Name: "web", ContainerPort: 8081})

	t.Run("이름이 있는 targetPort는 컨테이너 포트 이름으로 해석한다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pod}}
		httpPort := &ServicePort{Port: 80, TargetPortName: "web"}

		// when
		_, port, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if err != nil || port != 8081 {
			t.Fatalf("port = %d, err = %v, want 8081 and nil", port, err)
		}
	})

	t.Run("컨테이너에 같은 이름의 포트가 없으면 첫 포트로 대체하지 않고 포트와 파드 이름을 담아 오류를 낸다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pod}}
		httpPort := &ServicePort{Port: 80, TargetPortName: "http-missing"}

		// when
		_, port, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if err == nil || port != 0 {
			t.Fatalf("port = %d, err = %v, want error and port 0", port, err)
		}
		if !strings.Contains(err.Error(), "http-missing") || !strings.Contains(err.Error(), "web-1") {
			t.Fatalf("err = %q, want it to name the port and the pod", err)
		}
	})

	t.Run("정수 targetPort는 그대로 사용한다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pod}}
		httpPort := &ServicePort{Port: 80, TargetPort: 7000, TargetPortName: "web"}

		// when
		_, port, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if err != nil || port != 7000 {
			t.Fatalf("port = %d, err = %v, want 7000 and nil", port, err)
		}
	})

	t.Run("targetPort가 없으면 서비스 포트를 사용한다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pod}}
		httpPort := &ServicePort{Port: 8080}

		// when
		_, port, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if err != nil || port != 8080 {
			t.Fatalf("port = %d, err = %v, want 8080 and nil", port, err)
		}
	})
}

func TestFindPodAndPortForService_PodChoice(t *testing.T) {
	httpPort := &ServicePort{Port: 80}
	pending := Pod{Name: "pending", Status: "Pending", Ready: true}
	unready := Pod{Name: "unready", Status: "Running", Ready: false}
	terminating := Pod{Name: "terminating", Status: "Running", Ready: true, Terminating: true}
	healthy := Pod{Name: "healthy", Status: "Running", Ready: true}

	t.Run("Pending 준비 안 됨 종료 중인 파드는 건너뛰고 정상 파드를 고른다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pending, unready, terminating, healthy}}

		// when
		got, _, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if err != nil || got.Name != "healthy" {
			t.Fatalf("pod = %q, err = %v, want healthy and nil", got.Name, err)
		}
	})

	t.Run("준비된 파드가 하나도 없으면 오류를 낸다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{pending, unready, terminating}}

		// when
		_, _, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if !errors.Is(err, errNoReadyPods) || err.Error() != "no ready pods match service" {
			t.Fatalf("err = %v, want no ready pods match service", err)
		}
	})

	t.Run("일치하는 파드가 없어도 준비된 파드가 없다는 오류를 낸다", func(t *testing.T) {
		// given
		client := &fakePodClient{}

		// when
		_, _, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", selectorApp, httpPort)

		// then
		if !errors.Is(err, errNoReadyPods) {
			t.Fatalf("err = %v, want errNoReadyPods", err)
		}
	})
}

func TestFindPodAndPortForService_Selector(t *testing.T) {
	t.Run("셀렉터가 없는 서비스는 파드를 조회하지 않고 오류를 낸다", func(t *testing.T) {
		// given
		client := &fakePodClient{pods: []Pod{readyPod("any")}}

		// when
		_, _, err := FindPodAndPortForService(context.Background(), client, "app", "ctx", nil, &ServicePort{Port: 80})

		// then
		if !errors.Is(err, errServiceHasNoSelector) || client.calls != 0 {
			t.Fatalf("err = %v, calls = %d, want selector error and 0 calls", err, client.calls)
		}
	})

	t.Run("실제 파드 클라이언트도 빈 셀렉터를 거부한다", func(t *testing.T) {
		// given
		client := &podClient{kubeconfigPath: "/nonexistent/kubeconfig"}

		// when
		_, err := client.FindMatchingPods(context.Background(), "app", "ctx", map[string]string{})

		// then
		if !errors.Is(err, errServiceHasNoSelector) {
			t.Fatalf("err = %v, want errServiceHasNoSelector", err)
		}
	})
}

func TestPodFromCorev1(t *testing.T) {
	now := metav1.NewTime(time.Now())
	readyCondition := corev1.PodCondition{Type: corev1.PodReady, Status: corev1.ConditionTrue}

	t.Run("Ready 조건이 True이고 삭제 시각이 없으면 준비된 파드로 변환한다", func(t *testing.T) {
		// given
		in := corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{readyCondition}}}

		// when
		got := podFromCorev1(in)

		// then
		if !got.Ready || got.Terminating || got.Status != "Running" {
			t.Fatalf("pod = %+v, want ready running non-terminating", got)
		}
	})

	t.Run("삭제 시각이 있으면 종료 중인 파드로 변환한다", func(t *testing.T) {
		// given
		in := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{readyCondition}},
		}

		// when
		got := podFromCorev1(in)

		// then
		if !got.Terminating {
			t.Fatalf("pod = %+v, want terminating", got)
		}
	})

	t.Run("Ready 조건이 없으면 준비되지 않은 파드로 변환한다", func(t *testing.T) {
		// given
		in := corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}

		// when
		got := podFromCorev1(in)

		// then
		if got.Ready {
			t.Fatalf("pod = %+v, want not ready", got)
		}
	})

	t.Run("모든 컨테이너의 포트를 이름과 함께 담는다", func(t *testing.T) {
		// given
		in := corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Ports: []corev1.ContainerPort{{Name: "web", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}},
			{Ports: []corev1.ContainerPort{{Name: "metrics", ContainerPort: 9090}}},
		}}}

		// when
		got := podFromCorev1(in)

		// then
		if len(got.Ports) != 2 || got.Ports[0].Name != "web" || got.Ports[1].ContainerPort != 9090 {
			t.Fatalf("ports = %+v, want web:8080 and metrics:9090", got.Ports)
		}
	})
}
