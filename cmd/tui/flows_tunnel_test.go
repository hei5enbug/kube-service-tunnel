package tui

import (
	"errors"
	"slices"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/clipboard"
	"github.com/gdamore/tcell/v2"
)

func tunnelsApp(t *testing.T, tunnels []dns.DNSTunnel, options ...testOption) *runningApp {
	t.Helper()
	manager := &fakeManager{tunnels: tunnels}
	running := startTestApp(t, append([]testOption{withManager(manager)}, options...)...)
	running.typeText("4")
	running.waitFocus(t, store.FocusTunnels)
	return running
}

func (r *runningApp) applyFilter(t *testing.T, area store.FocusArea, query string) {
	t.Helper()
	r.typeText("/")
	eventually(t, "filter input focus", func() bool {
		return uiValue(t, r, func() bool { return r.app.app.GetFocus() == r.app.filterInput })
	})
	r.typeText(query)
	r.press(tcell.KeyEnter)
	r.waitFocus(t, area)
	if got := uiValue(t, r, func() string { return r.app.pane(area).filter }); got != query {
		t.Fatalf("filter = %q, want %q", got, query)
	}
}

func TestTunnelDeletionKeys(t *testing.T) {
	cases := []struct {
		name  string
		press func(r *runningApp)
	}{
		{"Backspace 키는 커서 아래 터널을 지운다", func(r *runningApp) { r.press(tcell.KeyBackspace) }},
		{"Backspace2 키는 커서 아래 터널을 지운다", func(r *runningApp) { r.press(tcell.KeyBackspace2) }},
		{"d 키는 커서 아래 터널을 지운다", func(r *runningApp) { r.typeText("d") }},
		{"Delete 키는 커서 아래 터널을 지운다", func(r *runningApp) { r.press(tcell.KeyDelete) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// given
			running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "admin"), tunnel("dev", "api", "web")})
			running.moveCursorTo(t, store.FocusTunnels, "web.api")

			// when
			tc.press(running)

			// then
			running.waitStatus(t, "Tunnel removed: http://web.api")
			if got := running.manager.unregistered(); !slices.Equal(got, []string{"web.api"}) {
				t.Fatalf("unregistered = %v, want [web.api]", got)
			}
			eventually(t, "tunnel row removed", func() bool {
				return slices.Equal(uiValue(t, running, func() []string { return running.app.pane(store.FocusTunnels).keys }), []string{"admin.api"})
			})
		})
	}
}

func TestTunnelFilterInput(t *testing.T) {
	t.Run("Tunnels 필터에 d1을 입력해도 지우거나 창을 옮기지 않는다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "d1"), tunnel("dev", "api", "web")})
		running.typeText("/")
		eventually(t, "filter input focus", func() bool {
			return uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput })
		})

		// when
		running.typeText("d1")

		// then
		eventually(t, "query d1", func() bool {
			return uiValue(t, running, func() string { return running.app.filterInput.GetText() }) == "d1"
		})
		if got := uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }); got != "d1" {
			t.Fatalf("filter = %q, want d1", got)
		}
		if got := running.manager.unregistered(); len(got) != 0 {
			t.Fatalf("unregistered = %v, want none", got)
		}
		if focus := running.app.store.GetState().Focus; focus != store.FocusTunnels {
			t.Fatalf("store focus = %q, want tunnels", focus)
		}
		if title := uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).table.GetTitle() }); title != " 4 Tunnels /d1 (1/2) " {
			t.Fatalf("title = %q", title)
		}
	})

	t.Run("필터 입력에서 Esc를 누르면 질의를 지우고 표로 돌아간다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "d1"), tunnel("dev", "api", "web")})
		running.typeText("/d1")
		eventually(t, "query d1", func() bool {
			return uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }) == "d1"
		})

		// when
		running.press(tcell.KeyEscape)

		// then
		running.waitFocus(t, store.FocusTunnels)
		if got := uiValue(t, running, func() int { return len(running.app.pane(store.FocusTunnels).keys) }); got != 2 {
			t.Fatalf("visible tunnels = %d, want 2", got)
		}
		if got := uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }); got != "" {
			t.Fatalf("filter = %q, want empty", got)
		}
	})

	t.Run("필터 입력에서 Tab을 누르면 질의를 유지하고 다음 창으로 간다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "web")})
		running.typeText("/we")
		eventually(t, "query we", func() bool {
			return uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }) == "we"
		})

		// when
		running.press(tcell.KeyTab)

		// then
		running.waitFocus(t, store.FocusContexts)
		if got := uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }); got != "we" {
			t.Fatalf("filter = %q, want we", got)
		}
	})

	t.Run("필터 입력에서 F1을 누르면 도움말을 열고 닫으면 입력으로 돌아간다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "web")})
		running.typeText("/")
		running.press(tcell.KeyF1)
		eventually(t, "help open", func() bool {
			return uiValue(t, running, func() bool { return running.app.modal != nil && running.app.modal.name == helpPage })
		})

		// when
		running.press(tcell.KeyEscape)

		// then
		eventually(t, "filter input focus", func() bool {
			return uiValue(t, running, func() bool { return running.app.app.GetFocus() == running.app.filterInput })
		})
	})
}

func TestFilteredDeletion(t *testing.T) {
	t.Run("필터된 표에서는 보이는 행의 터널을 지운다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "admin"), tunnel("dev", "api", "web")})
		running.applyFilter(t, store.FocusTunnels, "web")

		// when
		running.typeText("d")

		// then
		eventually(t, "web.api removed", func() bool { return slices.Equal(running.manager.unregistered(), []string{"web.api"}) })
	})

	t.Run("잃은 터널로 다시 그린 뒤에도 커서가 같은 터널을 가리킨다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{
			tunnel("dev", "api", "admin"), tunnel("dev", "api", "web"), tunnel("dev", "api", "website"), tunnel("dev", "api", "webz"),
		})
		running.applyFilter(t, store.FocusTunnels, "web")
		running.moveCursorTo(t, store.FocusTunnels, "website.api")
		running.manager.loseTunnel("web.api")
		eventually(t, "lost tunnel re-render", func() bool {
			return slices.Equal(uiValue(t, running, func() []string { return running.app.pane(store.FocusTunnels).keys }), []string{"website.api", "webz.api"})
		})

		// when
		running.typeText("d")

		// then
		eventually(t, "website.api removed", func() bool { return slices.Equal(running.manager.unregistered(), []string{"website.api"}) })
	})
}

func TestCopyTunnelURL(t *testing.T) {
	long := tunnel("dev", "payments-platform", "an-extremely-long-service-name-that-cannot-fit")
	url := "http://" + long.DNSURL

	t.Run("c 키는 잘리지 않은 원래 URL을 복사기에 넘긴다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{long})

		// when
		running.typeText("c")

		// then
		running.waitStatus(t, "Copied "+url)
		if got := running.copier.copied(); !slices.Equal(got, []string{url}) {
			t.Fatalf("copied = %v, want [%s]", got, url)
		}
	})

	t.Run("시스템 복사가 실패하면 OSC 52로 터미널에 보낸다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{long}, withCopier(&fakeCopier{err: errors.New("no pasteboard")}))

		// when
		running.typeText("c")

		// then
		running.waitStatus(t, osc52Message)
		if got := string(running.screen.GetClipboardData()); got != url {
			t.Fatalf("OSC 52 data = %q, want %q", got, url)
		}
		history := running.app.store.GetState().History
		if len(history) != 2 || history[0].Text != "System clipboard failed: no pasteboard" {
			t.Fatalf("history = %+v", history)
		}
	})

	t.Run("지원하지 않는 플랫폼이면 실패 기록 없이 OSC 52만 알린다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{long}, withCopier(&fakeCopier{err: clipboard.ErrUnsupported}))

		// when
		running.typeText("c")

		// then
		running.waitStatus(t, osc52Message)
		if history := running.app.store.GetState().History; len(history) != 1 {
			t.Fatalf("history = %+v, want only the OSC 52 notice", history)
		}
	})

	t.Run("터널이 없으면 복사하지 않고 알린다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, nil)

		// when
		running.typeText("c")

		// then
		running.waitStatus(t, "No tunnel under the cursor")
		if got := running.copier.copied(); len(got) != 0 {
			t.Fatalf("copied = %v, want nothing", got)
		}
	})
}

func TestTunnelLost(t *testing.T) {
	t.Run("잃은 터널은 목록에서 사라지고 다시 등록하는 방법을 경고한다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "web")})

		// when
		running.manager.loseTunnel("web.api")

		// then
		running.waitStatus(t, "Tunnel lost: http://web.api (context dev): pod restarted. Press Enter on the service to register it again.")
		if level := running.app.store.GetState().Status.Level; level != store.LevelWarning {
			t.Fatalf("level = %v, want warning", level)
		}
		eventually(t, "empty tunnel table", func() bool {
			return uiValue(t, running, func() int { return len(running.app.pane(store.FocusTunnels).keys) }) == 0
		})
	})
}
