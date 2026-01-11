package store

import (
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestNotify(t *testing.T) {
	t.Parallel()
	t.Run("Notify는 상태 줄과 기록에 같은 이벤트를 남기고 ID를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		s.now = func() time.Time { return fixed }

		// when
		id := s.Notify(LevelSuccess, "done")

		// then
		state := s.GetState()
		want := Event{ID: id, Level: LevelSuccess, Text: "done", Time: fixed}
		if id == 0 || state.Status != want {
			t.Fatalf("status = %+v, want %+v", state.Status, want)
		}
		if len(state.History) != 1 || state.History[0] != want {
			t.Fatalf("history = %+v", state.History)
		}
	})

	t.Run("경고와 오류만 읽지 않은 수를 늘린다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		levels := []Level{LevelInfo, LevelSuccess, LevelWarning, LevelError, LevelWarning}

		// when
		for _, level := range levels {
			s.Notify(level, level.String())
		}

		// then
		if got := s.GetState().Unread; got != 3 {
			t.Fatalf("unread = %d, want 3", got)
		}
	})

	t.Run("기록은 500개까지만 남기고 가장 오래된 것부터 버린다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		total := HistoryLimit + 7

		// when
		for i := range total {
			s.Notify(LevelInfo, fmt.Sprintf("e%d", i))
		}

		// then
		history := s.GetState().History
		if len(history) != HistoryLimit {
			t.Fatalf("history length = %d, want %d", len(history), HistoryLimit)
		}
		if history[0].Text != "e7" || history[len(history)-1].Text != fmt.Sprintf("e%d", total-1) {
			t.Fatalf("history spans %q..%q", history[0].Text, history[len(history)-1].Text)
		}
	})

	t.Run("이전에 받은 기록 조각은 새 Notify로 바뀌지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		for i := range HistoryLimit {
			s.Notify(LevelInfo, fmt.Sprintf("e%d", i))
		}
		before := s.GetState().History

		// when
		s.Notify(LevelInfo, "newest")

		// then
		if before[0].Text != "e0" || before[len(before)-1].Text != fmt.Sprintf("e%d", HistoryLimit-1) {
			t.Fatalf("old snapshot changed: %q..%q", before[0].Text, before[len(before)-1].Text)
		}
	})
}

// manualScheduler records the expiry timers of a store so that a test fires them itself.
type manualScheduler struct {
	mu    sync.Mutex
	tasks []scheduledTask
}

type scheduledTask struct {
	delay time.Duration
	fn    func()
}

func (m *manualScheduler) afterFunc(delay time.Duration, fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks = append(m.tasks, scheduledTask{delay: delay, fn: fn})
}

func (m *manualScheduler) scheduled() []scheduledTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.tasks)
}

func TestStatusExpiry(t *testing.T) {
	t.Parallel()
	t.Run("정보 이벤트는 TTL이 지나면 상태 줄에서 사라진다", func(t *testing.T) {
		t.Parallel()
		// given
		scheduler := &manualScheduler{}
		s := NewStore(WithStatusTTL(20*time.Millisecond), withScheduler(scheduler.afterFunc))
		s.Notify(LevelInfo, "short lived")
		timers := scheduler.scheduled()
		if len(timers) != 1 || timers[0].delay != 20*time.Millisecond {
			t.Fatalf("timers = %+v, want one timer of the TTL", timers)
		}

		// when
		timers[0].fn()

		// then
		if got := s.GetState().Status.ID; got != 0 {
			t.Fatalf("status id = %d, want expired", got)
		}
		if len(s.GetState().History) != 1 {
			t.Fatal("expiry must keep the history entry")
		}
	})

	t.Run("오래된 만료 타이머는 더 새로운 오류를 지우지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		scheduler := &manualScheduler{}
		s := NewStore(WithStatusTTL(20*time.Millisecond), withScheduler(scheduler.afterFunc))
		s.Notify(LevelInfo, "old")
		errID := s.Notify(LevelError, "broken")

		// when
		for _, timer := range scheduler.scheduled() {
			timer.fn()
		}

		// then
		if got := s.GetState().Status; got.ID != errID || got.Text != "broken" {
			t.Fatalf("status = %+v, want the error", got)
		}
	})

	t.Run("경고와 오류는 만료 타이머를 걸지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		scheduler := &manualScheduler{}
		s := NewStore(WithStatusTTL(20*time.Millisecond), withScheduler(scheduler.afterFunc))

		// when
		s.Notify(LevelWarning, "sticky")
		s.Notify(LevelError, "broken")

		// then
		if timers := scheduler.scheduled(); len(timers) != 0 {
			t.Fatalf("timers = %+v, want none", timers)
		}
	})

	t.Run("TTL이 0이면 만료 타이머를 걸지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		scheduler := &manualScheduler{}
		s := NewStore(WithStatusTTL(0), withScheduler(scheduler.afterFunc))

		// when
		s.Notify(LevelInfo, "kept")

		// then
		if timers := scheduler.scheduled(); len(timers) != 0 {
			t.Fatalf("timers = %+v, want none", timers)
		}
	})

	t.Run("기본 스케줄러는 지연 뒤에 함수를 한 번 실행한다", func(t *testing.T) {
		t.Parallel()
		// given
		ran := make(chan struct{}, 2)

		// when
		runAfter(0, func() { ran <- struct{}{} })

		// then
		select {
		case <-ran:
		case <-time.After(waitTimeout):
			t.Fatal("scheduled function did not run")
		}
	})

	t.Run("ExpireStatus는 ID가 다르면 아무것도 바꾸지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		first := s.Notify(LevelInfo, "first")
		second := s.Notify(LevelInfo, "second")

		// when
		s.ExpireStatus(first)

		// then
		if got := s.GetState().Status.ID; got != second {
			t.Fatalf("status id = %d, want %d", got, second)
		}
	})

	t.Run("ExpireStatus는 ID가 같으면 상태 줄을 비운다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		id := s.Notify(LevelInfo, "only")

		// when
		s.ExpireStatus(id)

		// then
		if got := s.GetState().Status; got != (Event{}) {
			t.Fatalf("status = %+v, want empty", got)
		}
	})
}

func TestStatusAndHistoryReset(t *testing.T) {
	t.Parallel()
	t.Run("ClearStatus는 상태 줄만 비우고 기록과 읽지 않은 수는 남긴다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		s.Notify(LevelWarning, "sticky")

		// when
		s.ClearStatus()

		// then
		state := s.GetState()
		if state.Status.ID != 0 || len(state.History) != 1 || state.Unread != 1 {
			t.Fatalf("state = %+v", state)
		}
	})

	t.Run("MarkHistoryRead는 지정한 ID까지의 경고와 오류를 읽음으로 만든다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		s.Notify(LevelError, "a")
		last := s.Notify(LevelWarning, "b")

		// when
		s.MarkHistoryRead(last)

		// then
		if got := s.GetState().Unread; got != 0 {
			t.Fatalf("unread = %d, want 0", got)
		}
	})

	t.Run("기록을 읽은 뒤에 도착한 경고는 읽지 않은 수에 남는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		shown := s.Notify(LevelError, "shown")
		s.Notify(LevelWarning, "arrived while opening")

		// when
		s.MarkHistoryRead(shown)

		// then
		if got := s.GetState().Unread; got != 1 {
			t.Fatalf("unread = %d, want 1", got)
		}
	})
}

func TestLevelString(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level Level
		want  string
	}{
		{LevelInfo, "INFO"},
		{LevelSuccess, "OK"},
		{LevelWarning, "WARN"},
		{LevelError, "ERROR"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("수준 %d는 %s로 표시된다", tc.level, tc.want), func(t *testing.T) {
			t.Parallel()
			// given
			level := tc.level

			// when
			got := level.String()

			// then
			if got != tc.want {
				t.Fatalf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}
