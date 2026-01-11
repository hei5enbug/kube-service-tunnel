package kubetest

import (
	"context"
	"net/http"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

func openWatch(t *testing.T, server *Server, namespace, selector string) watch.Interface {
	t.Helper()
	watcher, err := newClientset(t, server).CoreV1().Pods(namespace).Watch(context.Background(), metav1.ListOptions{FieldSelector: selector})
	if err != nil {
		t.Fatalf("open watch: %v", err)
	}
	t.Cleanup(watcher.Stop)
	return watcher
}

func TestServer_PodMutations(t *testing.T) {
	t.Parallel()

	t.Run("AddPod는 비어 있는 UID와 버전을 채워 저장하고 Added 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		watcher := openWatch(t, server, "app", "metadata.name=web-1")
		server.WaitForWatches(t, 1)

		// when
		stored := server.AddPod(corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app"}})

		// then
		event := nextEvent(t, watcher)
		got, _ := event.Object.(*corev1.Pod)
		if event.Type != watch.Added || got == nil || got.UID != stored.UID || got.ResourceVersion != stored.ResourceVersion {
			t.Fatalf("event = %v %+v, want Added for %+v", event.Type, event.Object, stored)
		}
		if stored.UID == "" || stored.ResourceVersion == "" {
			t.Fatalf("stored = %+v, want a generated UID and resourceVersion", stored.ObjectMeta)
		}
		if again, found := server.GetPod("app", "web-1"); !found || again.UID != stored.UID {
			t.Fatalf("GetPod() = %+v, %v, want the stored pod", again.ObjectMeta, found)
		}
	})

	t.Run("AddPod는 지정한 UID와 버전을 그대로 둔다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})

		// when
		stored := server.AddPod(RunningPod("app", "web-1", nil, true))

		// then
		if stored.UID != "uid-web-1" || stored.ResourceVersion != "100" {
			t.Fatalf("stored = %+v, want uid-web-1 at 100", stored.ObjectMeta)
		}
	})

	t.Run("이미 있는 파드를 AddPod하면 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})

		// when
		runToCompletion(func() { server.AddPod(RunningPod("app", "web-1", nil, true)) })

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})

	t.Run("UpdatePod는 UID를 유지하고 버전을 올려 Modified 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})
		watcher := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		updated := server.UpdatePod("app", "web-1", func(pod *corev1.Pod) { pod.Status.Phase = corev1.PodFailed })

		// then
		event := nextEvent(t, watcher)
		got, _ := event.Object.(*corev1.Pod)
		if event.Type != watch.Modified || got == nil || got.Status.Phase != corev1.PodFailed || got.UID != "uid-web-1" {
			t.Fatalf("event = %v %+v, want Modified keeping uid-web-1 in phase Failed", event.Type, event.Object)
		}
		if updated.ResourceVersion == "100" || got.ResourceVersion != updated.ResourceVersion {
			t.Fatalf("resourceVersion = %q (event %q), want a bumped version", updated.ResourceVersion, got.ResourceVersion)
		}
	})

	t.Run("없는 파드를 UpdatePod하면 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{})

		// when
		runToCompletion(func() { server.UpdatePod("app", "ghost", func(*corev1.Pod) {}) })

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})

	t.Run("ReplacePod는 새 UID와 올라간 버전으로 바꿔 Modified 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {RunningPod("app", "web-1", nil, true)}}})
		watcher := openWatch(t, server, "app", "metadata.name=web-1")
		server.WaitForWatches(t, 1)

		// when
		replaced := server.ReplacePod(RunningPod("app", "web-1", nil, true))

		// then
		event := nextEvent(t, watcher)
		got, _ := event.Object.(*corev1.Pod)
		if event.Type != watch.Modified || got == nil || got.UID != replaced.UID {
			t.Fatalf("event = %v %+v, want Modified carrying %q", event.Type, event.Object, replaced.UID)
		}
		if replaced.UID == "uid-web-1" || replaced.ResourceVersion == "100" {
			t.Fatalf("replaced = %+v, want a new UID and a bumped version", replaced.ObjectMeta)
		}
		if stored, _ := server.GetPod("app", "web-1"); stored.UID != replaced.UID {
			t.Fatalf("stored UID = %q, want %q", stored.UID, replaced.UID)
		}
	})

	t.Run("ReplacePod는 없는 파드이면 새로 추가하고 Added 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		watcher := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		replaced := server.ReplacePod(RunningPod("app", "web-1", nil, true))

		// then
		event := nextEvent(t, watcher)
		if _, found := server.GetPod("app", "web-1"); event.Type != watch.Added || !found || replaced.UID == "" {
			t.Fatalf("event = %v, found = %v, replaced = %+v, want Added and a stored pod", event.Type, found, replaced.ObjectMeta)
		}
	})

	t.Run("DeletePod는 파드를 지우고 올라간 버전의 Deleted 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{Pods: map[string][]corev1.Pod{"app": {
			RunningPod("app", "web-1", nil, true),
			RunningPod("app", "web-2", nil, true),
		}}})
		watcher := openWatch(t, server, "app", "metadata.name=web-1")
		server.WaitForWatches(t, 1)

		// when
		deleted := server.DeletePod("app", "web-1")

		// then
		event := nextEvent(t, watcher)
		got, _ := event.Object.(*corev1.Pod)
		if event.Type != watch.Deleted || got == nil || got.Name != "web-1" || got.ResourceVersion != deleted.ResourceVersion || deleted.ResourceVersion == "100" {
			t.Fatalf("event = %v %+v, want Deleted web-1 at a bumped version (%q)", event.Type, event.Object, deleted.ResourceVersion)
		}
		_, gone := server.GetPod("app", "web-1")
		_, kept := server.GetPod("app", "web-2")
		if gone || !kept {
			t.Fatalf("web-1 found = %v, web-2 found = %v, want false and true", gone, kept)
		}
		_, getErr := newClientset(t, server).CoreV1().Pods("app").Get(context.Background(), "web-1", metav1.GetOptions{})
		if !apierrors.IsNotFound(getErr) {
			t.Fatalf("GET after delete err = %v, want NotFound", getErr)
		}
	})

	t.Run("없는 파드를 DeletePod하면 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{})

		// when
		runToCompletion(func() { server.DeletePod("app", "ghost") })

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})
}

func TestServer_Watch(t *testing.T) {
	t.Parallel()

	t.Run("이름 셀렉터가 있는 감시는 그 파드의 이벤트만 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		watcher := openWatch(t, server, "app", "metadata.name=web-1")
		server.WaitForWatches(t, 1)

		// when
		server.AddPod(RunningPod("app", "web-2", nil, true))
		server.AddPod(RunningPod("other", "web-1", nil, true))
		server.AddPod(RunningPod("app", "web-1", nil, true))

		// then
		event := nextEvent(t, watcher)
		got, _ := event.Object.(*corev1.Pod)
		if got == nil || got.Name != "web-1" || got.Namespace != "app" {
			t.Fatalf("first event = %v %+v, want app/web-1", event.Type, event.Object)
		}
	})

	t.Run("셀렉터가 없는 감시는 네임스페이스의 모든 파드 이벤트를 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		watcher := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		server.AddPod(RunningPod("app", "web-1", nil, true))
		server.AddPod(RunningPod("app", "web-2", nil, true))

		// then
		first, second := nextEvent(t, watcher), nextEvent(t, watcher)
		names := []string{first.Object.(*corev1.Pod).Name, second.Object.(*corev1.Pod).Name}
		if names[0] != "web-1" || names[1] != "web-2" {
			t.Fatalf("names = %v, want [web-1 web-2]", names)
		}
	})

	t.Run("PublishPodEvent는 저장된 파드를 바꾸지 않고 지정한 이벤트를 보낸다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		watcher := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		server.PublishPodEvent(watch.Bookmark, RunningPod("app", "web-1", nil, true))

		// then
		event := nextEvent(t, watcher)
		if _, found := server.GetPod("app", "web-1"); event.Type != watch.Bookmark || found {
			t.Fatalf("event = %v, stored = %v, want Bookmark without a stored pod", event.Type, found)
		}
	})

	t.Run("CloseWatches는 열린 감시를 끝내고 이후 감시는 다시 열 수 있다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		first := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		server.CloseWatches()

		// then
		select {
		case _, open := <-first.ResultChan():
			if open {
				t.Fatal("watch delivered an event instead of ending")
			}
		case <-time.After(eventTimeout):
			t.Fatal("watch did not end after CloseWatches")
		}
		second := openWatch(t, server, "app", "")
		server.WaitForWatches(t, 2)
		server.AddPod(RunningPod("app", "web-1", nil, true))
		if event := nextEvent(t, second); event.Type != watch.Added {
			t.Fatalf("event = %v, want Added on the reopened watch", event.Type)
		}
	})

	t.Run("FailNextWatch는 다음 감시 요청만 지정한 상태로 거절하고 요청 수에 센다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		pods := newClientset(t, server).CoreV1().Pods("app")
		server.FailNextWatch(http.StatusGone)

		// when
		_, failed := pods.Watch(context.Background(), metav1.ListOptions{})
		watcher, succeeded := pods.Watch(context.Background(), metav1.ListOptions{})

		// then
		if !apierrors.IsGone(failed) {
			t.Fatalf("first watch err = %v, want Gone", failed)
		}
		if succeeded != nil {
			t.Fatalf("second watch err = %v, want nil", succeeded)
		}
		watcher.Stop()
		if server.WatchCount() != 2 {
			t.Fatalf("WatchCount() = %d, want 2", server.WatchCount())
		}
	})

	t.Run("서버가 닫히는 중에 온 감시 요청은 503으로 거절한다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		server.mu.Lock()
		server.closed = true
		server.mu.Unlock()

		// when
		_, err := newClientset(t, server).CoreV1().Pods("app").Watch(context.Background(), metav1.ListOptions{})

		// then
		if !apierrors.IsServiceUnavailable(err) {
			t.Fatalf("err = %v, want service unavailable", err)
		}
	})

	t.Run("읽지 않는 감시의 버퍼가 가득 차면 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{})
		stream := &watchStream{namespace: "app", events: make(chan watch.Event, 1)}
		server.mu.Lock()
		server.watches[stream] = struct{}{}
		server.mu.Unlock()

		// when
		server.PublishPodEvent(watch.Added, RunningPod("app", "web-1", nil, true))
		server.PublishPodEvent(watch.Added, RunningPod("app", "web-2", nil, true))

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})

	t.Run("WaitForWatches는 감시가 열리지 않으면 제한 시간 뒤 테스트를 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		recorder := &recordingTB{TB: t}
		server := NewServer(recorder, Config{})
		server.waitTimeout = 20 * time.Millisecond

		// when
		runToCompletion(func() { server.WaitForWatches(recorder, 1) })

		// then
		if len(recorder.recorded()) != 1 {
			t.Fatalf("failures = %v, want exactly one", recorder.recorded())
		}
	})
}

func TestServer_Shutdown(t *testing.T) {
	t.Parallel()

	t.Run("열린 감시가 있어도 정리 단계에서 멈추지 않고 서버를 닫는다", func(t *testing.T) {
		t.Parallel()
		// given
		server := NewServer(t, Config{})
		openWatch(t, server, "app", "")
		server.WaitForWatches(t, 1)

		// when
		finished := make(chan struct{})
		go func() {
			server.shutdown()
			close(finished)
		}()

		// then
		select {
		case <-finished:
		case <-time.After(eventTimeout):
			t.Fatal("shutdown did not finish with an open watch")
		}
	})
}
