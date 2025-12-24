package ui

import (
	"fmt"

	"github.com/rivo/tview"
)

func newTableCell(text string) *tview.TableCell {
	return tview.NewTableCell(text).SetBackgroundColor(backgroundColor)
}

func findNamespaceIndex(namespaces []string, selectedNamespace string) int {
	for i, ns := range namespaces {
		if ns == selectedNamespace {
			return i
		}
	}
	return -1
}

func (a *App) updateSidebar() {
	a.sidebar.Clear()
	namespaces := a.uiRenderer.GetNamespaces()
	selectedNamespace := a.uiRenderer.GetSelectedNamespace()
	selectedIndex := findNamespaceIndex(namespaces, selectedNamespace)

	for i, ns := range namespaces {
		text := ns
		if i == selectedIndex {
			text += " (current)"
		}
		namespace := ns
		a.sidebar.AddItem(text, "", 0, func() {
			if err := a.uiRenderer.SetSelectedNamespace(namespace); err != nil {
				a.setMessage(fmt.Sprintf("Error loading services: %v", err))
			} else {
				a.setMessage(fmt.Sprintf("Namespace selected: %s", namespace))
				a.updateMainView()
				a.updateDNSView()
			}
		})
	}
	if selectedIndex >= 0 && selectedIndex < len(namespaces) {
		a.sidebar.SetCurrentItem(selectedIndex)
	} else if len(namespaces) > 0 {
		a.sidebar.SetCurrentItem(0)
	}

	if len(namespaces) == 0 {
		a.setMessage("No namespaces with services found")
	} else if selectedNamespace == "" {
		a.setMessage("Select a namespace to view services")
	}
}

func (a *App) updateMainView() {
	a.mainView.Clear()

	headerCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).
			SetTextColor(textColor).
			SetSelectable(false).
			SetExpansion(expansion)
	}

	a.mainView.SetCell(0, 0, headerCell("Name", 2))
	a.mainView.SetCell(0, 1, headerCell("ClusterIP", 1))
	a.mainView.SetCell(0, 2, headerCell("Type", 1))

	dataCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).SetExpansion(expansion)
	}

	services := a.uiRenderer.GetServices()
	for i, svc := range services {
		row := i + 1
		a.mainView.SetCell(row, 0, dataCell(svc.Name, 2))
		a.mainView.SetCell(row, 1, dataCell(svc.ClusterIP, 1))
		a.mainView.SetCell(row, 2, dataCell(svc.Type, 1))
	}
}

func (a *App) setErrorCell(message string) {
	a.setMessage(message)
	a.updateMainView()
}

func (a *App) setPlaceholderCell(message string) {
	a.setMessage(message)
	a.updateMainView()
}

func (a *App) updateDNSView() {
	a.dnsView.Clear()

	headerCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).
			SetTextColor(textColor).
			SetSelectable(false).
			SetExpansion(expansion)
	}

	a.dnsView.SetCell(0, 0, headerCell("Context", 1))
	a.dnsView.SetCell(0, 1, headerCell("Namespace", 1))
	a.dnsView.SetCell(0, 2, headerCell("DNS URL", 2))

	entries := a.manager.GetAllDNSTunnels()

	if len(entries) == 0 {
		a.dnsView.SetCell(1, 0, newTableCell("No tunnel entries").SetTextColor(textColor).SetExpansion(1))
		return
	}

	dataCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).SetExpansion(expansion)
	}

	for i, entry := range entries {
		row := i + 1
		a.dnsView.SetCell(row, 0, dataCell(entry.Context, 1))
		a.dnsView.SetCell(row, 1, dataCell(entry.Namespace, 1))
		a.dnsView.SetCell(row, 2, dataCell(entry.DNSURL, 2))
	}
}
