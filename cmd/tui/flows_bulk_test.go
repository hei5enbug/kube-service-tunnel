package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// settleDelay lets already injected keys reach the event loop before asserting that nothing happened.
const settleDelay = 100 * time.Millisecond

func (r *runningApp) openConfirm(t *testing.T) {
	t.Helper()
	r.screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModCtrl)
	eventually(t, "confirmation open", func() bool {
		return uiValue(t, r, func() bool { return r.app.modal != nil && r.app.modal.name == confirmPage })
	})
}

func (r *runningApp) confirmClosed(t *testing.T) bool {
	return uiValue(t, r, func() bool { return r.app.modal == nil && !r.app.pages.HasPage(confirmPage) })
}

func TestBulkConfirmation(t *testing.T) {
	t.Run("Ctrl+P는 Cancel에 포커스를 둔 확인 창을 연다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)

		// when
		running.screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModCtrl)

		// then
		eventually(t, "Cancel focused", func() bool {
			return uiValue(t, running, func() bool {
				button, ok := running.app.app.GetFocus().(*tview.Button)
				return ok && button.GetLabel() == "Cancel"
			})
		})
		lines := running.screenLines(t)
		if !slices.ContainsFunc(lines, func(line string) bool { return containsAll(line, "Register 4 services in context dev?") }) {
			t.Fatalf("confirmation text missing:\n%v", lines)
		}
	})

	cancelKeys := []struct {
		name  string
		press func(r *runningApp)
	}{
		{"n 키는 매니저를 부르지 않고 취소한다", func(r *runningApp) { r.typeText("n") }},
		{"Esc 키는 매니저를 부르지 않고 취소한다", func(r *runningApp) { r.press(tcell.KeyEscape) }},
		{"Cancel에서 Enter를 누르면 매니저를 부르지 않고 취소한다", func(r *runningApp) { r.press(tcell.KeyEnter) }},
	}
	for _, tc := range cancelKeys {
		t.Run(tc.name, func(t *testing.T) {
			// given
			running := startLoadedApp(t)
			running.openConfirm(t)

			// when
			tc.press(running)

			// then
			eventually(t, "confirmation closed", func() bool { return running.confirmClosed(t) })
			running.waitFocus(t, store.FocusContexts)
			if calls := running.manager.bulkCalls(); len(calls) != 0 {
				t.Fatalf("bulk calls = %v, want none", calls)
			}
		})
	}

	t.Run("y는 활성 컨텍스트가 아니라 커서 아래 컨텍스트의 서비스로 한 번 등록한다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.moveCursorTo(t, store.FocusContexts, "prod")
		running.openConfirm(t)

		// when
		running.typeText("y")

		// then
		eventually(t, "bulk call", func() bool { return len(running.manager.bulkCalls()) == 1 })
		running.waitIdle(t)
		call := running.manager.bulkCalls()[0]
		want := []kube.ServiceRef{{Namespace: "shop", Name: "cart"}, {Namespace: "shop", Name: "pay"}}
		if call.Context != "prod" || !slices.Equal(call.Targets, want) {
			t.Fatalf("bulk call = %+v", call)
		}
		if got := running.app.store.GetState().SelectedContext; got != "dev" {
			t.Fatalf("active context = %q, want dev", got)
		}
	})

	t.Run("오른쪽 화살표로 Register를 고르고 Enter를 누르면 등록한다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.openConfirm(t)
		running.press(tcell.KeyRight)
		eventually(t, "Register focused", func() bool {
			return uiValue(t, running, func() bool {
				button, ok := running.app.app.GetFocus().(*tview.Button)
				return ok && button.GetLabel() == "Register"
			})
		})

		// when
		running.press(tcell.KeyEnter)

		// then
		eventually(t, "bulk call", func() bool { return len(running.manager.bulkCalls()) == 1 })
	})

	t.Run("확인을 거듭 눌러도 등록은 한 번만 실행된다", func(t *testing.T) {
		// given
		gate := make(chan struct{})
		running := startLoadedApp(t, withManager(&fakeManager{bulkGate: gate}))
		running.openConfirm(t)

		// when
		running.typeText("yy")

		// then
		eventually(t, "bulk running", func() bool { return len(running.manager.bulkCalls()) == 1 })
		time.Sleep(settleDelay)
		if calls := running.manager.bulkCalls(); len(calls) != 1 {
			t.Fatalf("bulk calls = %d, want 1", len(calls))
		}
	})

	t.Run("이미 로딩 중이면 startBulk는 두 번째 실행을 시작하지 않는다", func(t *testing.T) {
		// given
		gate := make(chan struct{})
		running := startLoadedApp(t, withManager(&fakeManager{bulkGate: gate}))
		plan := bulkPlan{Context: "dev", Targets: []kube.ServiceRef{{Namespace: "api", Name: "web"}}}

		// when
		running.onUI(t, func() {
			running.app.startBulk(plan)
			running.app.startBulk(plan)
		})

		// then
		eventually(t, "bulk running", func() bool { return len(running.manager.bulkCalls()) == 1 })
		time.Sleep(settleDelay)
		if calls := running.manager.bulkCalls(); len(calls) != 1 {
			t.Fatalf("bulk calls = %d, want 1", len(calls))
		}
	})

	t.Run("등록할 서비스가 없으면 창을 열지 않고 경고한다", func(t *testing.T) {
		// given
		manager := &fakeManager{tunnels: []dns.DNSTunnel{tunnel("prod", "shop", "cart"), tunnel("prod", "shop", "pay")}}
		running := startLoadedApp(t, withManager(manager))
		running.moveCursorTo(t, store.FocusContexts, "prod")

		// when
		running.screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModCtrl)

		// then
		running.waitStatus(t, "Nothing to register in prod (0 not registrable, 2 already registered)")
		if !running.confirmClosed(t) {
			t.Fatal("confirmation must not open")
		}
	})
}

func TestBulkResult(t *testing.T) {
	t.Run("결과 요약을 상태 줄에 보이고 실패와 건너뜀을 한 기록의 다음 줄들에 남긴다", func(t *testing.T) {
		// given
		manager := &fakeManager{bulkResult: dns.BulkRegistrationResult{
			Registered: []dns.DNSTunnel{tunnel("dev", "api", "admin")},
			Failures:   []kube.ServiceRegistrationFailure{{Namespace: "api", Service: "web", Reason: "no ready pod"}},
			Skipped:    []kube.ServiceRegistrationFailure{{Namespace: "api", Service: "website", Reason: "already registered (context prod)"}},
		}}
		running := startLoadedApp(t, withManager(manager))
		running.openConfirm(t)

		// when
		running.typeText("y")

		// then
		running.waitStatus(t, "Registered 1 of 4 in dev · 1 skipped · 1 failed · 2 excluded before start")
		state := running.app.store.GetState()
		lines := strings.Split(state.Status.Text, "\n")
		for _, want := range []string{
			"excluded dev api/grpc: no HTTP port",
			"excluded dev api/ext: no selector",
			"skipped dev api/website: already registered (context prod)",
			"failed dev api/web: no ready pod",
		} {
			if !slices.Contains(lines, want) {
				t.Fatalf("status lines %v lack %q", lines, want)
			}
		}
		if last := state.History[len(state.History)-1]; last.ID != state.Status.ID {
			t.Fatalf("history does not end with the bulk report: %+v", last)
		}
		if state.Status.Level != store.LevelWarning {
			t.Fatalf("level = %v, want warning", state.Status.Level)
		}
	})

	t.Run("등록 호출이 오류를 돌려주면 오류 수준으로 보인다", func(t *testing.T) {
		// given
		running := startLoadedApp(t, withManager(&fakeManager{bulkErr: errors.New("shutting down")}))
		running.openConfirm(t)

		// when
		running.typeText("y")

		// then
		running.waitStatus(t, "Registered 0 of 4 in dev · error: shutting down · 2 excluded before start")
		if level := running.app.store.GetState().Status.Level; level != store.LevelError {
			t.Fatalf("level = %v, want error", level)
		}
	})
}

func TestConfirmTextScrolls(t *testing.T) {
	t.Run("확인 창 글이 창보다 길면 아래 화살표로 스크롤되고 포커스는 버튼에 남는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		text := strings.Repeat("a long context name that wraps over many lines ", 20)
		dialog := uiValue(t, running, func() *tview.Frame {
			frame, _ := running.app.newConfirmDialog(text, func(bool) {})
			frame.SetRect(0, 0, 40, 8)
			return frame
		})
		message := uiValue(t, running, func() *tview.TextView {
			return dialog.GetPrimitive().(*tview.Flex).GetItem(0).(*tview.TextView)
		})

		// when
		running.onUI(t, func() {
			dialog.Draw(running.screen)
			dialog.InputHandler()(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), func(tview.Primitive) {})
		})

		// then
		row := uiValue(t, running, func() int { row, _ := message.GetScrollOffset(); return row })
		if row != 1 {
			t.Fatalf("scroll row = %d, want 1", row)
		}
	})
}
