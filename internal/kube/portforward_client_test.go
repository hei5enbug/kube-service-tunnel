package kube

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewPortForwardClient(t *testing.T) {
	t.Parallel()
	t.Run("처음에는 포워딩이 없어 없는 키를 중지하면 ErrPortForwardNotFound를 낸다", func(t *testing.T) {
		t.Parallel()
		// given
		client := NewPortForwardClient()

		// when
		err := client.StopPortForward("missing")
		client.StopAllPortForwards()

		// then
		if !errors.Is(err, ErrPortForwardNotFound) {
			t.Fatalf("err = %v, want ErrPortForwardNotFound", err)
		}
	})
}

func TestStartPortForward_DuplicateKey(t *testing.T) {
	t.Parallel()
	t.Run("같은 키가 이미 있으면 새 포워딩을 시작하지 않고 기존 항목을 유지한다", func(t *testing.T) {
		t.Parallel()
		// given
		var started atomic.Int32
		runner := func(request forwardRequest, stopCh <-chan struct{}, readyCh chan struct{}) error {
			started.Add(1)
			close(readyCh)
			<-stopCh
			return nil
		}
		client := newPortForwardClient(runner, testWait)
		t.Cleanup(client.StopAllPortForwards)
		startTestForward(t, client, 40000)

		// when
		key, err := client.StartPortForward(context.Background(), "ctx", "app", "web-1", 40000, 8080, nil, nil)

		// then
		if err == nil || key != "" || !strings.Contains(err.Error(), "already exists") || client.forwardCount() != 1 || started.Load() != 1 {
			t.Fatalf("key = %q, err = %v, count = %d, started = %d, want duplicate error keeping one forward", key, err, client.forwardCount(), started.Load())
		}
	})
}

func TestWatchForward_WithoutHandler(t *testing.T) {
	t.Parallel()
	t.Run("핸들러가 없어도 예기치 않게 끝난 포워딩의 항목을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		exit := make(chan struct{})
		client := newPortForwardClient(blockingReadyRunner(exit, errors.New("connection reset")), testWait)
		startTestForward(t, client, 40000)

		// when
		close(exit)

		// then
		eventually(t, func() bool { return client.forwardCount() == 0 }, "forward entry to be removed")
	})
}
