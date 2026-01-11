package kube

import (
	"context"
	"errors"
	"testing"
)

func TestCurrentContext(t *testing.T) {
	t.Parallel()
	t.Run("컨텍스트 클라이언트가 알려준 현재 컨텍스트 이름을 그대로 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := &kubeAdapter{contextClient: &fakeContextClient{current: "dev"}}

		// when
		got, err := adapter.CurrentContext(context.Background())

		// then
		if err != nil || got != "dev" {
			t.Fatalf("CurrentContext() = %q, %v, want dev and nil", got, err)
		}
	})

	t.Run("컨텍스트 클라이언트의 오류를 그대로 전달한다", func(t *testing.T) {
		t.Parallel()
		// given
		cause := errors.New("bad kubeconfig")
		adapter := &kubeAdapter{contextClient: &fakeContextClient{currentErr: cause}}

		// when
		_, err := adapter.CurrentContext(context.Background())

		// then
		if !errors.Is(err, cause) {
			t.Fatalf("err = %v, want %v", err, cause)
		}
	})
}
