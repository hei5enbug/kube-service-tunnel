package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleServiceSelection() {
	selectedNamespace := a.GetSelectedNamespace()
	if selectedNamespace == "" {
		a.SetMessage("Please select a namespace first")
		return
	}

	services := a.GetServices()
	if len(services) == 0 {
		a.SetMessage("No services found in selected namespace")
		return
	}

	selectedRow, _ := a.mainView.GetSelection()
	if selectedRow <= 0 {
		a.SetMessage("Please select a service (use arrow keys to navigate, then press Enter)")
		return
	}

	serviceIndex := selectedRow - 1
	if serviceIndex < 0 || serviceIndex >= len(services) {
		a.SetMessage("Invalid service selection")
		return
	}

	svc := services[serviceIndex]
	contextName := a.GetSelectedContext()

	go func() {
		if err := a.manager.RegisterDNSTunnel(contextName, svc.Name, svc.Namespace); err != nil {
			a.app.QueueUpdateDraw(func() {
				a.SetMessage(fmt.Sprintf("Port forwarding failed: %v", err))
			})
		} else {
			a.app.QueueUpdateDraw(func() {
				a.UpdateDNSView()
				a.SetMessage(fmt.Sprintf("Port forwarding started: %s.%s", svc.Name, svc.Namespace))
			})
		}
	}()
}

func (a *App) RenderServiceView() *tview.Table {
	mainView := tview.NewTable()
	mainView.SetBorders(false).
		SetSelectable(true, false).
		SetTitle(" Services ").
		SetBorder(true)
	a.ApplyViewStyles(mainView)

	mainView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.dnsView)
			return nil
		case tcell.KeyBacktab:
			a.app.SetFocus(a.namespaceView)
			return nil
		case tcell.KeyEnter:
			a.handleServiceSelection()
			return nil
		}
		return event
	})

	mainView.SetFocusFunc(func() {
		mainView.SetBorderColor(tcell.ColorGreen)
		a.UpdateHelpForFocus()
	})

	mainView.SetBlurFunc(func() {
		mainView.SetBorderColor(tcell.ColorWhite)
	})

	return mainView
}
