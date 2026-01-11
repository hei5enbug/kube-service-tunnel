package store

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const waitTimeout = 5 * time.Second

func TestSetterDoesNotBlockOnListener(t *testing.T) {
	t.Parallel()
	t.Run("리스너가 막혀 있어도 세터는 즉시 반환한다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		release := make(chan struct{})
		entered := make(chan struct{}, 1)
		s.Subscribe(func(State) {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
		})
		returned := make(chan struct{})

		// when
		go func() {
			s.Notify(LevelInfo, "hello")
			close(returned)
		}()

		// then
		select {
		case <-returned:
		case <-time.After(waitTimeout):
			t.Fatal("setter blocked on listener")
		}
		close(release)
		<-entered
		if got := s.GetState().Status.Text; got != "hello" {
			t.Fatalf("status = %q, want hello", got)
		}
	})

	t.Run("리스너 안에서 세터를 호출해도 교착하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		done := make(chan struct{})
		s.Subscribe(func(st State) {
			if st.Status.Text == "first" {
				s.Notify(LevelInfo, "second")
				return
			}
			close(done)
		})

		// when
		s.Notify(LevelInfo, "first")

		// then
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("listener re-entrancy deadlocked")
		}
	})
}

func TestListenerOrdering(t *testing.T) {
	t.Parallel()
	t.Run("리스너는 커밋된 순서대로 상태를 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		const total = 200
		var mu sync.Mutex
		var seen []string
		done := make(chan struct{})
		s.Subscribe(func(st State) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, st.Status.Text)
			if len(seen) == total {
				close(done)
			}
		})
		want := make([]string, total)
		for i := range want {
			want[i] = fmt.Sprintf("m%d", i)
		}

		// when
		for _, message := range want {
			s.Notify(LevelInfo, message)
		}

		// then
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("listener did not receive all states")
		}
		mu.Lock()
		defer mu.Unlock()
		for i := range want {
			if seen[i] != want[i] {
				t.Fatalf("seen[%d] = %q, want %q", i, seen[i], want[i])
			}
		}
	})

	t.Run("리스너는 동시에 실행되지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore(WithStatusTTL(0))
		const writers, perWriter = 8, 50
		var running, maxRunning, calls int32
		done := make(chan struct{})
		s.Subscribe(func(State) {
			now := atomic.AddInt32(&running, 1)
			for {
				prev := atomic.LoadInt32(&maxRunning)
				if now <= prev || atomic.CompareAndSwapInt32(&maxRunning, prev, now) {
					break
				}
			}
			runtime.Gosched()
			atomic.AddInt32(&running, -1)
			if atomic.AddInt32(&calls, 1) == writers*perWriter {
				close(done)
			}
		})
		var wg sync.WaitGroup

		// when
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < perWriter; i++ {
					s.Notify(LevelInfo, fmt.Sprintf("w%d-%d", w, i))
				}
			}(w)
		}
		wg.Wait()

		// then
		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("listener did not receive all states")
		}
		if got := atomic.LoadInt32(&maxRunning); got != 1 {
			t.Fatalf("max concurrent listeners = %d, want 1", got)
		}
	})
}

func TestTryBeginLoading(t *testing.T) {
	t.Parallel()
	t.Run("로딩 중이 아니면 로딩을 시작하고 true를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore()

		// when
		began := s.TryBeginLoading("loading")

		// then
		if !began || !s.GetState().IsLoading {
			t.Fatalf("began = %v, loading = %v", began, s.GetState().IsLoading)
		}
	})

	t.Run("이미 로딩 중이면 false를 돌려준다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore()
		s.TryBeginLoading("loading")

		// when
		began := s.TryBeginLoading("loading")

		// then
		if began {
			t.Fatal("second TryBeginLoading must return false")
		}
	})

	t.Run("동시에 호출해도 정확히 한 번만 성공한다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore()
		var winners int32
		var wg sync.WaitGroup

		// when
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if s.TryBeginLoading("loading") {
					atomic.AddInt32(&winners, 1)
				}
			}()
		}
		wg.Wait()

		// then
		if got := atomic.LoadInt32(&winners); got != 1 {
			t.Fatalf("winners = %d, want 1", got)
		}
	})

	t.Run("로딩을 끝낸 뒤에는 다시 시작할 수 있다", func(t *testing.T) {
		t.Parallel()
		// given
		s := NewStore()
		s.TryBeginLoading("loading")
		s.SetLoading(false)

		// when
		began := s.TryBeginLoading("loading")

		// then
		if !began {
			t.Fatal("TryBeginLoading must succeed after loading ended")
		}
	})
}
