package ui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func renderSidebar(a *App) *tview.List {
	sidebar := tview.NewList()
	sidebar.ShowSecondaryText(false).
		SetHighlightFullLine(true).
		SetTitle("Namespaces").
		SetBorder(true)
	sidebar.SetBackgroundColor(backgroundColor)

	sidebar.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		namespaces := a.manager.GetNamespaces()
		if index < len(namespaces) {
			if err := a.manager.SetSelectedNamespace(namespaces[index]); err != nil {
				a.setErrorCell(fmt.Sprintf("Error loading services: %v", err))
			} else {
				a.updateMainView()
				a.updateSidebar()
			}
		}
	})

	sidebar.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
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
	
	sidebar.SetFocusFunc(func() {
		a.updateHelpForFocus()
	})

	return sidebar
}

