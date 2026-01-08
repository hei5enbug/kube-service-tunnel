package kube

import (
	"context"
	"errors"
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
	t.Run("파드 손실을 감지하면 핸들러를 키와 오류로 한 번 호출하고 항목을 지운다", func(t *testing.T) {
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
		select {
		case event := <-events:
			t.Fatalf("handler called twice: %+v", event)
		case <-time.After(50 * time.Millisecond):
		}
	})

	t.Run("포워딩이 멈추지 않아도 중지 대기 시간이 지나면 핸들러를 호출한다", func(t *testing.T) {
		// given
		release := make(chan struct{})
		defer close(release)
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			close(readyCh)
			<-release
			return nil
		}
		client := newPortForwardClient(runner, testWait)
		client.stopTimeout = 20 * time.Millisecond
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
		waitUntil(t, func() bool { return client.forwardCount() == 0 }, "forward entry to be removed")
	})

	t.Run("시작할 때 전달받은 파드 UID를 감시에 그대로 넘긴다", func(t *testing.T) {
		// given
		gotUID := make(chan types.UID, 1)
		client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, expected types.UID) error {
			gotUID <- expected
			<-ctx.Done()
			return nil
		}
		defer client.StopAllPortForwards()

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
		// given
		var monitored atomic.Bool
		client := newPortForwardClient(blockingReadyRunner(make(chan struct{}), nil), testWait)
		client.detectLoss = func(context.Context, kubernetes.Interface, string, string, types.UID) error {
			monitored.Store(true)
			return nil
		}
		defer client.StopAllPortForwards()

		// when
		startTestForward(t, client, 40000)
		time.Sleep(20 * time.Millisecond)

		// then
		if monitored.Load() {
			t.Fatal("pod was monitored without a clientset")
		}
	})
}

func TestPodMonitor_Stop(t *testing.T) {
	stopCases := map[string]func(client *portForwardClient, key string){
		"StopPortForward":     func(client *portForwardClient, key string) { _ = client.StopPortForward(key) },
		"StopAllPortForwards": func(client *portForwardClient, key string) { client.StopAllPortForwards() },
	}
	for name, stop := range stopCases {
		t.Run(name+"로 중지하면 감시가 끝나고 핸들러는 호출되지 않는다", func(t *testing.T) {
			// given
			monitorExited := make(chan struct{})
			client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
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
			select {
			case <-monitorExited:
			case <-time.After(testWait):
				t.Fatal("pod monitor did not exit after stop")
			}
			select {
			case event := <-events:
				t.Fatalf("handler called after stop: %+v", event)
			case <-time.After(100 * time.Millisecond):
			}
		})
	}

	t.Run("포워딩이 스스로 끝나면 감시도 끝나고 핸들러는 한 번만 호출된다", func(t *testing.T) {
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
		select {
		case <-monitorExited:
		case <-time.After(testWait):
			t.Fatal("pod monitor did not exit after the forward ended")
		}
		waitUntil(t, func() bool { return calls.Load() == 1 }, "one lost callback")
	})

	t.Run("시작에 실패한 포워딩은 감시를 시작하지 않는다", func(t *testing.T) {
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
		// given
		var cancels atomic.Int32
		client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
		client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
			<-ctx.Done()
			cancels.Add(1)
			return nil
		}
		defer client.StopAllPortForwards()
		startMonitoredForward(t, client)

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, fake.NewClientset())

		// then
		time.Sleep(20 * time.Millisecond)
		if err == nil || cancels.Load() != 0 {
			t.Fatalf("err = %v, canceled monitors = %d, want duplicate error and 0", err, cancels.Load())
		}
	})
}

func TestPodMonitor_ConcurrentStopAndLoss(t *testing.T) {
	t.Run("중지와 파드 손실이 동시에 일어나도 핸들러는 최대 한 번만 호출된다", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			// given
			trigger := make(chan struct{})
			client := newPortForwardClient(monitoredForwardRunner(make(chan struct{})), testWait)
			client.detectLoss = func(ctx context.Context, _ kubernetes.Interface, _, _ string, _ types.UID) error {
				select {
				case <-trigger:
					return errors.New("pod app/web-1 was deleted")
				case <-ctx.Done():
					return nil
				}
			}
			var calls atomic.Int32
			client.SetLostHandler(func(string, error) { calls.Add(1) })
			key := startMonitoredForward(t, client)

			// when
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); close(trigger) }()
			go func() { defer wg.Done(); _ = client.StopPortForward(key) }()
			wg.Wait()
			waitUntil(t, func() bool { return client.forwardCount() == 0 }, "forward entry to be removed")
			time.Sleep(time.Millisecond)

			// then
			if got := calls.Load(); got > 1 {
				t.Fatalf("iteration %d: handler calls = %d, want at most 1", i, got)
			}
		}
	})

	t.Run("포워딩 종료와 파드 손실이 동시에 일어나도 핸들러는 정확히 한 번 호출된다", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			// given
			gate := make(chan struct{})
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
			var calls atomic.Int32
			client.SetLostHandler(func(string, error) { calls.Add(1) })
			startMonitoredForward(t, client)

			// when
			close(gate)
			waitUntil(t, func() bool { return calls.Load() >= 1 }, "lost callback")
			time.Sleep(time.Millisecond)

			// then
			if got := calls.Load(); got != 1 {
				t.Fatalf("iteration %d: handler calls = %d, want 1", i, got)
			}
		}
	})
}
