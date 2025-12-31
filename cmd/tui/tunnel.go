package tui

import (
	"fmt"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleTunnelDeletion() {
	selectedRow, _ := a.dnsView.GetSelection()
	if selectedRow <= 0 {
		a.store.SetMessage("Please select a local DNS tunnel")
		return
	}

	entries := a.renderedTunnels
	if len(entries) == 0 {
		a.store.SetMessage("No local DNS tunnels to delete")
		return
	}

	entryIndex := selectedRow - 1
	if entryIndex < 0 || entryIndex >= len(entries) {
		a.store.SetMessage("Invalid local DNS tunnel selection")
		return
	}

	entry := entries[entryIndex]

	if !a.store.TryBeginLoading() {
		return
	}

	go func() {
		defer a.store.SetLoading(false)

		err := a.manager.UnregisterDNSTunnel(entry.DNSURL)
		a.app.QueueUpdateDraw(a.UpdateDNSView)
		if err != nil {
			a.store.SetMessage(fmt.Sprintf("Failed to stop local DNS tunnel: %v", err))
			return
		}
		a.store.SetMessage(fmt.Sprintf("Local DNS tunnel stopped: http://%s", entry.DNSURL))
	}()
}

func (a *App) RenderTunnelView() *tview.Table {
	dnsView := tview.NewTable()
	dnsView.SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0).
		SetTitle(" Local DNS Tunnels ").
		SetBorder(true)
	a.ApplyViewStyles(dnsView)

	dnsView.SetInputCapture(a.handleTunnelInput)
	dnsView.SetFocusFunc(a.onTunnelFocus)
	dnsView.SetBlurFunc(a.onTunnelBlur)

	return dnsView
}

func (a *App) handleTunnelInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		a.store.SetFocus(store.FocusContexts)
		return nil
	case tcell.KeyBacktab:
		a.store.SetFocus(store.FocusServices)
		return nil
	case tcell.KeyDelete:
		a.handleTunnelDeletion()
		return nil
	}
	return event
}

func (a *App) onTunnelFocus() {
	a.dnsView.SetBorderColor(tcell.ColorGreen)
}

func (a *App) onTunnelBlur() {
	a.dnsView.SetBorderColor(tcell.ColorWhite)
}
