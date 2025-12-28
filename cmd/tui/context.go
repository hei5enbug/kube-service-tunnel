package tui

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) RenderContextView() *tview.Table {
	contextList := tview.NewTable()
	contextList.SetBorders(false).
		SetSelectable(true, false).
		SetTitle(" Context ").
		SetBorder(true)
	a.ApplyViewStyles(contextList)

	contextList.SetSelectedFunc(a.onContextSelected)
	contextList.SetInputCapture(a.handleContextInput)
	contextList.SetFocusFunc(a.onContextFocus)
	contextList.SetBlurFunc(a.onContextBlur)

	var prevState store.State
	var mu sync.Mutex
	a.store.Subscribe(func(s store.State) {
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(prevState.Contexts, s.Contexts) || prevState.SelectedContext != s.SelectedContext {
			a.app.QueueUpdateDraw(func() {
				a.UpdateContextList()
			})
		}
		prevState = s
	})

	return contextList
}

func (a *App) onContextSelected(row, column int) {
	go func() {
		state := a.store.GetState()
		if state.IsLoading {
			return
		}

		contexts := a.GetContexts()
		if row < 0 || row >= len(contexts) {
			return
		}
		contextName := contexts[row].Name

		if err := a.SetSelectedContext(contextName); err != nil {
			a.store.SetMessage(fmt.Sprintf("Error selecting context: %v", err))
		} else {
			a.store.SetMessage(fmt.Sprintf("Context selected: %s", contextName))
		}
	}()
}

func (a *App) handleContextInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		go a.store.SetFocus(store.FocusNamespaces)
		return nil
	case tcell.KeyBacktab:
		go a.store.SetFocus(store.FocusTunnels)
		return nil
	case tcell.KeyEnter:
		if event.Modifiers() == tcell.ModShift {
			a.handleRegisterAllServices()
			return nil
		}
	case tcell.KeyCtrlP:
		a.handleRegisterAllServices()
		return nil
	}
	return event
}

func (a *App) handleRegisterAllServices() {
	go func() {
		state := a.store.GetState()
		if state.SelectedContext == "" {
			return
		}
		contextName := state.SelectedContext

		if state.IsLoading {
			return
		}

		resourceMap := state.ResourceMap[contextName]
		var allServices []kube.Service
		for _, services := range resourceMap {
			allServices = append(allServices, services...)
		}

		a.store.SetLoading(true)
		defer a.store.SetLoading(false)

		if err := a.manager.RegisterAllByContext(contextName, allServices); err != nil {
			a.store.SetMessage(fmt.Sprintf("Failed to register all services: %v", err))
		} else {
			a.app.QueueUpdateDraw(func() {
				a.UpdateDNSView()
			})
			a.store.SetMessage(fmt.Sprintf("All services registered for context: %s", contextName))
		}
	}()
}

func (a *App) onContextFocus() {
	a.contextList.SetBorderColor(tcell.ColorGreen)
}

func (a *App) onContextBlur() {
	a.contextList.SetBorderColor(tcell.ColorWhite)
}
