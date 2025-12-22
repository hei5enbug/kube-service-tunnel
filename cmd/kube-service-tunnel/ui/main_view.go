package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleServiceSelection(mainView *tview.Table) {
	selectedNamespace := a.manager.GetSelectedNamespace()
	if selectedNamespace == "" {
		a.setErrorCell("Please select a namespace first")
		return
	}

	services := a.manager.GetServices()
	if len(services) == 0 {
		a.setErrorCell("No services found in selected namespace")
		return
	}

	selectedRow, _ := mainView.GetSelection()
	if selectedRow <= 0 {
		a.setErrorCell("Please select a service (use arrow keys to navigate, then press Enter)")
		return
	}

	serviceIndex := selectedRow - 1
	if serviceIndex < 0 || serviceIndex >= len(services) {
		a.setErrorCell("Invalid service selection")
		return
	}

	svc := services[serviceIndex]
	if err := a.manager.RegisterServicePortForward(svc.Name, svc.Namespace); err != nil {
		a.setErrorCell(fmt.Sprintf("Port forwarding failed: %v", err))
	} else {
		a.updateDNSView()
		a.setPlaceholderCell(fmt.Sprintf("Port forwarding started: %s.%s", svc.Name, svc.Namespace))
	}
}


func renderMainView(a *App) *tview.Table {
	mainView := tview.NewTable()
	mainView.SetBorders(false).
		SetSelectable(true, false).
		SetTitle("Services").
		SetBorder(true)
	mainView.SetBackgroundColor(backgroundColor)

	mainView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.dnsView)
			return nil
		case tcell.KeyBacktab:
			a.app.SetFocus(a.sidebar)
			return nil
		case tcell.KeyEnter:
			a.handleServiceSelection(mainView)
			return nil
		}
		return event
	})
	
	mainView.SetFocusFunc(func() {
		a.updateHelpForFocus()
	})

	return mainView
}

