package store

import (
	"errors"
	"slices"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

func service(namespace, name string) kube.Service {
	return kube.Service{Name: name, Namespace: namespace}
}

func sampleResources() map[string]kube.ContextResources {
	return map[string]kube.ContextResources{
		"dev": {Namespaces: map[string][]kube.Service{
			"web": {service("web", "front")},
			"api": {service("api", "auth"), service("api", "users")},
		}},
		"prod": {Namespaces: map[string][]kube.Service{"shop": {service("shop", "cart")}}},
	}
}

func TestLoadingLabel(t *testing.T) {
	t.Run("TryBeginLoading은 라벨을 함께 기록한다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.TryBeginLoading("Loading contexts…")

		// then
		if got := s.GetState().LoadingLabel; got != "Loading contexts…" {
			t.Fatalf("label = %q", got)
		}
	})

	t.Run("실패한 TryBeginLoading은 기존 라벨을 바꾸지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.TryBeginLoading("first")

		// when
		s.TryBeginLoading("second")

		// then
		if got := s.GetState().LoadingLabel; got != "first" {
			t.Fatalf("label = %q, want first", got)
		}
	})

	t.Run("SetLoadingLabel은 로딩 중에만 라벨을 바꾼다", func(t *testing.T) {
		// given
		s := NewStore()
		s.TryBeginLoading("start")

		// when
		s.SetLoadingLabel("Registering web.api in dev — 1/2")

		// then
		if got := s.GetState().LoadingLabel; got != "Registering web.api in dev — 1/2" {
			t.Fatalf("label = %q", got)
		}
	})

	t.Run("로딩이 아니면 SetLoadingLabel은 무시된다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetLoadingLabel("late progress")

		// then
		if got := s.GetState().LoadingLabel; got != "" {
			t.Fatalf("label = %q, want empty", got)
		}
	})

	t.Run("로딩을 끝내면 라벨도 지운다", func(t *testing.T) {
		// given
		s := NewStore()
		s.TryBeginLoading("busy")

		// when
		s.SetLoading(false)

		// then
		if state := s.GetState(); state.IsLoading || state.LoadingLabel != "" {
			t.Fatalf("state = %+v", state)
		}
	})
}

func TestSetAllResources(t *testing.T) {
	cases := []struct {
		name      string
		preferred string
		want      string
	}{
		{"kubeconfig 현재 컨텍스트가 목록에 있으면 그것을 활성화한다", "prod", "prod"},
		{"현재 컨텍스트가 목록에 없으면 첫 컨텍스트를 활성화한다", "gone", "dev"},
		{"현재 컨텍스트가 비어 있으면 첫 컨텍스트를 활성화한다", "", "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			s := NewStore()

			// when
			s.SetAllResources(sampleResources(), tc.preferred)

			// then
			state := s.GetState()
			if state.SelectedContext != tc.want {
				t.Fatalf("selected context = %q, want %q", state.SelectedContext, tc.want)
			}
			if !slices.Equal(state.Contexts, []string{"dev", "prod"}) {
				t.Fatalf("contexts = %v", state.Contexts)
			}
		})
	}

	t.Run("첫 네임스페이스를 기본으로 고르고 그 서비스를 보여 준다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetAllResources(sampleResources(), "dev")

		// then
		state := s.GetState()
		if !slices.Equal(state.Namespaces, []string{"api", "web"}) || state.SelectedNamespace != "api" {
			t.Fatalf("namespaces = %v, selected = %q", state.Namespaces, state.SelectedNamespace)
		}
		if len(state.Services) != 2 {
			t.Fatalf("services = %v", state.Services)
		}
	})
}

func TestSetContextResources(t *testing.T) {
	t.Run("새로 고친 컨텍스트만 바꾸고 선택한 네임스페이스는 남아 있으면 유지한다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")
		s.ActivateNamespace("web")
		before := s.GetState().Resources
		refreshed := kube.ContextResources{
			Namespaces: map[string][]kube.Service{"web": {service("web", "front"), service("web", "blog")}},
			Err:        errors.New("context dev namespace api: forbidden"),
		}

		// when
		s.SetContextResources("dev", refreshed)

		// then
		state := s.GetState()
		if state.SelectedNamespace != "web" || len(state.Services) != 2 {
			t.Fatalf("selected = %q, services = %v", state.SelectedNamespace, state.Services)
		}
		if state.Resources["dev"].Err == nil || len(state.Resources["prod"].Namespaces) != 1 {
			t.Fatalf("resources = %+v", state.Resources)
		}
		if len(before["dev"].Namespaces) != 2 {
			t.Fatal("the previous resources map must not change")
		}
	})

	t.Run("선택한 네임스페이스가 사라지면 첫 네임스페이스를 고른다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")
		s.ActivateNamespace("web")

		// when
		s.SetContextResources("dev", kube.ContextResources{Namespaces: map[string][]kube.Service{
			"blog": {service("blog", "ghost")},
		}})

		// then
		if got := s.GetState().SelectedNamespace; got != "blog" {
			t.Fatalf("selected namespace = %q, want blog", got)
		}
	})

	t.Run("비활성 컨텍스트를 새로 고쳐도 선택은 바뀌지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")
		revision := s.GetState().ResourcesRevision

		// when
		s.SetContextResources("prod", kube.ContextResources{})

		// then
		state := s.GetState()
		if state.SelectedContext != "dev" || state.SelectedNamespace != "api" || state.ResourcesRevision == revision {
			t.Fatalf("state = %+v", state)
		}
	})
}

func TestActivate(t *testing.T) {
	t.Run("ActivateContext는 그 컨텍스트의 첫 네임스페이스를 고른다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")

		// when
		s.ActivateContext("prod")

		// then
		state := s.GetState()
		if state.SelectedContext != "prod" || state.SelectedNamespace != "shop" || len(state.Services) != 1 {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("ActivateNamespace는 그 네임스페이스의 서비스를 보여 준다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")

		// when
		s.ActivateNamespace("web")

		// then
		state := s.GetState()
		if state.SelectedNamespace != "web" || len(state.Services) != 1 || state.Services[0].Name != "front" {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("TunnelsChanged는 터널 리비전을 올린다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.TunnelsChanged()

		// then
		if got := s.GetState().TunnelsRevision; got != 1 {
			t.Fatalf("tunnels revision = %d, want 1", got)
		}
	})

	t.Run("SetFocus는 포커스 영역을 바꾼다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetFocus(FocusTunnels)

		// then
		if got := s.GetState().Focus; got != FocusTunnels {
			t.Fatalf("focus = %q, want tunnels", got)
		}
	})
}
