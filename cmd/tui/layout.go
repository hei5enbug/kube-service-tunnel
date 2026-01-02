package tui

import (
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
)

const (
	minNormalWidth   = 80
	minNormalHeight  = 16
	chromeRows       = 3
	minLeftWidth     = 16
	maxLeftWidth     = 24
	minContextHeight = 3
	podMinPaneWidth  = 55
	borderCells      = 2
)

type layoutMode int

const (
	layoutNormal layoutMode = iota + 1
	layoutCompact
)

// layout holds the outer pane sizes for one terminal size. Compact mode shows only the focused pane.
type layout struct {
	mode          layoutMode
	width         int
	height        int
	left          int
	services      int
	tunnels       int
	contextHeight int
}

func computeLayout(width, height, contextCount int) layout {
	result := layout{mode: layoutCompact, width: width, height: height}
	if width < minNormalWidth || height < minNormalHeight {
		return result
	}
	result.mode = layoutNormal
	result.left = clamp(width/5, minLeftWidth, maxLeftWidth)
	rest := width - result.left
	result.services = (rest - borderCells) / 2
	result.tunnels = rest - result.services
	body := height - chromeRows
	result.contextHeight = clamp(contextCount+borderCells, minContextHeight, body/2)
	return result
}

func (l layout) paneWidth(area store.FocusArea) int {
	if l.mode != layoutNormal {
		return l.width
	}
	switch area {
	case store.FocusContexts, store.FocusNamespaces:
		return l.left
	case store.FocusServices:
		return l.services
	default:
		return l.tunnels
	}
}

func (l layout) innerWidth(area store.FocusArea) int {
	return max(l.paneWidth(area)-borderCells, 0)
}

func (a *App) beforeDraw(screen tcell.Screen) bool {
	a.screenRef.set(screen)
	width, height := screen.Size()
	a.applyLayout(width, height)
	a.applyFocusBorders()
	return false
}

// applyLayout runs on the UI goroutine during drawing, so it only mutates widgets.
func (a *App) applyLayout(width, height int) {
	next := computeLayout(width, height, len(a.store.GetState().Contexts))
	if next == a.layout {
		return
	}
	a.layout = next
	a.rebuildBody()
	a.renderResourcePanes()
	a.renderTunnels()
	a.renderChrome()
}

func (a *App) rebuildBody() {
	a.body.Clear()
	if a.layout.mode != layoutNormal {
		a.body.AddItem(a.focusedPane().table, 0, 1, false)
		return
	}
	a.left.Clear().
		AddItem(a.pane(store.FocusContexts).table, a.layout.contextHeight, 0, false).
		AddItem(a.pane(store.FocusNamespaces).table, 0, 1, false)
	a.body.
		AddItem(a.left, a.layout.left, 0, false).
		AddItem(a.pane(store.FocusServices).table, a.layout.services, 0, false).
		AddItem(a.pane(store.FocusTunnels).table, 0, 1, false)
}
