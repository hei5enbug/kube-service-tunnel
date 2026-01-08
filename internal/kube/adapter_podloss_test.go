package kube

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const apiWatchWait = 8 * time.Second

type apiLostEvent struct {
	key string
	err error
}

func awaitAPILost(t *testing.T, lost <-chan apiLostEvent) apiLostEvent {
	t.Helper()
	select {
	case event := <-lost:
		return event
	case <-time.After(apiWatchWait):
		t.Fatal("lost handler was not called")
		return apiLostEvent{}
	}
}

func waitForPodWatch(t *testing.T, api *fakeAPI, count int) {
	t.Helper()
	deadline := time.Now().Add(apiWatchWait)
	for api.watchCount() < count && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if api.watchCount() < count {
		t.Fatalf("pod watches = %d, want at least %d", api.watchCount(), count)
	}
}

func TestKubeAdapter_PodLoss(t *testing.T) {
	t.Run("StartForward한 파드가 삭제되면 핸들러가 호출되고 로컬 포트가 풀린다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		local := firstFreePortFrom(t, minLocalPort)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		waitForPodWatch(t, api, 1)

		// when
		api.publishPodEvent(watch.Deleted, runningCorePod("app", "web-1", nil, true))

		// then
		event := awaitAPILost(t, lost)
		if event.key != key || event.err == nil || event.err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 was deleted", event, key)
		}
		if !isLocalPortFree(local) {
			t.Fatalf("local port %d is still in use after the lost callback", local)
		}
	})

	t.Run("서비스 등록으로 연 포워딩도 파드가 종료 중이 되면 손실로 보고한다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		tunnel, err := adapter.RegisterServicePortForward(context.Background(), "dev",
			ServiceRef{Namespace: "app", Name: "web"}, map[int32]bool{})
		if err != nil {
			t.Fatalf("RegisterServicePortForward() error = %v", err)
		}
		waitForPodWatch(t, api, 1)
		terminating := runningCorePod("app", "web-1", nil, true)
		terminating.ResourceVersion = "101"
		now := metav1.Now()
		terminating.DeletionTimestamp = &now

		// when
		api.publishPodEvent(watch.Modified, terminating)

		// then
		event := awaitAPILost(t, lost)
		if event.key != tunnel.ForwardKey || event.err == nil || event.err.Error() != "pod app/web-1 is terminating" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 is terminating", event, tunnel.ForwardKey)
		}
	})

	t.Run("시작할 때 서버에 없는 파드는 포워딩을 열지 않고 오류를 돌려준다", func(t *testing.T) {
		// given
		adapter, _, _ := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		local := firstFreePortFrom(t, minLocalPort)

		// when
		key, err := adapter.StartForward(context.Background(), "dev", "app", "ghost-1", 8080, local)

		// then
		if err == nil || key != "" || !strings.Contains(err.Error(), "read pod app/ghost-1") {
			t.Fatalf("StartForward() = %q, %v, want a read pod error", key, err)
		}
		if !isLocalPortFree(local) {
			t.Fatalf("local port %d was opened although the pod could not be read", local)
		}
		select {
		case event := <-lost:
			t.Fatalf("unexpected lost callback: %+v", event)
		case <-time.After(100 * time.Millisecond):
		}
	})

	t.Run("감시를 다시 열 때 서버가 410을 답해도 파드를 다시 읽어 감시를 이어가고 이후 삭제를 손실로 보고한다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		local := firstFreePortFrom(t, minLocalPort)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		waitForPodWatch(t, api, 1)
		api.failNextWatch(http.StatusGone)
		api.closeWatches()
		waitForPodWatch(t, api, 3)

		// when
		api.publishPodEvent(watch.Deleted, runningCorePod("app", "web-1", nil, true))

		// then
		event := awaitAPILost(t, lost)
		if event.key != key || event.err == nil || event.err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("event = %+v, want key %q and pod app/web-1 was deleted", event, key)
		}
	})

	t.Run("API 서버가 감시를 끊어도 이어서 감시하고 손실로 보고하지 않는다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		local := firstFreePortFrom(t, minLocalPort)
		if _, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local); err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		waitForPodWatch(t, api, 1)

		// when
		api.closeWatches()
		waitForPodWatch(t, api, 2)

		// then
		select {
		case event := <-lost:
			t.Fatalf("unexpected lost callback: %+v", event)
		case <-time.After(100 * time.Millisecond):
		}
		assertEcho(t, local)
	})

	t.Run("해제한 포워딩은 파드가 삭제돼도 핸들러를 호출하지 않는다", func(t *testing.T) {
		// given
		adapter, _, api := newAPIAdapter(t)
		lost := make(chan apiLostEvent, 2)
		adapter.SetPortForwardLostHandler(func(key string, err error) { lost <- apiLostEvent{key, err} })
		local := firstFreePortFrom(t, minLocalPort)
		key, err := adapter.StartForward(context.Background(), "dev", "app", "web-1", 8080, local)
		if err != nil {
			t.Fatalf("StartForward() error = %v", err)
		}
		waitForPodWatch(t, api, 1)

		// when
		unregisterErr := adapter.UnregisterServicePortForward(key)
		api.publishPodEvent(watch.Deleted, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app", ResourceVersion: "101"}})

		// then
		if unregisterErr != nil {
			t.Fatalf("UnregisterServicePortForward() error = %v", unregisterErr)
		}
		select {
		case event := <-lost:
			t.Fatalf("unexpected lost callback after unregister: %+v", event)
		case <-time.After(200 * time.Millisecond):
		}
	})
}
