package tui

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) handleServiceSelection() {
	state := a.store.GetState()
	if state.IsLoading {
		return
	}

	selectedNamespace := a.GetSelectedNamespace()
	if selectedNamespace == "" {
		a.store.SetMessage("Please select a namespace first")
		return
	}

	services := a.GetServices()
	if len(services) == 0 {
		a.store.SetMessage("No services found in selected namespace")
		return
	}

	selectedRow, _ := a.mainView.GetSelection()
	if selectedRow <= 0 {
		a.store.SetMessage("Please select a service (use arrow keys to navigate, then press Enter)")
		return
	}

	serviceIndex := selectedRow - 1
	if serviceIndex < 0 || serviceIndex >= len(services) {
		a.store.SetMessage("Invalid service selection")
		return
	}

	svc := services[serviceIndex]
	contextName := a.GetSelectedContext()

	currentFocus := state.Focus

	go func() {
		a.store.SetLoading(true)
		defer func() {
			a.store.SetLoading(false)

			if currentFocus != "" {
				go a.store.SetFocus(currentFocus)
			}
		}()

		if err := a.manager.RegisterDNSTunnel(contextName, svc.Name, svc.Namespace); err != nil {
			a.store.SetMessage(fmt.Sprintf("Port forwarding failed: %v", err))
		} else {
			a.app.QueueUpdateDraw(func() {
				a.UpdateDNSView()
			})
			a.store.SetMessage(fmt.Sprintf("Port forwarding started: %s.%s", svc.Name, svc.Namespace))
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

	mainView.SetInputCapture(a.handleServiceInput)
	mainView.SetFocusFunc(a.onServiceFocus)
	mainView.SetBlurFunc(a.onServiceBlur)

	var prevState store.State
	var mu sync.Mutex
	a.store.Subscribe(func(s store.State) {
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(prevState.Services, s.Services) {
			a.app.QueueUpdateDraw(func() {
				a.UpdateMainView()
			})
		}
		prevState = s
	})

	return mainView
}

func (a *App) handleServiceInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		go a.store.SetFocus(store.FocusTunnels)
		return nil
	case tcell.KeyBacktab:
		go a.store.SetFocus(store.FocusNamespaces)
		return nil
	case tcell.KeyEnter:
		a.handleServiceSelection()
		return nil
	}
	return event
}

func (a *App) onServiceFocus() {
	a.mainView.SetBorderColor(tcell.ColorGreen)
}

func (a *App) onServiceBlur() {
	a.mainView.SetBorderColor(tcell.ColorWhite)
}
