package kube

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

// monitoredForwardRunner marks readiness, then blocks until stopped, recording when it exited.
func monitoredForwardRunner(exited chan<- struct{}) forwardRunner {
	return func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
		close(readyCh)
		<-stopCh
		close(exited)
		return nil
	}
}

func startMonitoredForward(t *testing.T, client *portForwardClient) string {
	t.Helper()
	key, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, fake.NewClientset())
	if err != nil {
		t.Fatalf("StartPortForward() error = %v", err)
	}
	return key
}

func TestPodMonitor_Lost(t *testing.T) {
	t.Parallel()
	t.Run("파드 손실을 감지하면 핸들러를 키와 오류로 한 번 호출하고 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		exited := make(chan struct{})
		client := newPortForwardClient(monitoredForwardRunner(exited), testWait)
		lostPod := errors.New("pod app/web-1 was deleted")
		trigger := make(chan struct{})
		var gotNamespace, gotPod string
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, namespace, pod string, _ types.UID) error {
			gotNamespace, gotPod = namespace, pod
			select {
			case <-trigger:
				return lostPod
			case <-ctx.Done():
				return nil
			}
		}
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) {
			select {
			case <-exited:
			default:
				t.Error("handler called before the forward released its port")
			}
			events <- lostEvent{key, err}
		})
		key := startMonitoredForward(t, client)

		// when
		close(trigger)

		// then
		select {
		case event := <-events:
			if event.key != key || !errors.Is(event.err, lostPod) {
				t.Fatalf("event = %+v, want key %q and %v", event, key, lostPod)
			}
		case <-time.After(testWait):
			t.Fatal("lost handler was not called")
		}
		if client.forwardCount() != 0 || gotNamespace != "app" || gotPod != "web-1" {
			t.Fatalf("count = %d, monitored %s/%s, want 0 and app/web-1", client.forwardCount(), gotNamespace, gotPod)
		}
		awaitBackground(t, client)
		expectNone(t, events, "second lost callback")
	})

	t.Run("포워딩이 멈추지 않아도 중지 대기 시간이 지나면 핸들러를 호출한다", func(t *testing.T) {
		t.Parallel()
		// given
		release := make(chan struct{})
		defer close(release)
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			close(readyCh)
			<-release
			return nil
		}
		client := newPortForwardClient(runner, testWait)
		client.stopTimeout = fastRetry
		client.detectLoss = func(context.Context, kubernetes.Interface, string, string, types.UID) error {
			return errors.New("pod app/web-1 was deleted")
		}
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })

		// when
		startMonitoredForward(t, client)

		// then
		select {
		case <-events:
		case <-time.After(testWait):
			t.Fatal("lost handler was not called")
		}
	})

	t.Run("핸들러가 없어도 손실된 포워딩의 항목을 지우고 중지한다", func(t *testing.T) {
		t.Parallel()
		// given
		exited := make(chan struct{})
		client := newPortForwardClient(monitoredForwardRunner(exited), testWait)
		client.detectLoss = func(context.Context, kubernetes.Interface, string, string, types.UID) error {
			return errors.New("pod app/web-1 was deleted")
		}

		// when
		startMonitoredForward(t, client)

		// then
		select {
		case <-exited:
		case <-time.After(testWait):
			t.Fatal("forward was not stopped")
		}
		eventually(t, func() bool { return client.forwardCount() == 0 }, "forward entry to be removed")
	})

	t.Run("시작할 때 전달받은 파드 UID를 감시에 그대로 넘긴다", func(t *testing.T) {
		t.Parallel()
		// given
		gotUID := make(chan types.UID, 1)
		client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, expected types.UID) error {
			gotUID <- expected
			<-ctx.Done()
			return nil
		}
		t.Cleanup(client.StopAllPortForwards)

		// when
		_, err := client.StartPortForward(withExpectedPodUID(context.Background(), "uid-9"), "ctx", "app", "web-1", 40000, 8080, nil, fake.NewClientset())

		// then
		if err != nil {
			t.Fatalf("StartPortForward() error = %v", err)
		}
		select {
		case uid := <-gotUID:
			if uid != "uid-9" {
				t.Fatalf("expected uid = %q, want uid-9", uid)
			}
		case <-time.After(testWait):
			t.Fatal("pod was not monitored")
		}
	})

	t.Run("클라이언트셋이 없으면 파드를 감시하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		monitored := make(chan struct{}, 1)
		client := newPortForwardClient(blockingReadyRunner(make(chan struct{}), nil), testWait)
		client.detectLoss = func(context.Context, kubernetes.Interface, string, string, types.UID) error {
			monitored <- struct{}{}
			return nil
		}
		t.Cleanup(client.StopAllPortForwards)

		// when
		startTestForward(t, client, 40000)
		client.StopAllPortForwards()
		awaitBackground(t, client)

		// then
		expectNone(t, monitored, "pod monitoring without a clientset")
	})
}

func TestPodMonitor_Stop(t *testing.T) {
	t.Parallel()
	stopCases := map[string]func(client *portForwardClient, key string){
		"StopPortForward":     func(client *portForwardClient, key string) { _ = client.StopPortForward(key) },
		"StopAllPortForwards": func(client *portForwardClient, key string) { client.StopAllPortForwards() },
	}
	for name, stop := range stopCases {
		t.Run(name+"로 중지하면 감시가 끝나고 핸들러는 호출되지 않는다", func(t *testing.T) {
			t.Parallel()
			// given
			forwardExited := make(chan struct{})
			monitorExited := make(chan struct{})
			client := newPortForwardClient(monitoredForwardRunner(forwardExited), testWait)
			client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
				<-ctx.Done()
				close(monitorExited)
				return nil
			}
			events := make(chan lostEvent, 4)
			client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })
			key := startMonitoredForward(t, client)

			// when
			stop(client, key)

			// then
			closed(t, monitorExited, "pod monitor to exit after stop")
			closed(t, forwardExited, "forward to exit after stop")
			awaitBackground(t, client)
			expectNone(t, events, "lost callback after stop")
		})
	}

	t.Run("포워딩이 스스로 끝나면 감시도 끝나고 핸들러는 한 번만 호출된다", func(t *testing.T) {
		t.Parallel()
		// given
		exit := make(chan struct{})
		monitorExited := make(chan struct{})
		client := newPortForwardClient(blockingReadyRunner(exit, errors.New("connection reset")), testWait)
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
			<-ctx.Done()
			close(monitorExited)
			return nil
		}
		var calls atomic.Int32
		client.SetLostHandler(func(string, error) { calls.Add(1) })
		startMonitoredForward(t, client)

		// when
		close(exit)

		// then
		closed(t, monitorExited, "pod monitor to exit after the forward ended")
		eventually(t, func() bool { return calls.Load() == 1 }, "one lost callback")
	})

	t.Run("시작에 실패한 포워딩은 감시를 시작하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		var monitored atomic.Bool
		client := newPortForwardClient(func(forwardRequest, <-chan struct{}, chan struct{}) error { return errors.New("dial failed") }, testWait)
		client.detectLoss = func(context.Context, kubernetes.Interface, string, string, types.UID) error {
			monitored.Store(true)
			return nil
		}

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, fake.NewClientset())

		// then
		if err == nil || monitored.Load() {
			t.Fatalf("err = %v, monitored = %v, want failure without monitoring", err, monitored.Load())
		}
	})

	t.Run("같은 키가 이미 있어 시작이 거부돼도 기존 감시는 유지된다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
			<-ctx.Done()
			return nil
		}
		t.Cleanup(client.StopAllPortForwards)
		key := startMonitoredForward(t, client)

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, fake.NewClientset())

		// then
		if err == nil {
			t.Fatal("err = nil, want a duplicate key error")
		}
		client.mu.Lock()
		monitorErr := client.forwards[key].monitorCtx.Err()
		client.mu.Unlock()
		if monitorErr != nil {
			t.Fatalf("existing monitor context error = %v, want it still running", monitorErr)
		}
	})
}

func TestPodMonitor_ConcurrentStopAndLoss(t *testing.T) {
	t.Parallel()
	const iterations = 100

	t.Run("중지와 파드 손실이 동시에 일어나도 핸들러는 최대 한 번만 호출된다", func(t *testing.T) {
		t.Parallel()
		// given
		calls := make([]*atomic.Int32, iterations)
		clients := make([]*portForwardClient, iterations)
		triggers := make([]chan struct{}, iterations)
		keys := make([]string, iterations)
		for i := range calls {
			triggers[i] = make(chan struct{})
			trigger := triggers[i]
			calls[i] = new(atomic.Int32)
			counter := calls[i]
			clients[i] = newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
			clients[i].detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
				select {
				case <-trigger:
					return errors.New("pod app/web-1 was deleted")
				case <-ctx.Done():
					return nil
				}
			}
			clients[i].SetLostHandler(func(string, error) { counter.Add(1) })
			keys[i] = startMonitoredForward(t, clients[i])
		}

		// when
		var wg sync.WaitGroup
		for i := range clients {
			wg.Add(2)
			go func() { defer wg.Done(); close(triggers[i]) }()
			go func() { defer wg.Done(); _ = clients[i].StopPortForward(keys[i]) }()
		}
		wg.Wait()
		for _, client := range clients {
			eventually(t, func() bool { return client.forwardCount() == 0 }, "forward entry to be removed")
			awaitBackground(t, client)
		}

		// then
		for i, counter := range calls {
			if got := counter.Load(); got > 1 {
				t.Fatalf("iteration %d: handler calls = %d, want at most 1", i, got)
			}
		}
	})

	t.Run("포워딩 종료와 파드 손실이 동시에 일어나도 핸들러는 정확히 한 번 호출된다", func(t *testing.T) {
		t.Parallel()
		// given
		calls := make([]*atomic.Int32, iterations)
		clients := make([]*portForwardClient, iterations)
		gates := make([]chan struct{}, iterations)
		for i := range calls {
			gates[i] = make(chan struct{})
			gate := gates[i]
			calls[i] = new(atomic.Int32)
			client := newPortForwardClient(func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
				close(readyCh)
				select {
				case <-gate:
					return errors.New("connection reset")
				case <-stopCh:
					return nil
				}
			}, testWait)
			client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
				select {
				case <-gate:
					return errors.New("pod app/web-1 was deleted")
				case <-ctx.Done():
					return nil
				}
			}
			counter := calls[i]
			client.SetLostHandler(func(string, error) { counter.Add(1) })
			startMonitoredForward(t, client)
			clients[i] = client
		}

		// when
		for _, gate := range gates {
			close(gate)
		}
		for i, counter := range calls {
			eventually(t, func() bool { return counter.Load() >= 1 }, "lost callback of iteration "+strconv.Itoa(i))
		}

		// then
		for _, client := range clients {
			awaitBackground(t, client)
		}
		for i, counter := range calls {
			if got := counter.Load(); got != 1 {
				t.Fatalf("iteration %d: handler calls = %d, want 1", i, got)
			}
		}
	})
}
