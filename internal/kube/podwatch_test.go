package kube

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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
	// changed is closed and replaced whenever watchers, opens or gets change, so tests wait on it.
	changed  chan struct{}
	watchers []*watch.RaceFreeFakeWatcher
	options  []metav1.ListOptions
	gets     int
	getErrs  []error
	// openErrs holds the results of the next watch opens in order; a nil entry lets that open succeed.
	openErrs []error
	// openTimes records when each watch open attempt happened, failed or not.
	openTimes []time.Time
}

func newWatchHarness(pods ...*corev1.Pod) *watchHarness {
	objects := make([]runtime.Object, 0, len(pods))
	for _, pod := range pods {
		objects = append(objects, pod)
	}
	h := &watchHarness{clientset: fake.NewClientset(objects...), changed: make(chan struct{})}
	h.clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		defer h.notifyLocked()
		h.openTimes = append(h.openTimes, time.Now())
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
		defer h.notifyLocked()
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

// notifyLocked wakes everything waiting in waitFor. Callers hold h.mu.
func (h *watchHarness) notifyLocked() {
	close(h.changed)
	h.changed = make(chan struct{})
}

// waitFor blocks until condition, evaluated with h.mu held, is true.
func (h *watchHarness) waitFor(t *testing.T, condition func() bool, what string) {
	t.Helper()
	timeout := time.NewTimer(testWait)
	defer timeout.Stop()
	for {
		h.mu.Lock()
		satisfied, changed := condition(), h.changed
		h.mu.Unlock()
		if satisfied {
			return
		}
		select {
		case <-changed:
		case <-timeout.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (h *watchHarness) watcherCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.watchers)
}

func (h *watchHarness) getCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gets
}

func (h *watchHarness) watcher(t *testing.T, index int) (*watch.RaceFreeFakeWatcher, metav1.ListOptions) {
	t.Helper()
	h.waitFor(t, func() bool { return len(h.watchers) > index }, "watch #"+strconv.Itoa(index))
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.watchers[index], h.options[index]
}

func (h *watchHarness) newMonitor(retryDelay time.Duration) *podWatch {
	return &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", retryDelay: retryDelay}
}

func (h *watchHarness) start(ctx context.Context) <-chan error {
	return h.startMonitor(ctx, h.newMonitor(fastRetry))
}

func (h *watchHarness) startMonitor(ctx context.Context, monitor *podWatch) <-chan error {
	result := make(chan error, 1)
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
		case <-time.After(testWait):
			t.Error("pod monitor did not stop after cancel")
		}
	})
	return result
}

func awaitLoss(t *testing.T, result <-chan error) error {
	t.Helper()
	return receive(t, result, "the pod monitor result")
}

// assertRunning fails the test when the monitor already ended. A monitor that lost the pod has returned
// before it could open another watch, so checking right after the next watch opened is deterministic.
func assertRunning(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("pod monitor ended unexpectedly: %v", err)
	default:
	}
}

func terminatingPod(resourceVersion string) *corev1.Pod {
	pod := watchedPod(resourceVersion)
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	return pod
}

func TestPodWatch_Loss(t *testing.T) {
	t.Parallel()
	t.Run("Deleted 이벤트가 오면 삭제됐다는 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
		t.Parallel()
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
			t.Parallel()
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
	t.Parallel()
	t.Run("파드 이름을 필드 셀렉터로 지정해 마지막으로 읽은 버전부터 감시한다", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
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
		assertRunning(t, result)
		second.Delete(watchedPod("102"))
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want pod app/web-1 was deleted after resume", err)
		}
	})

	t.Run("버전이 만료되면 파드를 다시 읽고 새 버전부터 감시한다", func(t *testing.T) {
		t.Parallel()
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
		assertRunning(t, result)
	})

	t.Run("버전이 만료된 사이 파드가 종료 중이 되었다면 다시 읽을 때 손실로 보고한다", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod("100"))
		result := h.start(context.Background())
		watcher, _ := h.watcher(t, 0)
		other := watchedPod("101")
		other.Name = "web-2"
		other.UID = "uid-2"

		// when
		watcher.Modify(watchedPod("101"))
		watcher.Modify(other)
		watcher.Action(watch.Bookmark, watchedPod("102"))
		watcher.Action(watch.Modified, &corev1.Service{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "103"}})
		watcher.Delete(watchedPod("104"))

		// then
		if err := awaitLoss(t, result); err == nil || err.Error() != "pod app/web-1 was deleted" {
			t.Fatalf("err = %v, want only the final deletion reported as pod app/web-1 was deleted", err)
		}
	})

	t.Run("API 조회가 일시적으로 실패해도 다시 시도해 감시를 이어간다", func(t *testing.T) {
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod("100"))
		h.getErrs = []error{errors.New("connection refused"), errors.New("connection refused")}
		result := h.start(context.Background())

		// when
		watcher, _ := h.watcher(t, 0)

		// then
		assertRunning(t, result)
		if h.getCount() != 3 {
			t.Fatalf("get calls = %d, want 3", h.getCount())
		}
		watcher.Delete(watchedPod("101"))
		if err := awaitLoss(t, result); err == nil {
			t.Fatal("err = nil, want deletion after recovery")
		}
	})

	t.Run("읽은 버전이 비어 있으면 감시를 열지 않고 다시 읽기를 반복하다 취소되면 끝난다", func(t *testing.T) {
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod(""))
		ctx, cancel := context.WithCancel(context.Background())
		result := h.start(ctx)
		h.waitFor(t, func() bool { return h.gets >= 3 }, "three pod reads")

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil || h.watcherCount() != 0 {
			t.Fatalf("err = %v, watchers = %d, want nil and 0", err, h.watcherCount())
		}
	})
}

func TestPodWatch_OpenFailure(t *testing.T) {
	t.Parallel()
	openErrors := map[string]error{
		"만료(410 Expired)": apierrors.NewResourceExpired("too old resource version"),
		"사라짐(410 Gone)":   apierrors.NewGone("gone"), //nolint:staticcheck // the API server can still answer 410 Gone
		"그 밖의 연결 오류":      errors.New("connection refused"),
		"권한 없음(403)":      apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "web-1", errors.New("no")),
	}
	for name, openErr := range openErrors {
		t.Run(name+" 오류로 감시를 다시 열지 못하면 파드를 다시 읽고 새 버전부터 감시해 이후 삭제를 손실로 보고한다", func(t *testing.T) {
			t.Parallel()
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
		t.Parallel()
		// given
		const retryDelay = 30 * time.Millisecond
		const attempts = 4
		h := newWatchHarness(watchedPod("100"))
		h.openErrs = []error{errors.New("connection refused")}
		ctx, cancel := context.WithCancel(context.Background())
		result := h.startMonitor(ctx, h.newMonitor(retryDelay))

		// when
		h.waitFor(t, func() bool { return len(h.openTimes) >= attempts }, "repeated watch open attempts")
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		for i := 1; i < attempts; i++ {
			if gap := h.openTimes[i].Sub(h.openTimes[i-1]); gap < retryDelay {
				t.Fatalf("open attempts #%d and #%d are %s apart, want at least the %s retry delay", i-1, i, gap, retryDelay)
			}
		}
	})
}

func TestPodWatch_ExpectedUID(t *testing.T) {
	t.Parallel()
	t.Run("첫 조회 전에 같은 이름의 파드로 교체됐다면 교체됐다는 오류를 돌려주고 감시를 열지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod("100"))
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", expectedUID: "uid-0", retryDelay: fastRetry}

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
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", expectedUID: "uid-1", retryDelay: fastRetry}
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
	t.Parallel()
	t.Run("감시 중에 컨텍스트를 취소하면 손실 없이 끝나고 감시를 닫는다", func(t *testing.T) {
		t.Parallel()
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
		eventually(t, watcher.IsStopped, "the watch to be stopped after cancel")
	})

	t.Run("조회를 재시도하는 중에 컨텍스트를 취소하면 손실 없이 끝난다", func(t *testing.T) {
		t.Parallel()
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
		t.Parallel()
		// given
		h := newWatchHarness(watchedPod("100"))
		ctx, cancel := context.WithCancel(context.Background())
		monitor := &podWatch{pods: h.clientset.CoreV1().Pods("app"), namespace: "app", name: "web-1", retryDelay: time.Hour}
		result := make(chan error, 1)
		go func() { result <- monitor.run(ctx) }()
		first, _ := h.watcher(t, 0)
		first.Error(&metav1.Status{Status: metav1.StatusFailure, Code: http.StatusGone})
		eventually(t, first.IsStopped, "the expired watch to be stopped")

		// when
		cancel()

		// then
		if err := awaitLoss(t, result); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if h.watcherCount() != 1 {
			t.Fatalf("watchers = %d, want no watch after the expiry", h.watcherCount())
		}
	})
}

func TestWatchPodLoss(t *testing.T) {
	t.Parallel()
	t.Run("실제 클라이언트셋 경로로 삭제를 감지해 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
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
