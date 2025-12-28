package tui

import (
	"fmt"

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

	namespaceView.SetSelectedFunc(func(row, column int) {
		namespaces := a.GetNamespaces()
		if row >= 0 && row < len(namespaces) {
			namespace := namespaces[row]

			go func() {
				a.app.QueueUpdateDraw(func() {
					a.SetMessage(fmt.Sprintf("Loading services in %s...", namespace))
				})

				err := a.SetSelectedNamespace(namespace)

				a.app.QueueUpdateDraw(func() {
					if err != nil {
						a.SetMessage(fmt.Sprintf("Error loading services: %v", err))
					} else {
						a.InitMainView()
						a.UpdateMainView()
						a.UpdateNamespaceView()
						a.SetMessage(fmt.Sprintf("Namespace selected: %s", namespace))
					}
				})
			}()
		}
	})

	namespaceView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.mainView)
			return nil
		case tcell.KeyBacktab:
			a.app.SetFocus(a.contextList)
			return nil
		}
		return event
	})

	namespaceView.SetFocusFunc(func() {
		namespaceView.SetBorderColor(tcell.ColorGreen)
		a.UpdateHelpForFocus()
	})

	namespaceView.SetBlurFunc(func() {
		namespaceView.SetBorderColor(tcell.ColorWhite)
	})

	return namespaceView
}
