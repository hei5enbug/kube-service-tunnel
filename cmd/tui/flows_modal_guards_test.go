package tui

import (
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func ignoreFocus(tview.Primitive) {}

func (r *runningApp) modalName(t *testing.T) string {
	return uiValue(t, r, func() string {
		if r.app.modal == nil {
			return ""
		}
		return r.app.modal.name
	})
}

func (r *runningApp) focusedButton(t *testing.T) string {
	return uiValue(t, r, func() string {
		if button, ok := r.app.app.GetFocus().(*tview.Button); ok {
			return button.GetLabel()
		}
		return ""
	})
}

// dialogHandler returns the input handler of the dialog in front, the way the event loop reaches its key capture.
func dialogHandler(a *App) func(*tcell.EventKey, func(tview.Primitive)) {
	_, front := a.pages.GetFrontPage()
	return front.InputHandler()
}

func TestModalLifecycle(t *testing.T) {
	t.Run("다른 창이 열려 있으면 새 창을 열기 전에 먼저 닫는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.onUI(t, running.app.showHistory)

		// when
		running.onUI(t, running.app.showHelp)

		// then
		open := uiValue(t, running, func() bool {
			return running.app.modal.name == helpPage && running.app.pages.HasPage(helpPage) && !running.app.pages.HasPage(historyPage)
		})
		if !open {
			t.Fatal("only the help dialog should remain")
		}
	})

	t.Run("열린 창이 없을 때 닫아도 포커스는 그대로다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)

		// when
		running.onUI(t, running.app.closeModal)

		// then
		if !running.focusIs(t, store.FocusServices) {
			t.Fatal("focus moved although no dialog was open")
		}
	})

	t.Run("창을 열기 전 포커스를 알 수 없으면 닫을 때 저장소의 포커스 창으로 돌아간다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("?")
		eventually(t, "help open", func() bool { return running.modalName(t) == helpPage })
		running.onUI(t, func() { running.app.modal.prevFocus = nil })
		running.app.store.SetFocus(store.FocusTunnels)

		// when
		running.press(tcell.KeyEscape)

		// then
		running.waitFocus(t, store.FocusTunnels)
	})
}

func TestLoadingRestoresFocus(t *testing.T) {
	t.Run("창이 열린 채 로딩이 끝나면 포커스가 그 창으로 돌아간다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("?")
		eventually(t, "help open", func() bool { return running.modalName(t) == helpPage })
		running.app.store.TryBeginLoading("busy")
		eventually(t, "loading shown", func() bool { return uiValue(t, running, func() bool { return running.app.loading != nil }) })

		// when
		running.app.store.SetLoading(false)

		// then
		running.waitIdle(t)
		if !uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.modal.focus }) {
			t.Fatal("focus did not return to the open dialog")
		}
	})

	t.Run("필터 입력 중 로딩이 끝나면 포커스가 필터 입력으로 돌아간다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("/")
		eventually(t, "filter input focus", func() bool {
			return uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput })
		})
		running.app.store.TryBeginLoading("busy")
		eventually(t, "loading shown", func() bool { return uiValue(t, running, func() bool { return running.app.loading != nil }) })

		// when
		running.app.store.SetLoading(false)

		// then
		running.waitIdle(t)
		if !uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput }) {
			t.Fatal("focus did not return to the filter input")
		}
	})

	t.Run("이미 멈춘 애니메이션이 틱을 받아도 로딩 상태를 건드리지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		stale := make(chan struct{})
		finished := make(chan error, 1)
		go func() {
			running.app.animateLoading(stale)
			finished <- nil
		}()

		// when
		time.Sleep(3 * loadingFrameInterval)

		// then
		if !eventLoopResponds(running.app.app) || uiValue(t, running, func() bool { return running.app.loading != nil }) {
			t.Fatal("stale animation broke the event loop or created a loading view")
		}
		close(stale)
		if !waitFor(finished, eventLoopTimeout) {
			t.Fatal("animation did not stop")
		}
	})
}

func TestConfirmDialogKeys(t *testing.T) {
	t.Run("오른쪽 화살표는 Register로 왼쪽 화살표는 Cancel로 포커스를 옮긴다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.openConfirm(t)

		// when
		running.press(tcell.KeyRight)
		eventually(t, "Register focused", func() bool { return running.focusedButton(t) == "Register" })
		running.press(tcell.KeyLeft)

		// then
		eventually(t, "Cancel focused", func() bool { return running.focusedButton(t) == "Cancel" })
	})

	t.Run("다른 문자 키는 확인 창을 닫지도 등록하지도 않는다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.openConfirm(t)

		// when
		running.typeText("x")

		// then
		time.Sleep(settleDelay)
		if running.confirmClosed(t) || len(running.manager.bulkCalls()) != 0 {
			t.Fatal("an unrelated key changed the confirmation")
		}
	})

	t.Run("취소한 뒤에 도착한 Register 입력은 등록을 시작하지 않는다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.openConfirm(t)
		enter := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)

		// when
		running.onUI(t, func() {
			form := running.app.modal.focus.(*tview.Form)
			form.GetButton(0).InputHandler()(enter, ignoreFocus)
			form.GetButton(1).InputHandler()(enter, ignoreFocus)
		})

		// then
		time.Sleep(settleDelay)
		if calls := running.manager.bulkCalls(); len(calls) != 0 {
			t.Fatalf("bulk calls = %v, want none", calls)
		}
		if running.app.store.GetState().IsLoading {
			t.Fatal("a late Register press started loading")
		}
	})

	t.Run("결정이 끝난 뒤의 입력은 그 사이에 열린 다른 창을 닫지 않는다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.openConfirm(t)
		enter := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)

		// when
		running.onUI(t, func() {
			cancel := running.app.modal.focus.(*tview.Form).GetButton(0)
			cancel.InputHandler()(enter, ignoreFocus)
			running.app.showHelp()
			cancel.InputHandler()(enter, ignoreFocus)
		})

		// then
		if got := running.modalName(t); got != helpPage {
			t.Fatalf("modal = %q, want help", got)
		}
	})
}

func TestEditDialogGuards(t *testing.T) {
	t.Run("취소가 끝난 뒤의 Esc는 그 사이에 열린 다른 창을 닫지 않는다", func(t *testing.T) {
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		escape := tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)

		// when
		running.onUI(t, func() {
			handler := dialogHandler(running.app)
			handler(escape, ignoreFocus)
			running.app.showHelp()
			handler(escape, ignoreFocus)
		})

		// then
		if got := running.modalName(t); got != helpPage {
			t.Fatalf("modal = %q, want help", got)
		}
	})

	t.Run("저장이 끝난 뒤의 Enter는 그 사이에 열린 다른 창을 닫지 않고 다시 저장하지 않는다", func(t *testing.T) {
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		tab := tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
		enter := tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone)

		// when
		running.onUI(t, func() {
			handler := dialogHandler(running.app)
			for range 3 {
				handler(tab, ignoreFocus)
			}
			handler(enter, ignoreFocus)
			running.app.showHelp()
			handler(enter, ignoreFocus)
		})

		// then
		running.waitIdle(t)
		if got := running.modalName(t); got != helpPage {
			t.Fatalf("modal = %q, want help", got)
		}
		if got := len(running.manager.updateCalls()); got != 1 {
			t.Fatalf("update calls = %d, want 1", got)
		}
	})

	t.Run("다른 작업이 진행 중이면 저장해도 수정을 시작하지 않는다", func(t *testing.T) {
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.app.store.TryBeginLoading("busy")
		request := editRequest{tunnel: tunnel("dev", "api", "web"), hostname: "api.local"}

		// when
		running.onUI(t, func() { running.app.startUpdate(request) })

		// then
		time.Sleep(settleDelay)
		if got := len(running.manager.updateCalls()); got != 0 {
			t.Fatalf("update calls = %d, want 0", got)
		}
	})
}
