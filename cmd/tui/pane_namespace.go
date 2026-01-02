package tui

import (
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/byoungmin/kube-service-tunnel/internal/kube"
)

const (
	noNamespacesText      = "No namespaces\nwith services."
	discoveryFailedText   = "Discovery\nfailed.\nr: retry\ni: details"
	namespaceMarkerSymbol = "▶"
)

type namespaceRow struct {
	Name     string
	Context  string
	Active   bool
	Services []kube.Service
}

func buildNamespaceRows(state store.State) []namespaceRow {
	resources := state.Resources[state.SelectedContext]
	rows := make([]namespaceRow, 0, len(state.Namespaces))
	for _, name := range state.Namespaces {
		rows = append(rows, namespaceRow{
			Name:     name,
			Context:  state.SelectedContext,
			Active:   name == state.SelectedNamespace,
			Services: resources.Namespaces[name],
		})
	}
	return rows
}

func (a *App) renderNamespaces() {
	p := a.pane(store.FocusNamespaces)
	state := a.store.GetState()
	all := buildNamespaceRows(state)
	rows := filterRows(all, func(row namespaceRow) bool { return matchFilter(p.filter, row.Name) })
	a.namespaceRows = rows

	empty := noNamespacesText
	if state.Resources[state.SelectedContext].Err != nil {
		empty = discoveryFailedText
	}
	view := tableView{
		title:  paneTitle(paneHeading(p.area), p.filter, len(rows), len(all)),
		empty:  emptyText(len(all), empty),
		active: state.SelectedNamespace,
	}
	nameWidth := a.layout.innerWidth(p.area) - 2
	for _, row := range rows {
		view.rows = append(view.rows, []viewCell{
			{text: marker(row.Active, namespaceMarkerSymbol)},
			{text: fit(row.Name, nameWidth), expand: true},
		})
		view.keys = append(view.keys, row.Name)
	}
	a.drawTable(p, view)
}

func (a *App) selectedNamespaceRow() (namespaceRow, bool) {
	index, ok := a.pane(store.FocusNamespaces).selectedIndex()
	if !ok || index >= len(a.namespaceRows) {
		return namespaceRow{}, false
	}
	return a.namespaceRows[index], true
}

func (a *App) activateNamespace() {
	row, ok := a.selectedNamespaceRow()
	if !ok {
		return
	}
	if row.Name != a.store.GetState().SelectedNamespace {
		a.store.ActivateNamespace(row.Name)
		a.resetPanes(store.FocusServices)
		a.renderResourcePanes()
	}
	a.focusPane(store.FocusServices)
}

func registrableCount(services []kube.Service) int {
	count := 0
	for i := range services {
		if _, reason := kube.Eligibility(&services[i]); reason == "" {
			count++
		}
	}
	return count
}
