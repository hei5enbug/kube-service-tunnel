package kube

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const watchRestartWait = 5 * time.Second

func watchedPod(resourceVersion string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-1", Namespace: "app", UID: types.UID("uid-1"), ResourceVersion: resourceVersion},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

// watchHarness serves pods from a fake clientset and hands every started watch to the test.
type watchHarness struct {
	clientset *fake.Clientset
	mu        sync.Mutex
	watchers  []*watch.RaceFreeFakeWatcher
	options   []metav1.ListOptions
	gets      int
	getErrs   []error
	// openErrs holds the results of the next watch opens in order; a nil entry lets that open succeed.
	openErrs []error
	// opens counts every watch open attempt, failed or not.
	opens int
}

func newWatchHarness(pods ...*corev1.Pod) *watchHarness {
	objects := make([]runtime.Object, 0, len(pods))
	for _, pod := range pods {
		objects = append(objects, pod)
	}
	h := &watchHarness{clientset: fake.NewClientset(objects...)}
	h.clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.opens++
		if len(h.openErrs) > 0 {
			err := h.openErrs[0]
			if len(h.openErrs) > 1 {
				h.openErrs = h.openErrs[1:]
			}
			if err != nil {
				return true, nil, err
			}
		}
		watcher := watch.NewRaceFreeFake()
		h.watchers = append(h.watchers, watcher)
		h.options = append(h.options, action.(k8stesting.WatchActionImpl).ListOptions)
		return true, watcher, nil
	})
	h.clientset.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.gets++
		if len(h.getErrs) == 0 {
			return false, nil, nil
		}
		err := h.getErrs[0]
		h.getErrs = h.getErrs[1:]
		return true, nil, err
	})
	return h
}

func (h *watchHarness) watcherCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.watchers)
}

func (h *watchHarness) openCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.opens
}

func (h *watchHarness) getCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gets
}

func (h *watchHarness) watcher(t *testing.T, index int) (*watch.RaceFreeFakeWatcher, metav1.ListOptions) {
	t.Helper()
	deadline := time.Now().Add(watchRestartWait)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		if len(h.watchers) > index {
			defer h.mu.Unlock()
			return h.watchers[index], h.options[index]
		}
		h.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("watch #%d was not started", index)
	return nil, metav1.ListOptions{}
}

func (h *watchHarness) start(ctx context.Context) <-chan error {
	result := make(chan error, 1)
	monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", retryDelay: 10 * time.Millisecond}
	go func() { result <- monitor.run(ctx) }()
	return result
}

// startUntilCleanup starts the monitor for a test that expects no result, and cancels it and waits for it to
// finish when the test ends.
func (h *watchHarness) startUntilCleanup(t *testing.T) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	result := h.start(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(watchRestartWait):
			t.Error("pod monitor did not stop after cancel")
		}
	})
	return result
}

func awaitLoss(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(watchRestartWait):
		t.Fatal("pod monitor did not report a result")
		return nil
	}
}

func assertNoResult(t *testing.T, result <-chan error, wait time.Duration) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("pod monitor ended unexpectedly: %v", err)
	case <-time.After(wait):
	}
}

func terminatingPod(resourceVersion string) *corev1.Pod {
	pod := watchedPod(resourceVersion)
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	return pod
}

func TestPodWatch_Loss(t *testing.T) {
	t.Run("Deleted 이벤트가 오면 삭제됐다는 오류를 돌려준다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		watcher, _ := h.watcher(t, 0)

		// when
		watcher.Delete(watchedPod("101"))

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want pod app/web-1 was deleted", err)
		}
	})

	t.Run("시작할 때 파드가 없으면 삭제됐다는 오류를 돌려주고 감시를 열지 않는다", func(t *testing.T) {
		// given
		h := newWatchHarness()

		// when
		result := h.start(context.Background())

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want pod app/web-1 was deleted", err)
		}
		if h.watcherCount() != 0 {
			t.Fatalf("watchers = %d, want 0", h.watcherCount())
		}
	})

	t.Run("시작할 때 이미 종료 중인 파드는 종료 중이라는 오류를 돌려준다", func(t *testing.T) {
		// given
		h := newWatchHarness(terminatingPod("100"))

		// when
		result := h.start(context.Background())

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 is terminating" {
			t.Fatalf("err = %v, want pod app/web-1 is terminating", err)
		}
	})

	t.Run("삭제 시각이 생기면 종료 중이라는 오류를 돌려준다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		watcher, _ := h.watcher(t, 0)

		// when
		watcher.Modify(terminatingPod("101"))

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 is terminating" {
			t.Fatalf("err = %v, want pod app/web-1 is terminating", err)
		}
	})

	t.Run("UID가 달라지면 교체됐다는 오류를 돌려준다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		watcher, _ := h.watcher(t, 0)
		replacement := watchedPod("101")
		replacement.UID = "uid-2"

		// when
		watcher.Add(replacement)

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was replaced by a new pod" {
			t.Fatalf("err = %v, want pod app/web-1 was replaced by a new pod", err)
		}
	})

	for _, phase := range []corev1.PodPhase{corev1.PodFailed, corev1.PodSucceeded} {
		t.Run("단계가 "+string(phase)+"가 되면 그 단계를 알리는 오류를 돌려준다", func(t *testing.T) {
			// given
			h := newWatchHarness(watchedPod("100"))
			result := h.start(context.Background())
			watcher, _ := h.watcher(t, 0)
			finished := watchedPod("101")
			finished.Status.Phase = phase

			// when
			watcher.Modify(finished)

			// then
			want := "pod app/web-1 is " + string(phase)
			if err := awaitLoss(t, result); err == nil || err.Error() != want {
				t.Fatalf("err = %v, want %s", err, want)
			}
		})
	}
}

func TestPodWatch_Watching(t *testing.T) {
	t.Run("파드 이름을 필드 셀렉터로 지정해 마지막으로 읽은 버전부터 감시한다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// when
		h.start(ctx)
		_, options := h.watcher(t, 0)

		// then
		if options.FieldSelector != "metadata.name=web-1" || options.ResourceVersion != "100" {
			t.Fatalf("options = %+v, want selector metadata.name=web-1 from resource version 100", options)
		}
	})

	t.Run("감시 채널이 닫혀도 마지막 버전부터 다시 감시하고 손실로 보고하지 않는다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		first, _ := h.watcher(t, 0)
		first.Modify(watchedPod("101"))

		// when
		first.Stop()
		second, options := h.watcher(t, 1)

		// then
		if options.ResourceVersion != "101" {
			t.Fatalf("resume resource version = %q, want 101", options.ResourceVersion)
		}
		assertNoResult(t, result, 100*time.Millisecond)
		second.Delete(watchedPod("102"))
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want pod app/web-1 was deleted after resume", err)
		}
	})

	t.Run("버전이 만료되면 파드를 다시 읽고 새 버전부터 감시한다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.startUntilCleanup(t)
		first, _ := h.watcher(t, 0)
		updated := watchedPod("200")
		if err := h.clientset.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), updated, "app"); err != nil {
			t.Fatalf("update pod: %v", err)
		}

		// when
		first.Error(&apierrors.NewResourceExpired("too old resource version").ErrStatus)
		_, options := h.watcher(t, 1)

		// then
		if options.ResourceVersion != "200" {
			t.Fatalf("resource version after expiry = %q, want 200", options.ResourceVersion)
		}
		assertNoResult(t, result, 50*time.Millisecond)
	})

	t.Run("버전이 만료된 사이 파드가 종료 중이 되었다면 다시 읽을 때 손실로 보고한다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		first, _ := h.watcher(t, 0)
		if err := h.clientset.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), terminatingPod("200"), "app"); err != nil {
			t.Fatalf("update pod: %v", err)
		}

		// when
		first.Error(&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone, Reason: metav1.StatusReasonExpired})

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 is terminating" {
			t.Fatalf("err = %v, want pod app/web-1 is terminating", err)
		}
	})

	t.Run("정상 이벤트와 다른 이름의 파드와 파드가 아닌 객체는 무시한다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.startUntilCleanup(t)
		watcher, _ := h.watcher(t, 0)
		other := watchedPod("101")
		other.Name = "web-2"
		other.UID = "uid-2"

		// when
		watcher.Modify(watchedPod("101"))
		watcher.Modify(other)
		watcher.Action(watch.Bookmark, watchedPod("102"))
		watcher.Action(watch.Modified, &corev1.Service{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "103"}})

		// then
		assertNoResult(t, result, 100*time.Millisecond)
	})

	t.Run("API 조회가 일시적으로 실패해도 다시 시도해 감시를 이어간다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		h.getErrs = []error{errors.New("connection refused"), errors.New("connection refused")}
		result := h.start(context.Background())

		// when
		watcher, _ := h.watcher(t, 0)

		// then
		assertNoResult(t, result, 50*time.Millisecond)
		if h.getCount() != 3 {
			t.Fatalf("get calls = %d, want 3", h.getCount())
		}
		watcher.Delete(watchedPod("101"))
		if err := awaitLoss(t, result); err == nil {
			t.Fatal("err = nil, want deletion after recovery")
		}
	})

	t.Run("읽은 버전이 비어 있으면 감시를 열지 않고 다시 읽기를 반복하다 취소되면 끝난다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod(""))
		ctx, cancel := context.WithCancel(context.Background())
		result := h.start(ctx)
		deadline := time.Now().Add(watchRestartWait)
		for h.getCount() < 3 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil || h.watcherCount() != 0 {
			t.Fatalf("err = %v, watchers = %d, want nil and 0", err, h.watcherCount())
		}
	})
}

func TestPodWatch_OpenFailure(t *testing.T) {
	openErrors := map[string]error{
		"만료(410 Expired)": apierrors.NewResourceExpired("too old resource version"),
		"사라짐(410 Gone)":   apierrors.NewGone("gone"),
		"그 밖의 연결 오류":      errors.New("connection refused"),
		"권한 없음(403)":      apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "web-1", errors.New("no")),
	}
	for name, openErr := range openErrors {
		t.Run(name+" 오류로 감시를 다시 열지 못하면 파드를 다시 읽고 새 버전부터 감시해 이후 삭제를 손실로 보고한다", func(t *testing.T) {
			// given
			h := newWatchHarness(watchedPod("100"))
			h.openErrs = []error{nil, openErr, nil}
			result := h.start(context.Background())
			first, _ := h.watcher(t, 0)
			if err := h.clientset.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), watchedPod("200"), "app"); err != nil {
				t.Fatalf("update pod: %v", err)
			}

			// when
			first.Stop()
			second, options := h.watcher(t, 1)
			second.Delete(watchedPod("201"))

			// then
			if options.ResourceVersion != "200" {
				t.Fatalf("resource version after failed open = %q, want 200", options.ResourceVersion)
			}
			if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
				t.Fatalf("err = %v, want pod app/web-1 was deleted", err)
			}
		})
	}

	t.Run("감시를 계속 열지 못해도 다시 읽기 대기 시간보다 빠르게 반복하지 않는다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		h.openErrs = []error{errors.New("connection refused")}
		ctx, cancel := context.WithCancel(context.Background())
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", retryDelay: 100 * time.Millisecond}
		result := make(chan error, 1)
		go func() { result <- monitor.run(ctx) }()

		// when
		time.Sleep(550 * time.Millisecond)
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if opens := h.openCount(); opens < 2 || opens > 7 {
			t.Fatalf("watch open attempts = %d, want between 2 and 7 for a 100ms delay over 550ms", opens)
		}
	})
}

func TestPodWatch_ExpectedUID(t *testing.T) {
	t.Run("첫 조회 전에 같은 이름의 파드로 교체됐다면 교체됐다는 오류를 돌려주고 감시를 열지 않는다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", expectedUID: "uid-0", retryDelay: 10 * time.Millisecond}

		// when
		err := monitor.run(context.Background())

		// then
		if err == nil || err.Error() != "pod app/web-1 was replaced by a new pod" {
			t.Fatalf("err = %v, want pod app/web-1 was replaced by a new pod", err)
		}
		if h.watcherCount() != 0 {
			t.Fatalf("watchers = %d, want 0", h.watcherCount())
		}
	})

	t.Run("기대한 UID와 같은 파드는 정상으로 보고 감시를 이어간다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", expectedUID: "uid-1", retryDelay: 10 * time.Millisecond}
		result := make(chan error, 1)
		go func() { result <- monitor.run(ctx) }()

		// when
		h.watcher(t, 0)
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

func TestPodWatch_Cancellation(t *testing.T) {
	t.Run("감시 중에 컨텍스트를 취소하면 손실 없이 끝나고 감시를 닫는다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		result := h.start(ctx)
		watcher, _ := h.watcher(t, 0)

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		deadline := time.Now().Add(watchRestartWait)
		for !watcher.IsStopped() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !watcher.IsStopped() {
			t.Fatal("watch was not stopped after cancel")
		}
	})

	t.Run("조회를 재시도하는 중에 컨텍스트를 취소하면 손실 없이 끝난다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		h.getErrs = []error{errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down"), errors.New("down")}
		ctx, cancel := context.WithCancel(context.Background())
		result := h.start(ctx)

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("다시 읽기 전 대기 중에 컨텍스트를 취소하면 손실 없이 끝난다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", retryDelay: time.Hour}
		result := make(chan error, 1)
		go func() { result <- monitor.run(ctx) }()
		first, _ := h.watcher(t, 0)
		first.Error(&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone})
		time.Sleep(50 * time.Millisecond)

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
}

func TestWatchPodLoss(t *testing.T) {
	t.Run("실제 클라이언트셋 경로로 삭제를 감지해 오류를 돌려준다", func(t *testing.T) {
		// given
		h := newWatchHarness(watchedPod("100"))
		result := make(chan error, 1)
		go func() { result <- watchPodLoss(context.Background(), h.clientset, "app", "web-1", "") }()
		watcher, _ := h.watcher(t, 0)

		// when
		watcher.Delete(watchedPod("101"))

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want pod app/web-1 was deleted", err)
		}
	})
}
