package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestEnterMovesFocus(t *testing.T) {
	t.Parallel()
	t.Run("Context에서 Enter를 누르면 커서 아래 컨텍스트를 활성화하고 Namespace로 간다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.moveCursorTo(t, store.FocusContexts, "prod")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitFocus(t, store.FocusNamespaces)
		if state := running.app.store.GetState(); state.SelectedContext != "prod" || state.SelectedNamespace != "shop" {
			t.Fatalf("selected = %q/%q, want prod/shop", state.SelectedContext, state.SelectedNamespace)
		}
	})

	t.Run("Namespace에서 Enter를 누르면 네임스페이스를 활성화하고 Services로 간다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("2")
		running.waitFocus(t, store.FocusNamespaces)
		running.moveCursorTo(t, store.FocusNamespaces, "default")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitFocus(t, store.FocusServices)
		if got := running.app.store.GetState().SelectedNamespace; got != "default" {
			t.Fatalf("selected namespace = %q, want default", got)
		}
		if got := running.cursorKey(t, store.FocusServices); got != serviceKey("default", "hello") {
			t.Fatalf("service cursor = %q", got)
		}
	})

	t.Run("컨텍스트를 바꾸면 Namespace와 Services 필터를 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		running.applyFilter(t, store.FocusServices, "web")
		running.typeText("1")
		running.waitFocus(t, store.FocusContexts)
		running.moveCursorTo(t, store.FocusContexts, "prod")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitFocus(t, store.FocusNamespaces)
		if got := uiValue(t, running, func() string { return running.app.pane(store.FocusServices).filter }); got != "" {
			t.Fatalf("services filter = %q, want empty", got)
		}
	})

	t.Run("Shift+Tab은 이전 창으로 가고 처음에서는 마지막 창으로 돈다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		running.press(tcell.KeyBacktab)

		// then
		running.waitFocus(t, store.FocusTunnels)
	})

	t.Run("Tab은 마지막 창에서 처음 창으로 돈다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("4")
		running.waitFocus(t, store.FocusTunnels)

		// when
		running.press(tcell.KeyTab)

		// then
		running.waitFocus(t, store.FocusContexts)
	})
}

func TestStartupSelection(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		current    string
		currentErr error
		want       string
	}{
		{"kubeconfig 현재 컨텍스트를 미리 고른다", "prod", nil, "prod"},
		{"현재 컨텍스트가 목록에 없으면 첫 컨텍스트를 고른다", "gone", nil, "dev"},
		{"현재 컨텍스트를 읽지 못하면 첫 컨텍스트를 고른다", "", errors.New("no kubeconfig"), "dev"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			fake := &fakeKube{current: tc.current, currentErr: tc.currentErr, resources: sampleCluster()}
			running := startTestApp(t, withKube(fake))

			// when
			running.app.start()

			// then
			running.waitIdle(t)
			if got := running.app.store.GetState().SelectedContext; got != tc.want {
				t.Fatalf("selected context = %q, want %q", got, tc.want)
			}
			if got := running.cursorKey(t, store.FocusContexts); got != tc.want {
				t.Fatalf("context cursor = %q, want %q", got, tc.want)
			}
			running.waitFocus(t, store.FocusContexts)
		})
	}

	t.Run("발견 실패는 요약 아래 컨텍스트별 줄을 단 경고 하나로 남긴다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startTestApp(t, withKube(&fakeKube{current: "dev", resources: sampleCluster()}))

		// when
		running.app.start()

		// then
		running.waitIdle(t)
		state := running.app.store.GetState()
		want := "Loaded 2 contexts · 1 failed\nDiscovery failed for prod: context prod namespace audit: forbidden"
		if state.Status.Text != want || state.Status.Level != store.LevelWarning {
			t.Fatalf("status = %+v", state.Status)
		}
		if len(state.History) != 1 || state.Unread != 1 {
			t.Fatalf("history = %+v, unread = %d", state.History, state.Unread)
		}
		lines := running.screenLines(t)
		if !slices.ContainsFunc(lines, func(line string) bool { return containsAll(line, "prod", "!") }) {
			t.Fatalf("no failed marker on screen:\n%v", lines)
		}
	})

	t.Run("컨텍스트 목록을 읽지 못하면 오류를 보여 준다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startTestApp(t, withKube(&fakeKube{listErr: errors.New("bad kubeconfig")}))

		// when
		running.app.start()

		// then
		running.waitIdle(t)
		if got := running.statusText(); got != "Cannot list contexts: bad kubeconfig" {
			t.Fatalf("status = %q", got)
		}
	})
}

// navigateAround activates the context and namespace under the cursor, then jumps and tabs between panes.
func (r *runningApp) navigateAround() {
	r.press(tcell.KeyEnter)
	r.press(tcell.KeyEnter)
	r.typeText("1")
	r.press(tcell.KeyTab)
}

func TestStatusPersistence(t *testing.T) {
	t.Parallel()
	t.Run("경고는 탐색을 해도 상태 줄에 남는다", func(t *testing.T) {
		t.Parallel()
		// given
		manager := &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}}
		running := startLoadedApp(t, withManager(manager))
		manager.loseTunnel("web.api")
		lost := "Tunnel lost: http://web.api (context dev): pod restarted. Press Enter on the service to register it again."
		running.waitStatus(t, lost)

		// when
		running.navigateAround()

		// then
		running.waitFocus(t, store.FocusNamespaces)
		if got := running.statusText(); got != lost {
			t.Fatalf("status = %q, want the lost-tunnel warning", got)
		}
	})

	t.Run("오래된 만료 타이머는 더 새로운 오류를 지우지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		manager := &fakeManager{registerErr: errors.New("no ready pod")}
		running := startLoadedApp(t, withManager(manager), withStore(store.WithStatusTTL(0)))
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "web"))
		running.press(tcell.KeyF4)
		running.waitStatus(t, "Theme: Light")
		themeEvent := running.app.store.GetState().Status.ID
		running.press(tcell.KeyEnter)
		running.waitStatus(t, "Registering web.api failed: no ready pod")

		// when
		running.app.store.ExpireStatus(themeEvent)

		// then
		if got := running.app.store.GetState().Status; got.Level != store.LevelError || got.Text != "Registering web.api failed: no ready pod" {
			t.Fatalf("status = %+v, want the error", got)
		}
	})

	t.Run("표에서 Esc를 누르면 필터가 없을 때 상태 줄을 비운다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		running.press(tcell.KeyEscape)

		// then
		eventually(t, "status cleared", func() bool { return running.app.store.GetState().Status.ID == 0 })
	})
}

func (r *runningApp) resize(t *testing.T, width, height int) {
	t.Helper()
	r.screen.SetSize(width, height)
	if err := r.screen.PostEvent(tcell.NewEventResize(width, height)); err != nil {
		t.Fatalf("post resize: %v", err)
	}
	eventually(t, "layout for new size", func() bool {
		return uiValue(t, r, func() bool { return r.app.layout.width == width && r.app.layout.height == height })
	})
}

func TestCompactLayout(t *testing.T) {
	t.Parallel()
	prepare := func(t *testing.T) *runningApp {
		running := startLoadedApp(t)
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		running.applyFilter(t, store.FocusServices, "we")
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "website"))
		return running
	}

	t.Run("80열보다 좁아지면 포커스된 창만 보이고 필터와 커서를 유지한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := prepare(t)

		// when
		running.resize(t, 70, 20)

		// then
		body := uiValue(t, running, func() []tview.Primitive { return bodyItems(running.app) })
		if len(body) != 1 || body[0] != running.app.pane(store.FocusServices).table {
			t.Fatalf("compact body = %v", body)
		}
		running.assertFilterAndCursor(t)
		if header := running.screenLines(t)[0]; !containsAll(header, "1 Context", "[3 Services]", "4 Tunnels") {
			t.Fatalf("header = %q", header)
		}
	})

	t.Run("다시 넓어지면 일반 배치로 돌아오고 필터와 커서를 유지한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := prepare(t)
		running.resize(t, 70, 20)

		// when
		running.resize(t, 100, 30)

		// then
		if got := uiValue(t, running, func() int { return len(bodyItems(running.app)) }); got != 3 {
			t.Fatalf("normal body items = %d, want 3", got)
		}
		running.assertFilterAndCursor(t)
	})

	t.Run("좁은 배치에서 숫자 키는 보이는 창을 바꾼다", func(t *testing.T) {
		t.Parallel()
		// given
		running := prepare(t)
		running.resize(t, 70, 15)

		// when
		running.typeText("4")

		// then
		running.waitFocus(t, store.FocusTunnels)
		body := uiValue(t, running, func() []tview.Primitive { return bodyItems(running.app) })
		if len(body) != 1 || body[0] != running.app.pane(store.FocusTunnels).table {
			t.Fatalf("compact body = %v", body)
		}
	})
}

func (r *runningApp) assertFilterAndCursor(t *testing.T) {
	t.Helper()
	if !r.focusIs(t, store.FocusServices) {
		t.Fatal("focus left the Services pane")
	}
	if got := uiValue(t, r, func() string { return r.app.pane(store.FocusServices).filter }); got != "we" {
		t.Fatalf("filter = %q, want we", got)
	}
	if got := r.cursorKey(t, store.FocusServices); got != serviceKey("api", "website") {
		t.Fatalf("cursor = %q, want api/website", got)
	}
}

func bodyItems(a *App) []tview.Primitive {
	items := make([]tview.Primitive, a.body.GetItemCount())
	for i := range items {
		items[i] = a.body.GetItem(i)
	}
	return items
}

func TestModalsKeepEventLoopAlive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		page string
		open func(r *runningApp)
	}{
		{"? 도움말", helpPage, func(r *runningApp) { r.typeText("?") }},
		{"F1 도움말", helpPage, func(r *runningApp) { r.press(tcell.KeyF1) }},
		{"F2 기록", historyPage, func(r *runningApp) { r.press(tcell.KeyF2) }},
		{"i 상세", detailsPage, func(r *runningApp) { r.typeText("i") }},
		{"Ctrl+P 확인", confirmPage, func(r *runningApp) { r.screen.InjectKey(tcell.KeyCtrlP, 0, tcell.ModCtrl) }},
	}
	for _, tc := range cases {
		t.Run(tc.name+" 창을 열고 Esc로 닫으면 포커스가 돌아오고 이벤트 루프가 응답한다", func(t *testing.T) {
			t.Parallel()
			// given
			running := startLoadedApp(t)
			tc.open(running)
			eventually(t, tc.page+" open", func() bool {
				return uiValue(t, running, func() bool { return running.app.modal != nil && running.app.modal.name == tc.page })
			})

			// when
			running.press(tcell.KeyEscape)

			// then
			eventually(t, "modal closed", func() bool {
				return uiValue(t, running, func() bool { return running.app.modal == nil && !running.app.pages.HasPage(tc.page) })
			})
			running.waitFocus(t, store.FocusContexts)
			if !eventLoopResponds(running.app.app) {
				t.Fatal("event loop is blocked after closing the modal")
			}
		})
	}

	t.Run("모달이 열려 있으면 표 단축키가 동작하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.typeText("?")
		eventually(t, "help open", func() bool { return uiValue(t, running, func() bool { return running.app.modal != nil }) })

		// when
		running.typeText("4")

		// then
		running.settle(t)
		if focus := running.app.store.GetState().Focus; focus != store.FocusContexts {
			t.Fatalf("focus = %q, want contexts", focus)
		}
		if !uiValue(t, running, func() bool { return running.app.modal != nil }) {
			t.Fatal("help closed unexpectedly")
		}
	})

	t.Run("F2로 기록을 열면 읽지 않은 수가 0이 된다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		if running.app.store.GetState().Unread == 0 {
			t.Fatal("fixture must have unread warnings")
		}

		// when
		running.press(tcell.KeyF2)

		// then
		eventually(t, "unread reset", func() bool { return running.app.store.GetState().Unread == 0 })
	})
}

func containsAll(text string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
