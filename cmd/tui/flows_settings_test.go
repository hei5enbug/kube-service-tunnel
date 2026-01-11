package tui

import (
	"slices"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

// remember stores a setting for a dev service and waits until a rendered Services row, if any, carries it.
func (r *runningApp) remember(t *testing.T, namespace, service string, setting store.TunnelSetting) {
	t.Helper()
	r.app.store.SetTunnelSetting(store.SettingKey("dev", namespace, service), setting)
	want := kube.ServiceRef{Namespace: namespace, Name: service, Hostname: setting.Hostname, LocalPort: setting.LocalPort}
	eventually(t, "rendered setting", func() bool {
		return uiValue(t, r, func() bool {
			index := slices.IndexFunc(r.app.serviceRows, func(row serviceRow) bool { return row.Key() == serviceKey(namespace, service) })
			return index < 0 || r.app.serviceRows[index].Target == want
		})
	})
}

// serviceCells returns the drawn cell texts of the Services row with the given key.
func (r *runningApp) serviceCells(t *testing.T, key string) []string {
	return uiValue(t, r, func() []string {
		p := r.app.pane(store.FocusServices)
		index := slices.Index(p.keys, key)
		if index < 0 {
			return nil
		}
		cells := make([]string, p.table.GetColumnCount())
		for column := range cells {
			cells[column] = p.table.GetCell(p.offset+index, column).Text
		}
		return cells
	})
}

func TestRememberedSettings(t *testing.T) {
	t.Parallel()
	t.Run("잃은 터널을 Enter로 다시 등록하면 편집에서 기억한 이름과 포트를 쓴다", func(t *testing.T) {
		t.Parallel()
		// given
		running := editApp(t, &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}})
		running.openEditDialog(t)
		running.fillEdit("api.local", "8080")
		running.press(tcell.KeyEnter)
		running.waitStatus(t, "Updated http://web.api → http://api.local · 127.0.0.1:8080")
		running.waitIdle(t)
		running.manager.loseTunnel("api.local")
		running.waitStatus(t, "Tunnel lost: http://api.local (context dev): pod restarted. Press Enter on the service to register it again.")
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		eventually(t, "unregistered marker", func() bool {
			cells := running.serviceCells(t, serviceKey("api", "web"))
			return len(cells) > 0 && cells[0] == " "
		})
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "web"))

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Port forwarding started: http://api.local")
		want := []registerCall{{Context: "dev", Target: kube.ServiceRef{Namespace: "api", Name: "web", Hostname: "api.local", LocalPort: 8080}}}
		if got := running.manager.registerCalls(); !slices.Equal(got, want) {
			t.Fatalf("register calls = %+v", got)
		}
	})

	t.Run("Ctrl+P 대상은 서비스마다 기억한 설정을 담는다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.remember(t, "api", "admin", store.TunnelSetting{Hostname: "admin.local", LocalPort: 3001})
		running.openConfirm(t)

		// when
		running.typeText("y")

		// then
		eventually(t, "bulk call", func() bool { return len(running.manager.bulkCalls()) == 1 })
		want := []kube.ServiceRef{
			{Namespace: "api", Name: "admin", Hostname: "admin.local", LocalPort: 3001},
			{Namespace: "api", Name: "web"},
			{Namespace: "api", Name: "website"},
			{Namespace: "default", Name: "hello"},
		}
		if got := running.manager.bulkCalls()[0].Targets; !slices.Equal(got, want) {
			t.Fatalf("targets = %+v", got)
		}
	})

	t.Run("요청한 포트가 바쁘면 등록은 경고로 대신 쓴 포트를 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		running := servicesApp(t, withManager(&fakeManager{busyFallback: 40002}))
		running.remember(t, "api", "web", store.TunnelSetting{Hostname: "api.local", LocalPort: 8080})
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "web"))

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Port forwarding started: http://api.local · local port 8080 was busy, using 40002")
		if level := running.app.store.GetState().Status.Level; level != store.LevelWarning {
			t.Fatalf("level = %v, want warning", level)
		}
	})
}

func TestServicesPaneMarkers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tunnel   dns.DNSTunnel
		wantMark string
		wantNote string
	}{
		{"이름을 바꾼 자기 터널은 ●와 → 새 이름으로 보인다", tunnelAt("dev", "api", "web", "api.local"), registeredSymbol, "→ api.local"},
		{"다른 컨텍스트의 터널이 쓰는 이름은 ○와 그 컨텍스트로 보인다", tunnel("prod", "api", "web"), nameTakenSymbol, "in prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := startLoadedApp(t, withManager(&fakeManager{tunnels: []dns.DNSTunnel{tc.tunnel}}))

			// when
			running.typeText("3")

			// then
			running.waitFocus(t, store.FocusServices)
			cells := running.serviceCells(t, serviceKey("api", "web"))
			if len(cells) != 4 || cells[0] != tc.wantMark || cells[3] != tc.wantNote {
				t.Fatalf("cells = %q, want mark %q and note %q", cells, tc.wantMark, tc.wantNote)
			}
		})
	}
}
