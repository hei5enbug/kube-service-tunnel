package kube

import (
	"context"
	"errors"
	"testing"
	"time"
)

func (p *portForwardClient) forwardCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.forwards)
}

func blockingReadyRunner(exit <-chan struct{}, exitErr error) forwardRunner {
	return func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
		close(readyCh)
		select {
		case <-stopCh:
			return nil
		case <-exit:
			return exitErr
		}
	}
}

func startTestForward(t *testing.T, client *portForwardClient, local int32) string {
	t.Helper()
	key, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", local, 8080, nil, nil)
	if err != nil {
		t.Fatalf("StartPortForward() error = %v", err)
	}
	return key
}

func TestBuildPortForwardKey(t *testing.T) {
	t.Parallel()
	t.Run("같은 파드와 원격 포트라도 로컬 포트가 다르면 다른 키를 만든다", func(t *testing.T) {
		t.Parallel()
		// given
		first := BuildPortForwardKey("ctx", "app", "web-1", 8080, 40000)

		// when
		second := BuildPortForwardKey("ctx", "app", "web-1", 8080, 40001)

		// then
		if first == second {
			t.Fatalf("keys are equal: %q", first)
		}
	})
}

func TestWaitForForwardReady(t *testing.T) {
	t.Parallel()
	t.Run("준비 신호가 오면 성공을 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		readyCh := make(chan struct{})
		close(readyCh)

		// when
		err := waitForForwardReady(context.Background(), readyCh, make(chan error, 1), make(chan struct{}), time.Second)

		// then
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})

	t.Run("준비 전에 포워딩이 오류로 끝나면 그 오류를 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		dialErr := errors.New("dial failed")
		errCh := make(chan error, 1)
		doneCh := make(chan struct{})
		errCh <- dialErr
		close(doneCh)

		// when
		err := waitForForwardReady(context.Background(), make(chan struct{}), errCh, doneCh, time.Second)

		// then
		if !errors.Is(err, dialErr) {
			t.Fatalf("err = %v, want %v", err, dialErr)
		}
	})

	t.Run("준비 전에 포워딩이 오류 없이 끝나면 준비 전에 종료됐다고 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		errCh := make(chan error, 1)
		doneCh := make(chan struct{})
		errCh <- nil
		close(doneCh)

		// when
		err := waitForForwardReady(context.Background(), make(chan struct{}), errCh, doneCh, time.Second)

		// then
		if !errors.Is(err, errForwardExitedEarly) {
			t.Fatalf("err = %v, want errForwardExitedEarly", err)
		}
	})

	t.Run("결과 없이 종료 신호만 오면 준비 전에 종료됐다고 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		doneCh := make(chan struct{})
		close(doneCh)

		// when
		err := waitForForwardReady(context.Background(), make(chan struct{}), make(chan error, 1), doneCh, time.Second)

		// then
		if !errors.Is(err, errForwardExitedEarly) {
			t.Fatalf("err = %v, want errForwardExitedEarly", err)
		}
	})

	t.Run("제한 시간 안에 준비되지 않으면 시간 초과를 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		timeout := 10 * time.Millisecond

		// when
		err := waitForForwardReady(context.Background(), make(chan struct{}), make(chan error, 1), make(chan struct{}), timeout)

		// then
		if !errors.Is(err, errForwardNotReady) {
			t.Fatalf("err = %v, want errForwardNotReady", err)
		}
	})

	t.Run("컨텍스트가 취소되면 제한 시간을 기다리지 않고 바로 취소 오류를 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		start := time.Now()

		// when
		err := waitForForwardReady(ctx, make(chan struct{}), make(chan error, 1), make(chan struct{}), time.Minute)

		// then
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > testWait {
			t.Fatalf("returned after %s, want prompt return", elapsed)
		}
	})
}

func TestStartPortForward(t *testing.T) {
	t.Parallel()
	t.Run("포워딩이 곧바로 실패하면 성공으로 보고하지 않고 항목도 남기지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		dialErr := errors.New("dial failed")
		client := newPortForwardClient(func(forwardRequest, <-chan struct{}, chan struct{}) error { return dialErr }, testWait)

		// when
		key, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		if !errors.Is(err, dialErr) || key != "" || client.forwardCount() != 0 {
			t.Fatalf("key = %q, err = %v, count = %d, want failure with no entry", key, err, client.forwardCount())
		}
	})

	t.Run("오류 없이 곧바로 끝나는 포워딩도 실패로 보고한다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(func(forwardRequest, <-chan struct{}, chan struct{}) error { return nil }, testWait)

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		if !errors.Is(err, errForwardExitedEarly) {
			t.Fatalf("err = %v, want errForwardExitedEarly", err)
		}
	})

	t.Run("준비되면 키를 돌려주고 항목을 유지한다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(blockingReadyRunner(make(chan struct{}), nil), testWait)
		defer client.StopAllPortForwards()

		// when
		key, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		want := BuildPortForwardKey("ctx", "app", "web-1", 8080, 40000)
		if err != nil || key != want || client.forwardCount() != 1 {
			t.Fatalf("key = %q, err = %v, count = %d, want %q, nil, 1", key, err, client.forwardCount(), want)
		}
	})

	t.Run("시간 초과가 나면 중지 채널을 닫고 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		stopped := make(chan struct{})
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			<-stopCh
			close(stopped)
			return nil
		}
		client := newPortForwardClient(runner, 20*time.Millisecond)

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		if !errors.Is(err, errForwardNotReady) || client.forwardCount() != 0 {
			t.Fatalf("err = %v, count = %d, want timeout with no entry", err, client.forwardCount())
		}
		select {
		case <-stopped:
		case <-time.After(testWait):
			t.Fatal("stop channel was not closed after timeout")
		}
	})

	t.Run("컨텍스트가 취소되면 중지 채널을 닫고 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			<-stopCh
			close(stopped)
			return nil
		}
		client := newPortForwardClient(runner, time.Minute)
		cancel()

		// when
		_, err := client.StartPortForward(ctx, "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		if !errors.Is(err, context.Canceled) || client.forwardCount() != 0 {
			t.Fatalf("err = %v, count = %d, want cancellation with no entry", err, client.forwardCount())
		}
		select {
		case <-stopped:
		case <-time.After(testWait):
			t.Fatal("stop channel was not closed after cancellation")
		}
	})

	t.Run("같은 파드와 원격 포트를 서로 다른 로컬 포트로 동시에 등록할 수 있다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(blockingReadyRunner(make(chan struct{}), nil), testWait)
		defer client.StopAllPortForwards()
		startTestForward(t, client, 40000)

		// when
		_, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40001, 8080, nil, nil)

		// then
		if err != nil || client.forwardCount() != 2 {
			t.Fatalf("err = %v, count = %d, want nil and 2", err, client.forwardCount())
		}
	})
}

func TestStopPortForward(t *testing.T) {
	t.Parallel()
	t.Run("없는 키를 중지하면 ErrPortForwardNotFound를 감싼 오류를 낸다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(nil, testWait)

		// when
		err := client.StopPortForward("missing")

		// then
		if !errors.Is(err, ErrPortForwardNotFound) {
			t.Fatalf("err = %v, want ErrPortForwardNotFound", err)
		}
	})

	t.Run("중지하면 실행 중이던 포워딩이 종료되고 항목이 지워진다", func(t *testing.T) {
		t.Parallel()
		// given
		exited := make(chan struct{})
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			close(readyCh)
			<-stopCh
			close(exited)
			return nil
		}
		client := newPortForwardClient(runner, testWait)
		key := startTestForward(t, client, 40000)

		// when
		err := client.StopPortForward(key)

		// then
		if err != nil || client.forwardCount() != 0 {
			t.Fatalf("err = %v, count = %d, want nil and 0", err, client.forwardCount())
		}
		select {
		case <-exited:
		case <-time.After(testWait):
			t.Fatal("forward did not exit after stop")
		}
	})

	t.Run("전체 중지를 여러 번 호출해도 패닉 없이 모든 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		client := newPortForwardClient(blockingReadyRunner(make(chan struct{}), nil), testWait)
		startTestForward(t, client, 40000)
		startTestForward(t, client, 40001)
		client.StopAllPortForwards()

		// when
		client.StopAllPortForwards()

		// then
		if client.forwardCount() != 0 {
			t.Fatalf("count = %d, want 0", client.forwardCount())
		}
	})
}

type lostEvent struct {
	key string
	err error
}

func TestLostHandler(t *testing.T) {
	t.Parallel()
	t.Run("준비된 뒤 예기치 않게 끝나면 키와 오류로 핸들러를 한 번 호출하고 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		exit := make(chan struct{})
		exitErr := errors.New("connection reset")
		client := newPortForwardClient(blockingReadyRunner(exit, exitErr), testWait)
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })
		key := startTestForward(t, client, 40000)

		// when
		close(exit)

		// then
		select {
		case event := <-events:
			if event.key != key || !errors.Is(event.err, exitErr) {
				t.Fatalf("event = %+v, want key %q and %v", event, key, exitErr)
			}
		case <-time.After(testWait):
			t.Fatal("lost handler was not called")
		}
		if client.forwardCount() != 0 {
			t.Fatalf("count = %d, want 0", client.forwardCount())
		}
		awaitBackground(t, client)
		expectNone(t, events, "lost callback twice")
	})

	t.Run("오류 없이 끝나도 파드 연결 끊김 오류로 핸들러를 호출한다", func(t *testing.T) {
		t.Parallel()
		// given
		exit := make(chan struct{})
		client := newPortForwardClient(blockingReadyRunner(exit, nil), testWait)
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })
		startTestForward(t, client, 40000)

		// when
		close(exit)

		// then
		select {
		case event := <-events:
			if event.err == nil || event.err.Error() != "lost connection to pod" {
				t.Fatalf("err = %v, want lost connection to pod", event.err)
			}
		case <-time.After(testWait):
			t.Fatal("lost handler was not called")
		}
	})

	t.Run("StopPortForward로 중지한 포워딩은 핸들러를 호출하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		exited := make(chan struct{})
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			close(readyCh)
			<-stopCh
			defer close(exited)
			return nil
		}
		client := newPortForwardClient(runner, testWait)
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })
		key := startTestForward(t, client, 40000)

		// when
		err := client.StopPortForward(key)

		// then
		if err != nil {
			t.Fatalf("StopPortForward() error = %v", err)
		}
		closed(t, exited, "forward to exit")
		awaitBackground(t, client)
		expectNone(t, events, "lost callback after stop")
	})

	t.Run("StopAllPortForwards로 중지한 포워딩도 핸들러를 호출하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		exited := make(chan struct{})
		client := newPortForwardClient(monitoredForwardRunner(exited), testWait)
		events := make(chan lostEvent, 4)
		client.SetLostHandler(func(key string, err error) { events <- lostEvent{key, err} })
		startTestForward(t, client, 40000)

		// when
		client.StopAllPortForwards()

		// then
		closed(t, exited, "forward to exit")
		awaitBackground(t, client)
		expectNone(t, events, "lost callback after stop all")
	})
}
