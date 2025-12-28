package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleTunnelDeletion() {
	selectedRow, _ := a.dnsView.GetSelection()
	if selectedRow <= 0 {
		a.SetMessage("Please select a port forward")
		return
	}

	entries := a.manager.GetAllDNSTunnels()
	if len(entries) == 0 {
		a.SetMessage("No port forwards to delete")
		return
	}

	entryIndex := selectedRow - 1
	if entryIndex < 0 || entryIndex >= len(entries) {
		a.SetMessage("Invalid port forward selection")
		return
	}

	entry := entries[entryIndex]

	go func() {
		if err := a.manager.UnregisterDNSTunnel(entry.DNSURL); err != nil {
			a.app.QueueUpdateDraw(func() {
				a.SetMessage(fmt.Sprintf("Failed to stop port forward: %v", err))
			})
		} else {
			a.app.QueueUpdateDraw(func() {
				a.UpdateDNSView()
				a.SetMessage(fmt.Sprintf("Port forward stopped: %s", entry.DNSURL))
			})
		}
	}()
}

func (a *App) RenderTunnelView() *tview.Table {
	dnsView := tview.NewTable()
	dnsView.SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0).
		SetTitle(" Port Forwarding ").
		SetBorder(true)
	a.ApplyViewStyles(dnsView)

	dnsView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.contextList)
			return nil
		case tcell.KeyBacktab:
			a.app.SetFocus(a.mainView)
			return nil
		case tcell.KeyDelete:
			a.handleTunnelDeletion()
			return nil
		}
		return event
	})

	dnsView.SetFocusFunc(func() {
		dnsView.SetBorderColor(tcell.ColorGreen)
		a.UpdateHelpForFocus()
	})

	dnsView.SetBlurFunc(func() {
		dnsView.SetBorderColor(tcell.ColorWhite)
	})

	return dnsView
}
