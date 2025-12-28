package tui

import (
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
	"github.com/rivo/tview"
)

func newTableCell(text string) *tview.TableCell {
	return tview.NewTableCell(text).
		SetBackgroundColor(backgroundColor).
		SetTextColor(textColor)
}

func findNamespaceIndex(namespaces []string, selectedNamespace string) int {
	for i, ns := range namespaces {
		if ns == selectedNamespace {
			return i
		}
	}
	return -1
}

func (a *App) ApplyViewStyles(view interface{}) {
	switch v := view.(type) {
	case *tview.TextView:
		v.SetBackgroundColor(backgroundColor)
		if v.HasFocus() {
			v.SetBorderColor(focusedBorderColor)
		} else {
			v.SetBorderColor(textColor)
		}
		v.SetTitleColor(textColor)
		v.SetTextColor(textColor)
	case *tview.Table:
		v.SetBackgroundColor(backgroundColor)
		if v.HasFocus() {
			v.SetBorderColor(focusedBorderColor)
		} else {
			v.SetBorderColor(textColor)
		}
		v.SetTitleColor(textColor)
	case *tview.Flex:
		v.SetBackgroundColor(backgroundColor)
	}
}

func (a *App) UpdateNamespaceView() {
	if a.namespaceView == nil {
		return
	}
	a.namespaceView.Clear()
	a.ApplyViewStyles(a.namespaceView)

	namespaces := a.GetNamespaces()
	selectedNamespace := a.GetSelectedNamespace()
	selectedIndex := findNamespaceIndex(namespaces, selectedNamespace)

	for i, ns := range namespaces {
		text := ns
		if ns == selectedNamespace {
			text += " (current)"
		}

		cell := tview.NewTableCell(text).
			SetExpansion(1).
			SetTextColor(textColor).
			SetBackgroundColor(backgroundColor)

		a.namespaceView.SetCell(i, 0, cell)
	}
	if selectedIndex >= 0 && selectedIndex < len(namespaces) {
		a.namespaceView.Select(selectedIndex, 0)
	} else if len(namespaces) > 0 {
		a.namespaceView.Select(0, 0)
	}

	if len(namespaces) == 0 {
		a.SetMessage("No namespaces with services found")
	} else if selectedNamespace == "" {
		a.SetMessage("Select a namespace to view services")
	}
}

func (a *App) InitMainView() {
	if a.mainView == nil {
		return
	}
	a.mainView.Clear()
	a.ApplyViewStyles(a.mainView)

	headerCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).
			SetSelectable(false).
			SetExpansion(expansion)
	}

	a.mainView.SetCell(0, 0, headerCell("Name", 2))
	a.mainView.SetCell(0, 1, headerCell("ClusterIP", 1))
	a.mainView.SetCell(0, 2, headerCell("Type", 1))
}

func (a *App) UpdateMainView() {
	a.InitMainView()

	if a.mainView == nil {
		return
	}

	dataCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).SetExpansion(expansion)
	}

	services := a.GetServices()
	for i, svc := range services {
		row := i + 1
		a.mainView.SetCell(row, 0, dataCell(svc.Name, 2))
		a.mainView.SetCell(row, 1, dataCell(svc.ClusterIP, 1))
		a.mainView.SetCell(row, 2, dataCell(svc.Type, 1))
	}
}

func (a *App) UpdateDNSView() {
	if a.dnsView == nil {
		return
	}
	a.dnsView.Clear()
	a.ApplyViewStyles(a.dnsView)

	headerCell := func(text string, expansion int) *tview.TableCell {
		return newTableCell(text).
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

func (a *App) UpdateHeader() {
	if a.header == nil {
		return
	}
	a.ApplyViewStyles(a.header)

	titleText := "Kubernetes Service Tunnel"
	if titleView, ok := a.header.GetItem(0).(*tview.TextView); ok {
		titleView.SetText(titleText)
		a.ApplyViewStyles(titleView)
		titleView.SetTextColor(systemColor)
	}
}

func (a *App) UpdateContextList() {
	if a.contextList == nil {
		return
	}
	a.contextList.Clear()
	a.ApplyViewStyles(a.contextList)

	contexts := a.GetContexts()
	selectedContext := a.GetSelectedContext()
	for i, ctx := range contexts {
		text := ctx.Name
		if ctx.Name == selectedContext {
			text += " (current)"
		}

		cell := tview.NewTableCell(text).
			SetExpansion(1).
			SetTextColor(textColor).
			SetBackgroundColor(backgroundColor)

		a.contextList.SetCell(i, 0, cell)

		if ctx.Name == selectedContext {
			a.contextList.Select(i, 0)
		}
	}
}

func (a *App) UpdateHelpView() {
	if a.helpView == nil {
		return
	}
	a.ApplyViewStyles(a.helpView)
	if a.app != nil {
		a.UpdateHelpForFocus(a.store.GetState().Focus)
	}
}

func (a *App) UpdateMessageView() {
	if a.messageView == nil {
		return
	}
	a.ApplyViewStyles(a.messageView)
	a.messageView.SetBorderColor(systemColor)
	a.messageView.SetTitleColor(systemColor)
	a.messageView.SetTextColor(systemColor)

	currentText := a.messageView.GetText(true)
	a.messageView.SetText(currentText)
}

func (a *App) UpdateAllColors() {
	if a.root != nil {
		a.root.SetBackgroundColor(backgroundColor)
	}
	a.UpdateHeader()
	a.UpdateContextList()
	a.UpdateNamespaceView()
	a.UpdateMainView()
	a.UpdateDNSView()
	a.UpdateHelpView()
	a.UpdateMessageView()
}

func (a *App) SetMessage(message string) {
	if a.messageView != nil {
		if message != "" {
			a.messageView.SetText(message)
		} else {
			a.messageView.SetText("")
		}
	}
}

func (a *App) UpdateHelpForFocus(focus store.FocusArea) {
	var focusType string

	switch focus {
	case store.FocusContexts:
		focusType = "context"
	case store.FocusNamespaces:
		focusType = "namespace"
	case store.FocusServices:
		focusType = "services"
	case store.FocusTunnels:
		focusType = "tunnel"
	default:
		focusType = "default"
	}

	UpdateHelpText(a.helpView, focusType)
}

func (a *App) GetContexts() []kube.Context {
	return a.store.GetState().Contexts
}

func (a *App) GetSelectedContext() string {
	return a.store.GetState().SelectedContext
}

func (a *App) GetNamespaces() []string {
	return a.store.GetState().Namespaces
}

func (a *App) GetSelectedNamespace() string {
	return a.store.GetState().SelectedNamespace
}

func (a *App) GetServices() []kube.Service {
	return a.store.GetState().Services
}

func (a *App) SetSelectedContext(contextName string) error {
	a.store.SetSelectedContextWithResources(contextName)
	return nil
}

func (a *App) SetSelectedNamespace(namespace string) error {
	a.store.SetSelectedNamespace(namespace)
	return nil
}
