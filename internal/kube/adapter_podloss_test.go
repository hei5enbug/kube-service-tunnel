package kube

import (
	"context"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type apiLostEvent struct {
	key string
	err error
}

// lostEvents registers a lost handler on the adapter and returns the channel it reports to.
func lostEvents(adapter KubeAdapterInterface) <-chan apiLostEvent {
	lost := make(chan apiLostEvent, 2)
	adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
	return lost
}

func TestKubeAdapter_PodLoss(t *testing.T) {
	t.Parallel()

	t.Run("StartForward한 파드가 삭제되면 핸들러가 호출되고 로컬 포트가 풀린다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, server := newAPIAdapter(t)
		lost := lostEvents(adapter)
		local := freeLocalPort(t)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		server.WaitForWatches(t, 1)

		// when
		server.DeletePod("app", "web-1")

		// then
		event := receive(t, lost, "lost handler")
		if event.key != key || event.err == nil || event.err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 was deleted", event, key)
		}
		if !isLocalPortFree(local) {
			t.Fatalf("local port %d is still in use after the lost callback", local)
		}
	})

	t.Run("서비스 등록으로 연 포워딩도 파드가 종료 중이 되면 손실로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, server := newAPIAdapter(t)
		lost := lostEvents(adapter)
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "dev",
			ServiceRef{Namespace: "app", Name: "web", LocalPort: freeLocalPort(t)}, map[int32]bool{})
		if err != nil {
			t.Fatalf("RegisterServicePortForward() error = %v", err)
		}
		server.WaitForWatches(t, 1)

		// when
		server.UpdatePod("app", "web-1", func(pod *corev1.Pod) {
			now := metav1.Now()
			pod.DeletionTimestamp = &now
		})

		// then
		event := receive(t, lost, "lost handler")
		if event.key != tunnel.ForwardKey || event.err == nil || event.err.Error() != "pod app/web-1 is terminating" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 is terminating", event, tunnel.ForwardKey)
		}
	})

	t.Run("시작할 때 서버에 없는 파드는 포워딩을 열지 않고 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, _ := newAPIAdapter(t)
		lost := lostEvents(adapter)
		local := freeLocalPort(t)

		// when
		key, err := adapter.StartForward(context.Background(), "dev", "app", "ghost-1", 8080, local)

		// then
		if err == nil || key != "" || !strings.Contains(err.Error(), "read pod app/ghost-1") {
			t.Fatalf("StartForward() = %q, %v, want a read pod error", key, err)
		}
		if !isLocalPortFree(local) {
			t.Fatalf("local port %d was opened although the pod could not be read", local)
		}
		expectNone(t, lost, "lost callback") // StartForward failed before it started any goroutine
	})

	t.Run("감시를 다시 열 때 서버가 410을 답해도 파드를 다시 읽어 감시를 이어가고 이후 삭제를 손실로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, server := newAPIAdapter(t)
		lost := lostEvents(adapter)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, freeLocalPort(t))
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		server.WaitForWatches(t, 1)
		server.FailNextWatch(http.StatusGone)
		server.CloseWatches()
		server.WaitForWatches(t, 3)

		// when
		server.DeletePod("app", "web-1")

		// then
		event := receive(t, lost, "lost handler")
		if event.key != key || event.err == nil || event.err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 was deleted", event, key)
		}
	})

	t.Run("API 서버가 감시를 끊어도 이어서 감시하고 손실로 보고하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, server := newAPIAdapter(t)
		lost := lostEvents(adapter)
		local := freeLocalPort(t)
		if _, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local); err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		server.WaitForWatches(t, 1)

		// when
		server.CloseWatches()
		server.WaitForWatches(t, 2)

		// then
		expectNoneWithin(t, lost, "lost callback") // the monitor keeps watching by design, so it never completes
		assertEcho(t, local)
	})

	t.Run("해제한 포워딩은 파드가 삭제돼도 핸들러를 호출하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter, server := newAPIAdapter(t)
		lost := lostEvents(adapter)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, freeLocalPort(t))
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		server.WaitForWatches(t, 1)

		// when
		unregisterErr := adapter.UnregisterServicePortForward(key)
		server.DeletePod("app", "web-1")

		// then
		if unregisterErr != nil {
			t.Fatalf("UnregisterServicePortForward() error = %v", unregisterErr)
		}
		server.WaitForOpenWatches(t, 0)
		awaitBackground(t, adapter.(*kubeAdapter).portForwardClient.(*portForwardClient))
		expectNone(t, lost, "lost callback after unregister")
	})
}
