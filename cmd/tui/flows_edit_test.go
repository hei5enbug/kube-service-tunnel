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

// editApp starts with dev discovered and the manager's tunnels in the focused Tunnels pane.
func editApp(t *testing.T, manager *fakeManager) *runningApp {
	t.Helper()
	running := startLoadedApp(t, withManager(manager))
	running.typeText("4")
	running.waitFocus(t, store.FocusTunnels)
	return running
}

func (r *runningApp) editOpen(t *testing.T) bool {
	return uiValue(t, r, func() bool { return r.app.modal != nil && r.app.modal.name == editPage })
}

func (r *runningApp) openEditDialog(t *testing.T) {
	t.Helper()
	r.typeText("e")
	eventually(t, "edit dialog open", func() bool { return r.editOpen(t) })
}

// focusedLabel names the focused dialog item by the label of its input field or button.
func (r *runningApp) focusedLabel(t *testing.T) string {
	return uiValue(t, r, func() string {
		switch item := r.app.app.GetFocus().(type) {
		case *tview.InputField:
			return item.GetLabel()
		case *tview.Button:
			return item.GetLabel()
		}
		return ""
	})
}

func (r *runningApp) focusedText(t *testing.T) string {
	return uiValue(t, r, func() string {
		if field, ok := r.app.app.GetFocus().(*tview.InputField); ok {
			return field.GetText()
		}
		return ""
	})
}

// screenLine returns the first drawn line that contains every part, or "" when none does.
func (r *runningApp) screenLine(t *testing.T, parts ...string) string {
	t.Helper()
	index := slices.IndexFunc(r.screenLines(t), func(line string) bool { return containsAll(line, parts...) })
	if index < 0 {
		return ""
	}
	return r.screenLines(t)[index]
}

// fillEdit types the hostname, moves to Local port and types the port.
func (r *runningApp) fillEdit(hostname, port string) {
	r.typeText(hostname)
	r.press(tcell.KeyTab)
	r.typeText(port)
}

func TestEditDialogOpens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tunnel   dns.DNSTunnel
		wantHost string
		wantPort string
	}{
		{"기본 이름과 자동 포트인 터널은 두 칸을 비우고 기본값을 안내한다", tunnel("dev", "api", "web"), "web.api (default)", "auto · now 40000"},
		{"바꾼 이름과 정한 포트는 칸에 미리 채운다", withLocalPort(tunnelAt("dev", "api", "web", "api.local"), 8080), "api.local", "8080"},
		{"정한 포트가 바빠 다른 포트를 쓰면 정한 포트를 채운다", fallbackTunnel(), "web.api (default)", "8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tc.tunnel}})

			// when
			running.openEditDialog(t)

			// then
			if running.screenLine(t, "Service", "dev api/web") == "" {
				t.Fatalf("service line missing:\n%s", strings.Join(running.screenLines(t), "\n"))
			}
			if line := running.screenLine(t, "Hostname"); !strings.Contains(line, tc.wantHost) {
				t.Fatalf("hostname line = %q, want %q", line, tc.wantHost)
			}
			if line := running.screenLine(t, "Local port"); !strings.Contains(line, tc.wantPort) {
				t.Fatalf("local port line = %q, want %q", line, tc.wantPort)
			}
			if got := running.focusedLabel(t); got != "Hostname" {
				t.Fatalf("focus = %q, want Hostname", got)
			}
		})
	}

	t.Run("커서 아래 터널이 없으면 창을 열지 않고 안내한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{})

		// when
		running.typeText("e")

		// then
		running.waitStatus(t, "No tunnel under the cursor")
		if running.editOpen(t) {
			t.Fatal("edit dialog must not open")
		}
	})
}

func fallbackTunnel() dns.DNSTunnel {
	busy := withLocalPort(tunnel("dev", "api", "web"), 8080)
	busy.LocalPort = 40002
	return busy
}

func TestEditDialogKeys(t *testing.T) {
	t.Parallel()
	focusCases := []struct {
		name string
		keys []tcell.Key
		want string
	}{
		{"Tab은 Hostname에서 Local port로 간다", []tcell.Key{tcell.KeyTab}, "Local port"},
		{"Tab을 세 번 누르면 Save 버튼에 간다", []tcell.Key{tcell.KeyTab, tcell.KeyTab, tcell.KeyTab}, "Save"},
		{"Save 다음 Tab은 Hostname으로 돌아온다", []tcell.Key{tcell.KeyTab, tcell.KeyTab, tcell.KeyTab, tcell.KeyTab}, "Hostname"},
		{"Hostname에서 Shift+Tab은 Save 버튼으로 간다", []tcell.Key{tcell.KeyBacktab}, "Save"},
		{"Hostname에서 Enter는 Local port로 간다", []tcell.Key{tcell.KeyEnter}, "Local port"},
	}
	for _, tc := range focusCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
			running.openEditDialog(t)

			// when
			for _, key := range tc.keys {
				running.press(key)
			}

			// then
			eventually(t, "focus on "+tc.want, func() bool { return running.focusedLabel(t) == tc.want })
			if calls := running.manager.updateCalls(); len(calls) != 0 {
				t.Fatalf("update calls = %v, want none", calls)
			}
		})
	}

	closeCases := []struct {
		name  string
		press func(r *runningApp)
	}{
		{"Esc는 저장하지 않고 닫은 뒤 Tunnels로 포커스를 돌린다", func(r *runningApp) { r.press(tcell.KeyEscape) }},
		{"Cancel에서 Enter는 저장하지 않고 닫은 뒤 Tunnels로 포커스를 돌린다", func(r *runningApp) {
			r.press(tcell.KeyTab)
			r.press(tcell.KeyTab)
			r.press(tcell.KeyEnter)
		}},
	}
	for _, tc := range closeCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
			running.openEditDialog(t)
			running.typeText("api.local")

			// when
			tc.press(running)

			// then
			eventually(t, "edit dialog closed", func() bool {
				return uiValue(t, running, func() bool { return running.app.modal == nil && !running.app.pages.HasPage(editPage) })
			})
			running.waitFocus(t, store.FocusTunnels)
			if calls := running.manager.updateCalls(); len(calls) != 0 {
				t.Fatalf("update calls = %v, want none", calls)
			}
		})
	}

	t.Run("칸에 d를 입력하면 글자로 들어가고 터널을 지우지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)

		// when
		running.typeText("d4")

		// then
		eventually(t, "typed text", func() bool { return running.focusedText(t) == "d4" })
		running.settle(t)
		if got := running.manager.unregistered(); len(got) != 0 {
			t.Fatalf("unregistered = %v, want none", got)
		}
		if !running.editOpen(t) || running.app.store.GetState().Focus != store.FocusTunnels {
			t.Fatal("the dialog must stay open over the Tunnels pane")
		}
	})

	t.Run("Local port 칸은 숫자만 다섯 자리까지 받는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		running.press(tcell.KeyTab)

		// when
		running.typeText("8a0801234")

		// then
		eventually(t, "port text", func() bool { return running.focusedText(t) == "80801" })
	})
}

func TestEditValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		hostname  string
		port      string
		wantError string
		wantFocus string
	}{
		{"규칙에 맞지 않는 이름은 오류를 보이고 Hostname에 포커스를 둔다", "a_b", "", `✗ hostname label "a_b" is invalid`, "Hostname"},
		{"포트 0은 범위 오류를 보이고 Local port에 포커스를 둔다", "", "0", "✗ local port 0 is out of range 1-65535", "Local port"},
		{"포트 80은 프록시 포트라는 오류를 보인다", "", "80", "✗ local port 80 is used by the proxy", "Local port"},
		{"포트 70000은 범위 오류를 보인다", "api.local", "70000", "✗ local port 70000 is out of range 1-65535", "Local port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
			running.openEditDialog(t)
			running.fillEdit(tc.hostname, tc.port)

			// when
			running.press(tcell.KeyEnter)

			// then
			eventually(t, "error line", func() bool { return running.screenLine(t, tc.wantError) != "" })
			if got := running.focusedLabel(t); got != tc.wantFocus {
				t.Fatalf("focus = %q, want %q", got, tc.wantFocus)
			}
			if !running.editOpen(t) {
				t.Fatal("dialog closed after a failed save")
			}
			if calls := running.manager.updateCalls(); len(calls) != 0 {
				t.Fatalf("update calls = %v, want none", calls)
			}
		})
	}
}

func TestEditSave(t *testing.T) {
	t.Parallel()
	t.Run("Local port에서 Enter는 옛 주소와 새 이름과 포트로 한 번 갱신하고 설정을 기억한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		running.fillEdit("API.local.", "8080")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updated http://web.api → http://api.local · 127.0.0.1:8080")
		if got := running.manager.updateCalls(); !slices.Equal(got, []updateCall{{DNSURL: "web.api", Hostname: "api.local", LocalPort: 8080}}) {
			t.Fatalf("update calls = %v", got)
		}
		state := running.app.store.GetState()
		if got := state.TunnelSettings[store.SettingKey("dev", "api", "web")]; got != (store.TunnelSetting{Hostname: "api.local", LocalPort: 8080}) {
			t.Fatalf("setting = %+v", got)
		}
		if state.Status.Level != store.LevelSuccess {
			t.Fatalf("level = %v, want success", state.Status.Level)
		}
	})

	t.Run("기본 이름을 직접 적고 Save 버튼을 누르면 빈 이름으로 보내고 바뀐 것이 없다고 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		running.typeText(" Web.Api. ")
		running.press(tcell.KeyBacktab)
		eventually(t, "Save focused", func() bool { return running.focusedLabel(t) == "Save" })

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Nothing changed for http://web.api")
		if got := running.manager.updateCalls(); !slices.Equal(got, []updateCall{{DNSURL: "web.api"}}) {
			t.Fatalf("update calls = %v", got)
		}
		if settings := running.app.store.GetState().TunnelSettings; len(settings) != 0 {
			t.Fatalf("settings = %v, want none", settings)
		}
	})

	t.Run("두 칸을 비우고 저장하면 기본값으로 되돌리고 기억한 설정을 지운다", func(t *testing.T) {
		t.Parallel()
		// given
		custom := withLocalPort(tunnelAt("dev", "api", "web", "api.local"), 8080)
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{custom}})
		running.app.store.SetTunnelSetting(store.SettingKey("dev", "api", "web"), store.TunnelSetting{Hostname: "api.local", LocalPort: 8080})
		running.openEditDialog(t)
		running.clearFocused(len("api.local"))
		running.press(tcell.KeyTab)
		running.clearFocused(len("8080"))

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updated http://api.local → http://web.api · 127.0.0.1:40000 (auto)")
		if got := running.manager.updateCalls(); !slices.Equal(got, []updateCall{{DNSURL: "api.local"}}) {
			t.Fatalf("update calls = %v", got)
		}
		if settings := running.app.store.GetState().TunnelSettings; len(settings) != 0 {
			t.Fatalf("settings = %v, want none", settings)
		}
	})

	t.Run("이름을 바꾸면 순서가 바뀌어도 커서가 그 터널을 따라간다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "admin"), tunnel("dev", "api", "web")}})
		running.moveCursorTo(t, store.FocusTunnels, "web.api")
		running.openEditDialog(t)
		running.typeText("aaa.local")

		// when
		running.press(tcell.KeyTab)
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updated http://web.api → http://aaa.local · 127.0.0.1:40000 (auto)")
		running.waitIdle(t)
		eventually(t, "renamed rows", func() bool {
			return slices.Equal(uiValue(t, running, func() []string { return running.app.pane(store.FocusTunnels).keys }), []string{"aaa.local", "admin.api"})
		})
		if got := running.cursorKey(t, store.FocusTunnels); got != "aaa.local" {
			t.Fatalf("cursor = %q, want aaa.local", got)
		}
		running.waitFocus(t, store.FocusTunnels)
	})
}

func TestRenameWithFilter(t *testing.T) {
	t.Parallel()
	t.Run("필터에 맞지 않는 이름으로 바꾸면 필터를 지우고 커서가 그 터널에 남는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "admin"), tunnel("dev", "api", "web")}})
		running.typeText("/web")
		running.press(tcell.KeyEnter)
		running.moveCursorTo(t, store.FocusTunnels, "web.api")
		running.openEditDialog(t)
		running.typeText("aaa.local")
		running.press(tcell.KeyTab)

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updated http://web.api → http://aaa.local · 127.0.0.1:40000 (auto)")
		running.waitIdle(t)
		eventually(t, "cursor on renamed tunnel", func() bool { return running.cursorKey(t, store.FocusTunnels) == "aaa.local" })
		if filter := uiValue(t, running, func() string { return running.app.pane(store.FocusTunnels).filter }); filter != "" {
			t.Fatalf("filter = %q, want cleared", filter)
		}
	})
}

// clearFocused presses Backspace count times in the focused field.
func (r *runningApp) clearFocused(count int) {
	for range count {
		r.press(tcell.KeyBackspace2)
	}
}

func TestEditFailures(t *testing.T) {
	t.Parallel()
	t.Run("옛 설정 정리만 실패하면 경고하고 설정은 기억한다", func(t *testing.T) {
		t.Parallel()
		// given
		cleanupErr := errors.Join(dns.ErrUpdateCleanup, errors.New("remove hosts entry web.api: busy"))
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}, updateErr: cleanupErr})
		running.openEditDialog(t)
		running.fillEdit("api.local", "")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updated http://web.api → http://api.local · 127.0.0.1:40000 (auto) · "+dns.ErrUpdateCleanup.Error())
		state := running.app.store.GetState()
		if state.Status.Level != store.LevelWarning || !strings.Contains(state.Status.Text, "remove hosts entry web.api: busy") {
			t.Fatalf("status = %+v", state.Status)
		}
		if got := state.TunnelSettings[store.SettingKey("dev", "api", "web")]; got != (store.TunnelSetting{Hostname: "api.local"}) {
			t.Fatalf("setting = %+v", got)
		}
	})

	t.Run("다른 오류면 오류를 알리고 설정을 기억하지 않는다", func(t *testing.T) {
		t.Parallel()
		// given
		updateErr := errors.New("local port 8080 is already in use on 127.0.0.1")
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}, updateErr: updateErr})
		running.openEditDialog(t)
		running.fillEdit("", "8080")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Updating http://web.api failed: local port 8080 is already in use on 127.0.0.1")
		state := running.app.store.GetState()
		if state.Status.Level != store.LevelError {
			t.Fatalf("level = %v, want error", state.Status.Level)
		}
		if len(state.TunnelSettings) != 0 {
			t.Fatalf("settings = %v, want none", state.TunnelSettings)
		}
	})
}

func TestEditKeepsEventLoopAlive(t *testing.T) {
	t.Parallel()
	t.Run("편집 창을 열고 닫은 뒤에도 이벤트 루프가 응답하고 표 단축키가 다시 동작한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		running.press(tcell.KeyEscape)
		running.waitFocus(t, store.FocusTunnels)

		// when
		running.typeText("1")

		// then
		running.waitFocus(t, store.FocusContexts)
		if !eventLoopResponds(running.app.app) {
			t.Fatal("event loop is blocked after closing the edit dialog")
		}
	})
}
