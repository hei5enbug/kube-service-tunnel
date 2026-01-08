package store

import (
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

// deliveredDuring runs change and returns how many states listeners received because of it. A trailing
// TunnelsChanged marks the end, since listeners receive states in commit order.
func deliveredDuring(t *testing.T, s *Store, change func()) int {
	t.Helper()
	delivered := make(chan State, 16)
	s.Subscribe(func(state State) { delivered <- state })

	change()
	s.TunnelsChanged()

	count := 0
	timeout := time.After(waitTimeout)
	for {
		select {
		case state := <-delivered:
			if state.TunnelsRevision > 0 {
				return count
			}
			count++
		case <-timeout:
			t.Fatal("listener did not receive the end marker")
		}
	}
}

func TestNoOpSettersDoNotNotify(t *testing.T) {
	t.Run("ClearStatus는 상태 줄이 비어 있으면 리스너에게 알리지 않는다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		delivered := deliveredDuring(t, s, s.ClearStatus)

		// then
		if delivered != 0 {
			t.Fatalf("delivered = %d, want 0", delivered)
		}
	})

	t.Run("MarkHistoryRead는 읽지 않은 수가 그대로면 리스너에게 알리지 않는다", func(t *testing.T) {
		// given
		s := NewStore(WithStatusTTL(0))
		last := s.Notify(LevelError, "a")
		s.MarkHistoryRead(last)

		// when
		delivered := deliveredDuring(t, s, func() { s.MarkHistoryRead(last) })

		// then
		if delivered != 0 {
			t.Fatalf("delivered = %d, want 0", delivered)
		}
	})

	t.Run("ActivateContext는 이미 활성인 컨텍스트면 네임스페이스 선택을 건드리지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")
		s.ActivateNamespace("web")

		// when
		delivered := deliveredDuring(t, s, func() { s.ActivateContext("dev") })

		// then
		if delivered != 0 || s.GetState().SelectedNamespace != "web" {
			t.Fatalf("delivered = %d, namespace = %q", delivered, s.GetState().SelectedNamespace)
		}
	})

	t.Run("ActivateNamespace는 이미 선택한 네임스페이스면 알리지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetAllResources(sampleResources(), "dev")

		// when
		delivered := deliveredDuring(t, s, func() { s.ActivateNamespace("api") })

		// then
		if delivered != 0 {
			t.Fatalf("delivered = %d, want 0", delivered)
		}
	})

	t.Run("SetLoading은 같은 값이면 알리지 않고 라벨도 지우지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.TryBeginLoading("busy")

		// when
		delivered := deliveredDuring(t, s, func() { s.SetLoading(true) })

		// then
		if delivered != 0 || s.GetState().LoadingLabel != "busy" {
			t.Fatalf("delivered = %d, label = %q", delivered, s.GetState().LoadingLabel)
		}
	})

	t.Run("SetFocus는 같은 영역이면 알리지 않는다", func(t *testing.T) {
		// given
		s := NewStore()
		s.SetFocus(FocusServices)

		// when
		delivered := deliveredDuring(t, s, func() { s.SetFocus(FocusServices) })

		// then
		if delivered != 0 {
			t.Fatalf("delivered = %d, want 0", delivered)
		}
	})

	t.Run("바뀌는 세터는 정확히 한 번 알린다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		delivered := deliveredDuring(t, s, func() { s.SetLoading(true) })

		// then
		if delivered != 1 {
			t.Fatalf("delivered = %d, want 1", delivered)
		}
	})
}

func TestSetAllResourcesWithoutContexts(t *testing.T) {
	t.Run("컨텍스트가 하나도 없으면 활성 컨텍스트는 비어 있다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetAllResources(map[string]kube.ContextResources{}, "dev")

		// then
		state := s.GetState()
		if state.SelectedContext != "" || state.SelectedNamespace != "" || len(state.Namespaces) != 0 {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("선호 컨텍스트가 없으면 이름순 첫 컨텍스트를 고른다", func(t *testing.T) {
		// given
		s := NewStore()

		// when
		s.SetAllResources(sampleResources(), "missing")

		// then
		if got := s.GetState().SelectedContext; got != "dev" {
			t.Fatalf("context = %q, want dev", got)
		}
	})
}
