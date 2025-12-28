package tui

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) RenderNamespaceView() *tview.Table {
	namespaceView := tview.NewTable()
	namespaceView.SetBorders(false).
		SetSelectable(true, false).
		SetTitle(" Namespaces ").
		SetBorder(true)
	a.ApplyViewStyles(namespaceView)

	namespaceView.SetSelectedFunc(a.onNamespaceSelected)
	namespaceView.SetInputCapture(a.handleNamespaceInput)
	namespaceView.SetFocusFunc(a.onNamespaceFocus)
	namespaceView.SetBlurFunc(a.onNamespaceBlur)

	var prevState store.State
	var mu sync.Mutex
	a.store.Subscribe(func(s store.State) {
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(prevState.Namespaces, s.Namespaces) || prevState.SelectedNamespace != s.SelectedNamespace {
			a.app.QueueUpdateDraw(func() {
				a.UpdateNamespaceView()
			})
		}
		prevState = s
	})

	return namespaceView
}

func (a *App) onNamespaceSelected(row, column int) {
	go func() {
		state := a.store.GetState()
		if state.IsLoading {
			return
		}

		namespaces := a.GetNamespaces()
		if row >= 0 && row < len(namespaces) {
			namespace := namespaces[row]

			if err := a.SetSelectedNamespace(namespace); err != nil {
				a.store.SetMessage(fmt.Sprintf("Error selecting namespace: %v", err))
			} else {
				a.store.SetMessage(fmt.Sprintf("Namespace selected: %s", namespace))
			}
		}
	}()
}

func (a *App) handleNamespaceInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		go a.store.SetFocus(store.FocusServices)
		return nil
	case tcell.KeyBacktab:
		go a.store.SetFocus(store.FocusContexts)
		return nil
	}
	return event
}

func (a *App) onNamespaceFocus() {
	a.namespaceView.SetBorderColor(tcell.ColorGreen)
}

func (a *App) onNamespaceBlur() {
	a.namespaceView.SetBorderColor(tcell.ColorWhite)
}
