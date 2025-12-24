package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleTunnelDeletion(dnsView *tview.Table) {
	defer func() {
		if r := recover(); r != nil {
			a.setMessage(fmt.Sprintf("Error deleting port forward: %v", r))
		}
	}()

	selectedRow, _ := dnsView.GetSelection()
	if selectedRow <= 0 {
		a.setMessage("Please select a port forward")
		return
	}

	entries := a.manager.GetAllDNSTunnels()
	if len(entries) == 0 {
		a.setMessage("No port forwards to delete")
		return
	}

	entryIndex := selectedRow - 1
	if entryIndex < 0 || entryIndex >= len(entries) {
		a.setMessage("Invalid port forward selection")
		return
	}

	entry := entries[entryIndex]
	if err := a.manager.UnregisterDNSTunnel(entry.DNSURL); err != nil {
		a.setMessage(fmt.Sprintf("Failed to stop port forward: %v", err))
	} else {
		a.updateDNSView()
		a.setMessage(fmt.Sprintf("Port forward stopped: %s", entry.DNSURL))
	}
}

func renderDNSView(a *App) *tview.Table {
	dnsView := tview.NewTable()
	dnsView.SetBorders(false).
		SetSelectable(true, false).
		SetFixed(1, 0).
		SetTitle("Port Forwarding").
		SetBorder(true)
	dnsView.SetBackgroundColor(backgroundColor)

	dnsView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.contextList)
			return nil
		case tcell.KeyBacktab:
			a.app.SetFocus(a.mainView)
			return nil
		case tcell.KeyDelete:
			a.handleTunnelDeletion(dnsView)
			return nil
		}
		return event
	})

	dnsView.SetFocusFunc(func() {
		a.updateHelpForFocus()
	})

	return dnsView
}
