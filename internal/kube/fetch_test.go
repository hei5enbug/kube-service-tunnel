package kube

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFetchAllResources(t *testing.T) {
	t.Run("일부 컨텍스트와 네임스페이스가 실패해도 부분 결과와 합쳐진 오류를 함께 돌려준다", func(t *testing.T) {
		// given
		adapter := &kubeAdapter{
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

		// when
		resources, err := adapter.FetchAllResources(context.Background())

		// then
		if len(resources["dev"]["app"]) != 1 {
			t.Fatalf("resources = %+v, want dev/app with one service", resources)
		}
		if _, ok := resources["dev"]["kube-system"]; ok {
			t.Fatalf("resources = %+v, want system namespace skipped", resources)
		}
		if prod, ok := resources["prod"]; !ok || len(prod) != 0 {
			t.Fatalf("resources = %+v, want failed context prod with empty namespaces", resources)
		}
		if err == nil || !strings.Contains(err.Error(), "context prod: unreachable") || !strings.Contains(err.Error(), "context dev namespace bad: forbidden") {
			t.Fatalf("err = %v, want both context and namespace errors", err)
		}
	})

	t.Run("실패가 없으면 오류 없이 결과를 돌려준다", func(t *testing.T) {
		// given
		adapter := &kubeAdapter{
			contextClient:   &fakeContextClient{contexts: []Context{{Name: "dev"}}},
			namespaceClient: &fakeNamespaceClient{namespaces: []string{"app"}},
			serviceClient:   &fakeServiceClient{byNamespace: map[string][]Service{"app": {httpService("web", "app", selectorApp)}}},
		}

		// when
		resources, err := adapter.FetchAllResources(context.Background())

		// then
		if err != nil || len(resources["dev"]["app"]) != 1 {
			t.Fatalf("resources = %+v, err = %v, want one service and nil error", resources, err)
		}
	})

	t.Run("컨텍스트 목록을 읽지 못하면 오류를 돌려준다", func(t *testing.T) {
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
