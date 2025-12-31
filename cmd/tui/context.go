package tui

import (
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
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
	if a.store.GetState().IsLoading {
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
}

func (a *App) handleContextInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		a.store.SetFocus(store.FocusNamespaces)
		return nil
	case tcell.KeyBacktab:
		a.store.SetFocus(store.FocusTunnels)
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
	state := a.store.GetState()
	contextName := state.SelectedContext
	if contextName == "" || state.IsLoading {
		return
	}

	namespaces := sortedNamespaces(state.ResourceMap[contextName])
	if len(namespaces) == 0 {
		a.store.SetMessage(fmt.Sprintf("No namespaces with services found for context %s", contextName))
		return
	}

	if !a.store.TryBeginLoading() {
		return
	}

	go func() {
		defer a.store.SetLoading(false)

		result, err := a.manager.RegisterAllByContext(a.ctx, contextName, namespaces)
		a.app.QueueUpdateDraw(a.UpdateDNSView)
		a.store.SetMessage(describeBulkResult(contextName, result, err))
	}()
}

func sortedNamespaces(namespaceMap map[string][]kube.Service) []string {
	namespaces := make([]string, 0, len(namespaceMap))
	for namespace := range namespaceMap {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

func describeBulkResult(contextName string, result dns.BulkRegistrationResult, err error) string {
	if err != nil {
		return fmt.Sprintf("Failed to register services: %v", err)
	}

	registered := len(result.Registered)
	if len(result.Failures) == 0 {
		return fmt.Sprintf("Registered %d services for context %s", registered, contextName)
	}

	first := result.Failures[0]
	return fmt.Sprintf("Registered %d services for context %s; %d failed (first: %s/%s: %s)",
		registered, contextName, len(result.Failures), first.Namespace, first.Service, first.Reason)
}

func (a *App) onContextFocus() {
	a.contextList.SetBorderColor(tcell.ColorGreen)
}

func (a *App) onContextBlur() {
	a.contextList.SetBorderColor(tcell.ColorWhite)
}
