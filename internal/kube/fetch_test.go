package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newFetchAdapter() *kubeAdapter {
	return &kubeAdapter{
		contextClient: &fakeContextClient{contexts: []Context{{Name: "dev"}, {Name: "prod"}}},
		namespaceClient: &fakeNamespaceClient{
			namespacesByCtx:  map[string][]string{"dev": {"app", "bad", "kube-system"}},
			namespaceErrsCtx: map[string]error{"prod": errors.New("unreachable")},
		},
		serviceClient: &fakeServiceClient{
			byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}, "kube-system": {httpService("dns", "kube-system", selectorApp)}},
			errs:        map[string]error{"bad": errors.New("forbidden")},
		},
	}
}

func TestFetchAllResources(t *testing.T) {
	t.Parallel()
	t.Run("일부 컨텍스트와 네임스페이스가 실패해도 컨텍스트마다 부분 결과와 자기 오류를 담고 전체 오류는 없다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := newFetchAdapter()

		// when
		resources, err := adapter.FetchAllResources(context.Background())

		// then
		if err != nil {
			t.Fatalf("err = %v, want nil when contexts could be listed", err)
		}
		dev := resources["dev"]
		if len(dev.Namespaces["app"]) != 1 {
			t.Fatalf("dev = %+v, want app with one service", dev)
		}
		if _, ok := dev.Namespaces["kube-system"]; ok {
			t.Fatalf("dev = %+v, want system namespace skipped", dev)
		}
		if dev.Err == nil || !strings.Contains(dev.Err.Error(), "context dev namespace bad: forbidden") || strings.Contains(dev.Err.Error(), "prod") {
			t.Fatalf("dev.Err = %v, want only its own namespace error", dev.Err)
		}
		prod, ok := resources["prod"]
		if !ok || prod.Namespaces == nil || len(prod.Namespaces) != 0 {
			t.Fatalf("resources = %+v, want failed context prod with empty non-nil namespaces", resources)
		}
		if prod.Err == nil || !strings.Contains(prod.Err.Error(), "context prod: unreachable") {
			t.Fatalf("prod.Err = %v, want context error", prod.Err)
		}
	})

	t.Run("실패가 없으면 모든 컨텍스트의 오류가 비어 있다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := &kubeAdapter{
			contextClient:   &fakeContextClient{contexts: []Context{{Name: "dev"}}},
			namespaceClient: &fakeNamespaceClient{namespaces: []string{"app"}},
			serviceClient:   &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}},
		}

		// when
		resources, err := adapter.FetchAllResources(context.Background())

		// then
		if err != nil || resources["dev"].Err != nil || len(resources["dev"].Namespaces["app"]) != 1 {
			t.Fatalf("resources = %+v, err = %v, want one service and nil errors", resources, err)
		}
	})

	t.Run("컨텍스트 목록을 읽지 못하면 오류만 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := &kubeAdapter{contextClient: &fakeContextClient{err: errors.New("bad kubeconfig")}}

		// when
		resources, err := adapter.FetchAllResources(context.Background())

		// then
		if err == nil || resources != nil {
			t.Fatalf("resources = %+v, err = %v, want nil and error", resources, err)
		}
	})
}

func TestFetchContextResources(t *testing.T) {
	t.Parallel()
	t.Run("한 컨텍스트만 새로 조회하고 같은 규칙으로 부분 결과와 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := newFetchAdapter()

		// when
		got := adapter.FetchContextResources(context.Background(), "dev")

		// then
		if len(got.Namespaces["app"]) != 1 || len(got.Namespaces) != 1 {
			t.Fatalf("got = %+v, want only app with one service", got)
		}
		if got.Err == nil || !strings.Contains(got.Err.Error(), "context dev namespace bad: forbidden") {
			t.Fatalf("Err = %v, want namespace error", got.Err)
		}
	})

	t.Run("네임스페이스 목록을 읽지 못하면 빈 결과와 컨텍스트 오류를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		adapter := newFetchAdapter()

		// when
		got := adapter.FetchContextResources(context.Background(), "prod")

		// then
		if got.Namespaces == nil || len(got.Namespaces) != 0 {
			t.Fatalf("Namespaces = %+v, want empty non-nil map", got.Namespaces)
		}
		if got.Err == nil || !strings.Contains(got.Err.Error(), "context prod: unreachable") {
			t.Fatalf("Err = %v, want context error", got.Err)
		}
	})
}
