package tui

import (
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
)

// handleGlobalKey runs first for every key. Ctrl+C always quits; everything else is ignored while loading.
func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		a.Quit()
		return nil
	}
	if a.store.GetState().IsLoading {
		return nil
	}
	return event
}

// handleTableKey is the input capture of every pane table. Keys it does not use reach tview's table navigation.
func (a *App) handleTableKey(p *pane, event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyRune {
		if a.handleTableRune(p, event.Rune()) {
			return nil
		}
		return event
	}
	if a.handleTableSpecialKey(p, event.Key()) {
		return nil
	}
	return event
}

func (a *App) handleTableSpecialKey(p *pane, key tcell.Key) bool {
	switch key {
	case tcell.KeyTab:
		a.focusPane(neighborArea(p.area, 1))
	case tcell.KeyBacktab:
		a.focusPane(neighborArea(p.area, -1))
	case tcell.KeyEscape:
		a.escapePane(p)
	case tcell.KeyEnter:
		a.enterPane(p)
	case tcell.KeyF1:
		a.showHelp()
	case tcell.KeyF2:
		a.showHistory()
	case tcell.KeyF4:
		a.nextTheme()
	case tcell.KeyCtrlP:
		return a.onlyIn(p, store.FocusContexts, a.confirmBulk)
	case tcell.KeyDelete, tcell.KeyBackspace, tcell.KeyBackspace2:
		return a.onlyIn(p, store.FocusTunnels, a.deleteTunnel)
	default:
		return false
	}
	return true
}

func (a *App) handleTableRune(p *pane, r rune) bool {
	switch r {
	case '1', '2', '3', '4':
		a.focusPane(paneOrder[r-'1'])
	case '/':
		a.openFilter(p)
	case 'i':
		a.showDetails(p)
	case 'r':
		a.refreshActiveContext()
	case '?':
		a.showHelp()
	case 'd':
		return a.onlyIn(p, store.FocusTunnels, a.deleteTunnel)
	case 'c':
		return a.onlyIn(p, store.FocusTunnels, a.copyTunnelURL)
	default:
		return false
	}
	return true
}

func (a *App) onlyIn(p *pane, area store.FocusArea, action func()) bool {
	if p.area != area {
		return false
	}
	action()
	return true
}

func (a *App) enterPane(p *pane) {
	switch p.area {
	case store.FocusContexts:
		a.activateContext()
	case store.FocusNamespaces:
		a.activateNamespace()
	case store.FocusServices:
		a.registerService()
	}
}

// focusPane moves keyboard focus on the UI goroutine; the table's focus func updates the store and the chrome.
func (a *App) focusPane(area store.FocusArea) {
	a.app.SetFocus(a.pane(area).table)
}

func (a *App) onPaneFocus(p *pane) {
	a.store.SetFocus(p.area)
	if a.layout.mode == layoutCompact {
		a.rebuildBody()
	}
	a.applyFocusBorders()
	a.renderChrome()
}
