package tui

import (
	"slices"
	"testing"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
)

func TestPaneLookup(t *testing.T) {
	t.Run("알 수 없는 영역을 찾으면 첫 창을 돌려준다", func(t *testing.T) {
		// given
		running := startTestApp(t)

		// when
		found := uiValue(t, running, func() store.FocusArea { return running.app.pane("nowhere").area })

		// then
		if found != store.FocusContexts {
			t.Fatalf("area = %q, want contexts", found)
		}
	})
}

func TestRenameKey(t *testing.T) {
	t.Run("키가 있으면 그 행의 키만 새 이름으로 바꾼다", func(t *testing.T) {
		// given
		p := &pane{keys: []string{"admin.api", "web.api"}}
		original := p.keys

		// when
		p.renameKey("web.api", "site.api")

		// then
		if !slices.Equal(p.keys, []string{"admin.api", "site.api"}) {
			t.Fatalf("keys = %v", p.keys)
		}
		if original[1] != "web.api" {
			t.Fatalf("the previous key slice was modified: %v", original)
		}
	})

	t.Run("키가 없으면 아무것도 바꾸지 않는다", func(t *testing.T) {
		// given
		p := &pane{keys: []string{"admin.api"}}

		// when
		p.renameKey("missing.api", "site.api")

		// then
		if !slices.Equal(p.keys, []string{"admin.api"}) {
			t.Fatalf("keys = %v", p.keys)
		}
	})
}

func TestSelectedRowsWithoutRows(t *testing.T) {
	t.Run("행이 없으면 선택된 행이 없다고 답한다", func(t *testing.T) {
		// given
		running := startTestApp(t)

		// when
		results := uiValue(t, running, func() [4]bool {
			_, contextOK := running.app.selectedContextRow()
			_, namespaceOK := running.app.selectedNamespaceRow()
			_, serviceOK := running.app.selectedServiceRow()
			_, tunnelOK := running.app.selectedTunnel()
			return [4]bool{contextOK, namespaceOK, serviceOK, tunnelOK}
		})

		// then
		if results != [4]bool{} {
			t.Fatalf("selected = %v, want none", results)
		}
	})
}

func TestTunnelView(t *testing.T) {
	rows := []dns.DNSTunnel{tunnel("dev", "api", "web"), tunnel("prod", "shop", "cart")}

	t.Run("파드 열을 켜면 머리글과 각 행에 파드 이름이 들어간다", func(t *testing.T) {
		// given
		withPod := true

		// when
		view := tunnelView(rows, 80, withPod)

		// then
		if len(view.header) != 3 || len(view.rows[0]) != 3 || view.rows[0][2].text != rows[0].Pod {
			t.Fatalf("header = %v, first row = %v", view.header, view.rows[0])
		}
	})

	t.Run("파드 열을 끄면 URL과 컨텍스트 열만 남는다", func(t *testing.T) {
		// given
		withPod := false

		// when
		view := tunnelView(rows, 80, withPod)

		// then
		if len(view.header) != 2 || len(view.rows[1]) != 2 || !slices.Equal(view.keys, []string{"web.api", "cart.shop"}) {
			t.Fatalf("header = %v, rows = %v, keys = %v", view.header, view.rows, view.keys)
		}
	})
}

func TestEmptyPortList(t *testing.T) {
	t.Run("포트가 없는 서비스는 none으로 보여 준다", func(t *testing.T) {
		// given
		var ports []kube.ServicePort

		// when
		text := portLines(ports)

		// then
		if text != "none" {
			t.Fatalf("portLines = %q", text)
		}
	})
}

func TestButtonStyle(t *testing.T) {
	t.Run("흑백 테마의 버튼은 기본 스타일이다", func(t *testing.T) {
		// given
		mono := themes[slices.IndexFunc(themes, func(theme Theme) bool { return theme.Monochrome })]

		// when
		style := mono.buttonStyle()

		// then
		if style != tcell.StyleDefault {
			t.Fatalf("style = %v, want default", style)
		}
	})

	t.Run("색 테마의 버튼은 글자색과 테두리색 배경을 쓴다", func(t *testing.T) {
		// given
		dark := themes[0]

		// when
		foreground, background, _ := dark.buttonStyle().Decompose()

		// then
		if foreground != dark.Text || background != dark.Border {
			t.Fatalf("foreground = %v, background = %v", foreground, background)
		}
	})
}
