package tui

import (
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
)

// consumed reports whether the pane table input capture swallows the key instead of passing it to the table.
func (r *runningApp) consumed(t *testing.T, area store.FocusArea, event *tcell.EventKey) bool {
	t.Helper()
	return uiValue(t, r, func() bool { return r.app.handleTableKey(r.app.pane(area), event) == nil })
}

func TestTableKeyPassThrough(t *testing.T) {
	t.Parallel()
	t.Run("쓰지 않는 문자 키와 특수 키는 표로 그대로 넘긴다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		character := running.consumed(t, store.FocusContexts, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
		special := running.consumed(t, store.FocusContexts, tcell.NewEventKey(tcell.KeyF3, 0, tcell.ModNone))

		// then
		if character || special {
			t.Fatalf("consumed rune = %v, special = %v, want both false", character, special)
		}
	})

	t.Run("쓰는 키는 표로 넘기지 않고 소비한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		consumed := running.consumed(t, store.FocusContexts, tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone))

		// then
		if !consumed {
			t.Fatal("? must be consumed")
		}
	})

	foreignKeys := []struct {
		name string
		key  *tcell.EventKey
	}{
		{"e", tcell.NewEventKey(tcell.KeyRune, 'e', tcell.ModNone)},
		{"d", tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)},
		{"c", tcell.NewEventKey(tcell.KeyRune, 'c', tcell.ModNone)},
		{"Delete", tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone)},
		{"Backspace2", tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone)},
	}
	for _, tc := range foreignKeys {
		t.Run(tc.name+" 키는 터널 전용이라 다른 창에서 소비되지 않고 아무 일도 하지 않는다", func(t *testing.T) {
			t.Parallel()
			// given
			running := startLoadedApp(t, withManager(&fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}}))
			before := running.app.store.GetState().Status.ID

			// when
			consumed := running.consumed(t, store.FocusContexts, tc.key)

			// then
			running.settle(t)
			if consumed || len(running.manager.unregistered()) != 0 || len(running.copier.copied()) != 0 {
				t.Fatalf("consumed = %v, unregistered = %v, copied = %v", consumed, running.manager.unregistered(), running.copier.copied())
			}
			if running.app.store.GetState().Status.ID != before || uiValue(t, running, func() bool { return running.app.modal != nil }) {
				t.Fatal("the key changed the status or opened a dialog")
			}
		})
	}

	t.Run("Ctrl+P는 Contexts 창 밖에서 소비되지 않고 확인 창도 열지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		consumed := running.consumed(t, store.FocusTunnels, tcell.NewEventKey(tcell.KeyCtrlP, 0, tcell.ModCtrl))

		// then
		if consumed || uiValue(t, running, func() bool { return running.app.modal != nil }) {
			t.Fatalf("consumed = %v, want false and no dialog", consumed)
		}
	})
}

func TestBulkWithoutContext(t *testing.T) {
	t.Parallel()
	t.Run("컨텍스트가 없으면 Ctrl+P는 확인 창 대신 안내를 보여 준다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startTestApp(t)

		// when
		running.screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModCtrl)

		// then
		running.waitStatus(t, "No context under the cursor")
		if uiValue(t, running, func() bool { return running.app.modal != nil }) {
			t.Fatal("no dialog expected")
		}
	})
}

func TestFilterEdgeCases(t *testing.T) {
	t.Parallel()
	t.Run("필터가 열려 있지 않으면 입력 변경을 무시한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		running.onUI(t, func() { running.app.onFilterChanged("zzz") })

		// then
		for _, area := range paneOrder {
			if got := uiValue(t, running, func() string { return running.app.pane(area).filter }); got != "" {
				t.Fatalf("%s filter = %q, want empty", area, got)
			}
		}
	})

	t.Run("같은 문자열이면 행을 다시 그리지 않고 다른 문자열이면 다시 그린다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		running.applyFilter(t, store.FocusServices, "web")
		running.typeText("/")
		eventually(t, "filter input focus", func() bool {
			return uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput })
		})
		cell := func() any { return running.app.pane(store.FocusServices).table.GetCell(1, 0) }
		before := uiValue(t, running, cell)

		// when
		sameText := uiValue(t, running, func() any {
			running.app.onFilterChanged("web")
			return cell()
		})
		newText := uiValue(t, running, func() any {
			running.app.onFilterChanged("website")
			return cell()
		})

		// then
		if sameText != before {
			t.Fatal("unchanged filter text redrew the rows")
		}
		if newText == before {
			t.Fatal("changed filter text did not redraw the rows")
		}
	})

	t.Run("필터가 열려 있지 않을 때 입력 종료 신호는 포커스를 옮기지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)

		// when
		running.onUI(t, func() { running.app.onFilterDone(tcell.KeyEscape) })

		// then
		if !running.focusIs(t, store.FocusServices) {
			t.Fatal("focus moved although no filter was open")
		}
	})

	tabCases := []struct {
		name string
		key  tcell.Key
		want store.FocusArea
	}{
		{"필터 입력에서 Tab은 필터를 닫고 다음 창으로 포커스를 옮긴다", tcell.KeyTab, store.FocusTunnels},
		{"필터 입력에서 Backtab은 필터를 닫고 이전 창으로 포커스를 옮긴다", tcell.KeyBacktab, store.FocusNamespaces},
	}
	for _, tc := range tabCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := startLoadedApp(t)
			running.typeText("3")
			running.waitFocus(t, store.FocusServices)
			running.typeText("/")
			eventually(t, "filter input focus", func() bool {
				return uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput })
			})

			// when
			running.press(tc.key)

			// then
			running.waitFocus(t, tc.want)
			if open := uiValue(t, running, func() store.FocusArea { return running.app.filterPane }); open != "" {
				t.Fatalf("filter still open on %q", open)
			}
		})
	}
}

func TestEscapePane(t *testing.T) {
	t.Parallel()
	t.Run("필터가 없을 때 Esc는 남아 있는 경고를 상태 줄에서 지우고 기록은 남긴다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		if running.app.store.GetState().Status.ID == 0 {
			t.Fatal("fixture must leave a status event")
		}

		// when
		running.press(tcell.KeyEscape)

		// then
		eventually(t, "status cleared", func() bool { return running.app.store.GetState().Status.ID == 0 })
		if len(running.app.store.GetState().History) == 0 {
			t.Fatal("history must keep the cleared event")
		}
	})

	t.Run("필터가 있을 때 Esc는 필터만 지우고 상태 줄은 그대로 둔다", func(t *testing.T) {
		t.Parallel()
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "admin"), tunnel("dev", "api", "web")})
		running.applyFilter(t, store.FocusTunnels, "web")
		running.app.store.Notify(store.LevelWarning, "keep me")

		// when
		running.press(tcell.KeyEscape)

		// then
		eventually(t, "filter cleared", func() bool {
			return len(uiValue(t, running, func() []string { return running.app.pane(store.FocusTunnels).keys })) == 2
		})
		if got := running.statusText(); got != "keep me" {
			t.Fatalf("status = %q, want it kept", got)
		}
	})
}
