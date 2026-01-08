package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

func TestEnterOnEmptyPanes(t *testing.T) {
	t.Run("Contexts 창에 행이 없으면 Enter는 아무 일도 하지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t)

		// when
		running.press(tcell.KeyEnter)

		// then
		time.Sleep(settleDelay)
		if !running.focusIs(t, store.FocusContexts) || running.app.store.GetState().Status.ID != 0 {
			t.Fatal("Enter on an empty Contexts pane must do nothing")
		}
	})

	t.Run("Namespaces 창에 행이 없으면 Enter는 포커스를 옮기지 않는다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("2")
		running.waitFocus(t, store.FocusNamespaces)

		// when
		running.press(tcell.KeyEnter)

		// then
		time.Sleep(settleDelay)
		if !running.focusIs(t, store.FocusNamespaces) {
			t.Fatal("focus left the empty Namespaces pane")
		}
	})

	t.Run("필터에 걸린 서비스가 없으면 Enter는 등록하지 않고 알린다", func(t *testing.T) {
		// given
		running := servicesApp(t)
		running.applyFilter(t, store.FocusServices, "no-such-service")

		// when
		running.press(tcell.KeyEnter)

		// then
		running.waitStatus(t, "No service under the cursor")
		if calls := running.manager.registerCalls(); len(calls) != 0 {
			t.Fatalf("register calls = %v, want none", calls)
		}
	})

	t.Run("다른 작업이 진행 중이면 서비스를 등록하지 않는다", func(t *testing.T) {
		// given
		running := servicesApp(t)
		running.moveCursorTo(t, store.FocusServices, serviceKey("api", "web"))
		running.app.store.TryBeginLoading("busy")

		// when
		running.onUI(t, running.app.registerService)

		// then
		time.Sleep(settleDelay)
		if calls := running.manager.registerCalls(); len(calls) != 0 {
			t.Fatalf("register calls = %v, want none", calls)
		}
	})
}

func TestFilteredPanesRender(t *testing.T) {
	t.Run("Namespaces 창의 필터는 이름이 맞는 네임스페이스만 남긴다", func(t *testing.T) {
		// given
		running := startLoadedApp(t)
		running.typeText("2")
		running.waitFocus(t, store.FocusNamespaces)

		// when
		running.applyFilter(t, store.FocusNamespaces, "def")

		// then
		keys := uiValue(t, running, func() []string { return running.app.pane(store.FocusNamespaces).keys })
		if len(keys) != 1 || keys[0] != "default" {
			t.Fatalf("namespace keys = %v, want [default]", keys)
		}
	})

	t.Run("Services 창의 필터는 이름이 맞는 서비스만 남긴다", func(t *testing.T) {
		// given
		running := servicesApp(t)

		// when
		running.applyFilter(t, store.FocusServices, "website")

		// then
		keys := uiValue(t, running, func() []string { return running.app.pane(store.FocusServices).keys })
		if len(keys) != 1 || keys[0] != serviceKey("api", "website") {
			t.Fatalf("service keys = %v, want [api/website]", keys)
		}
	})
}

func TestEmptyDetails(t *testing.T) {
	t.Run("네임스페이스가 하나도 없는 컨텍스트에서 Namespaces 상세는 컨텍스트 정보를 보여 준다", func(t *testing.T) {
		// given
		lonely := &fakeKube{current: "lonely", resources: map[string]kube.ContextResources{"lonely": {Namespaces: map[string][]kube.Service{}}}}
		running := startLoadedApp(t, withKube(lonely))
		running.typeText("2")
		running.waitFocus(t, store.FocusNamespaces)

		// when
		running.typeText("i")

		// then
		eventually(t, "details open", func() bool { return running.modalName(t) == detailsPage })
		text := strings.Join(running.screenLines(t), "\n")
		if !containsAll(text, "Context", "lonely", "Namespaces", "Discovery", "ok") {
			t.Fatalf("details lack the context:\n%s", text)
		}
	})

	t.Run("컨텍스트가 없으면 Namespaces 상세 대신 안내를 보여 준다", func(t *testing.T) {
		// given
		running := startTestApp(t)
		running.typeText("2")
		running.waitFocus(t, store.FocusNamespaces)

		// when
		running.typeText("i")

		// then
		running.waitStatus(t, "Nothing under the cursor")
	})
}

func TestHistoryWithoutEvents(t *testing.T) {
	t.Run("기록이 비어 있으면 F2는 안내 문구를 보여 준다", func(t *testing.T) {
		// given
		running := startTestApp(t)

		// when
		running.press(tcell.KeyF2)

		// then
		eventually(t, "history open", func() bool { return running.modalName(t) == historyPage })
		if text := strings.Join(running.screenLines(t), "\n"); !strings.Contains(text, "No events yet.") {
			t.Fatalf("history lacks the empty notice:\n%s", text)
		}
	})
}

func TestDeleteTunnelGuards(t *testing.T) {
	t.Run("커서 아래 터널이 없으면 d 키는 알리기만 한다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, nil)

		// when
		running.typeText("d")

		// then
		running.waitStatus(t, "No tunnel under the cursor")
		if got := running.manager.unregistered(); len(got) != 0 {
			t.Fatalf("unregistered = %v, want none", got)
		}
	})

	t.Run("다른 작업이 진행 중이면 터널을 지우지 않는다", func(t *testing.T) {
		// given
		running := tunnelsApp(t, []dns.DNSTunnel{tunnel("dev", "api", "web")})
		running.app.store.TryBeginLoading("busy")

		// when
		running.onUI(t, running.app.deleteTunnel)

		// then
		time.Sleep(settleDelay)
		if got := running.manager.unregistered(); len(got) != 0 {
			t.Fatalf("unregistered = %v, want none", got)
		}
	})

	t.Run("지우기가 실패하면 오류로 알리고 터널을 목록에 남긴다", func(t *testing.T) {
		// given
		manager := &fakeManager{tunnels: []dns.DNSTunnel{tunnel("dev", "api", "web")}, unregisterErr: errors.New("port busy")}
		running := tunnelsApp(t, manager.tunnels, withManager(manager))

		// when
		running.typeText("d")

		// then
		running.waitStatus(t, "Removing http://web.api failed: port busy")
		if level := running.app.store.GetState().Status.Level; level != store.LevelError {
			t.Fatalf("level = %v, want error", level)
		}
		if got := uiValue(t, running, func() []string { return running.app.pane(store.FocusTunnels).keys }); len(got) != 1 {
			t.Fatalf("tunnel keys = %v, want the tunnel kept", got)
		}
	})
}
