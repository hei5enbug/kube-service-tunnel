package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

func servicesApp(t *testing.T, options ...testOption) *runningApp {
	t.Helper()
	running := startLoadedApp(t, options...)
	running.typeText("3")
	running.waitFocus(t, store.FocusServices)
	return running
}

// drawnStyle returns the style of one screen cell after the next draw.
func (r *runningApp) drawnStyle(t *testing.T, x, y int) tcell.Style {
	t.Helper()
	r.onUI(t, func() {})
	return uiValue(t, r, func() tcell.Style {
		_, _, style, _ := r.screen.GetContent(x, y)
		return style
	})
}

func TestRegisterService(t *testing.T) {
	t.Parallel()
	t.Run("Enter는 커서 아래 서비스를 등록하고 표시를 갱신한다", func(t *testing.T) {
		t.Parallel()
		// given
		running := servicesApp(t)
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "web"))

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "Port forwarding started: http://web.api")
		want := []registerCall{{Context: "dev", Target: kube.ServiceRef{Namespace: "api", Name: "web"}}}
		if got := running.manager.registerCalls(); !slices.Equal(got, want) {
			t.Fatalf("register calls = %v", got)
		}
		eventually(t, "registered marker", func() bool {
			return uiValue(t, running, func() string { return running.app.pane(store.FocusServices).table.GetCell(4, 0).Text }) == registeredSymbol
		})
		running.waitFocus(t, store.FocusServices)
	})

	cases := []struct {
		name    string
		tunnels []dns.DNSTunnel
		key     string
		want    string
	}{
		{"등록할 수 없는 서비스는 매니저를 부르지 않고 이유를 알린다", nil, serviceKey("api", "grpc"), "api/grpc cannot be registered: no HTTP port"},
		{"다른 컨텍스트의 터널이 쓰는 이름은 그 컨텍스트를 밝히며 거절한다", []dns.DNSTunnel{tunnel("prod", "api", "web")}, serviceKey("api", "web"),
			"http://web.api is used by context prod. Rename that tunnel with e in Tunnels first."},
		{"이미 터널이 있는 서비스는 그 주소를 밝히며 거절한다", []dns.DNSTunnel{tunnelAt("dev", "api", "web", "api.local")}, serviceKey("api", "web"),
			"dev api/web is already registered as http://api.local"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := servicesApp(t, withManager(&fakeManager{tunnels: tc.tunnels}))
			running.moveCursorTo(t, store.FocusServices, tc.key)

			// when
			running.press(tcell.KeyEnter)

			// then
			running.waitStatus(t, tc.want)
			if got := running.manager.registerCalls(); len(got) != 0 {
				t.Fatalf("register calls = %v, want none", got)
			}
		})
	}
}

func TestRefreshActiveContext(t *testing.T) {
	t.Parallel()
	t.Run("r은 활성 컨텍스트만 새로 고치고 선택과 커서와 필터를 유지한다", func(t *testing.T) {
		t.Parallel()
		// given
		refreshed := sampleCluster()["dev"]
		refreshed.Namespaces = map[string][]kube.Service{
			"api":     append(slices.Clone(refreshed.Namespaces["api"]), httpService("api", "webhooks", 80)),
			"default": refreshed.Namespaces["default"],
		}
		fake := &fakeKube{current: "dev", resources: sampleCluster(), refreshed: map[string]kube.ContextResources{"dev": refreshed}}
		running := startLoadedApp(t, withKube(fake))
		running.typeText("3")
		running.waitFocus(t, store.FocusServices)
		running.applyFilter(t, store.FocusServices, "web")
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "website"))

		// when
		running.typeText("r")

		// then
		running.waitStatus(t, "Refreshed dev · 2 namespaces")
		running.waitIdle(t)
		wantKeys := []string{serviceKey("api", "web"), serviceKey("api", "website"), serviceKey("api", "webhooks")}
		serviceKeys := func() []string {
			return uiValue(t, running, func() []string { return running.app.pane(store.FocusServices).keys })
		}
		eventually(t, "refreshed services", func() bool { return slices.Equal(serviceKeys(), wantKeys) })
		if got := running.cursorKey(t, store.FocusServices); got != serviceKey("api", "website") {
			t.Fatalf("cursor = %q", got)
		}
		running.waitFocus(t, store.FocusServices)
	})

	t.Run("새로 고침에 오류가 있으면 경고하고 Namespace 빈 상태가 실패를 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		failed := kube.ContextResources{Namespaces: map[string][]kube.Service{}, Err: errors.New("context dev: unauthorized")}
		fake := &fakeKube{current: "dev", resources: sampleCluster(), refreshed: map[string]kube.ContextResources{"dev": failed}}
		running := startLoadedApp(t, withKube(fake))

		// when
		running.typeText("r")

		// then
		running.waitStatus(t, "Refreshed dev with errors: context dev: unauthorized")
		eventually(t, "discovery failed row", func() bool {
			return uiValue(t, running, func() string { return emptyRows(running.app.pane(store.FocusNamespaces)) }) == discoveryFailedText
		})
	})
}

func TestThemeCycle(t *testing.T) {
	t.Parallel()
	t.Run("F4는 Dark 다음 Light로 바꾸고 알린다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)

		// when
		running.press(tcell.KeyF4)

		// then
		running.waitStatus(t, "Theme: Light")
		if got := uiValue(t, running, func() string { return running.app.theme.Name }); got != "Light" {
			t.Fatalf("theme = %q", got)
		}
	})

	t.Run("Mono 테마는 선택한 행을 반전으로 그린다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.press(tcell.KeyF4)
		running.waitStatus(t, "Theme: Light")

		// when
		running.press(tcell.KeyF4)

		// then
		running.waitStatus(t, "Theme: Mono")
		style := running.drawnStyle(t, 2, 2)
		if _, _, attrs := style.Decompose(); attrs&tcell.AttrReverse == 0 {
			t.Fatalf("selected cell style = %v, want reverse video", style)
		}
	})

	t.Run("Mono 다음 F4는 다시 Dark로 돌아간다", func(t *testing.T) {
		t.Parallel()
		// given
		running := startLoadedApp(t)
		running.press(tcell.KeyF4)
		running.press(tcell.KeyF4)
		running.waitStatus(t, "Theme: Mono")

		// when
		running.press(tcell.KeyF4)

		// then
		running.waitStatus(t, "Theme: Dark")
	})
}

func TestDetails(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		pane  string
		setup func(t *testing.T, r *runningApp)
		want  []string
	}{
		{"Context 상세는 발견 오류 전문을 보여 준다", "1", func(t *testing.T, r *runningApp) {
			r.moveCursorTo(t, store.FocusContexts, "prod")
		}, []string{"Context", "prod", "failed", "forbidden", "Namespaces", "Services"}},
		{"Namespace 상세는 등록할 수 있는 수를 보여 준다", "2", nil, []string{"Namespace", "api", "Registrable", "3"}},
		{"Service 상세는 포트와 선택자와 자격을 보여 준다", "3", func(t *testing.T, r *runningApp) {
			r.moveCursorTo(t, store.FocusServices, serviceKey("api", "ext"))
		}, []string{"ClusterIP", "10.0.0.10", "http: port 80, targetPort 80, TCP", "Selector", "none", "no selector"}},
		{"Tunnel 상세는 서비스와 이름과 로컬 포트와 파드 포트를 보여 준다", "4", nil, []string{
			"http://web.api", "dev api/web", "web.api (default)", "127.0.0.1:40000 (auto)", "web-7d9f8b6c5-x2k4q", "8080",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// given
			running := startLoadedApp(t, withManager(&fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}}))
			running.typeText(tc.pane)
			running.waitFocus(t, paneOrder[tc.pane[0]-'1'])
			if tc.setup != nil {
				tc.setup(t, running)
			}

			// when
			running.typeText("i")

			// then
			eventually(t, "details open", func() bool {
				return uiValue(t, running, func() bool { return running.app.modal != nil && running.app.modal.name == detailsPage })
			})
			text := strings.Join(running.screenLines(t), "\n")
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("details lack %q:\n%s", want, text)
				}
			}
		})
	}

	t.Run("행이 없으면 상세 대신 안내를 보여 준다", func(t *testing.T) {
		t.Parallel()
		// given
		running := tunnelsApp(t, nil)

		// when
		running.typeText("i")

		// then
		running.waitStatus(t, "Nothing under the cursor")
	})
}
