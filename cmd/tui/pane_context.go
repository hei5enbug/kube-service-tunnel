package tui

import (
	"slices"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
)

type contextRow struct {
	Name   string
	Err    error
	Active bool
}

func buildContextRows(state store.State) []contextRow {
	rows := make([]contextRow, 0, len(state.Contexts))
	for _, name := range state.Contexts {
		rows = append(rows, contextRow{
			Name:   name,
			Err:    state.Resources[name].Err,
			Active: name == state.SelectedContext,
		})
	}
	return rows
}

func (a *App) renderContexts() {
	p := a.pane(store.FocusContexts)
	state := a.store.GetState()
	all := buildContextRows(state)
	rows := filterRows(all, func(row contextRow) bool { return matchFilter(p.filter, row.Name) })
	a.contextRows = rows
	a.drawTable(p, a.contextView(p, rows, len(all), state.SelectedContext))
}

func (a *App) contextView(p *pane, rows []contextRow, total int, active string) tableView {
	failed := slices.ContainsFunc(rows, func(row contextRow) bool { return row.Err != nil })
	nameWidth := a.layout.innerWidth(p.area) - 2
	if failed {
		nameWidth -= 2
	}
	view := tableView{
		title:  paneTitle(paneHeading(p.area), p.filter, len(rows), total),
		empty:  emptyText(total, "No contexts"),
		active: active,
	}
	for _, row := range rows {
		cells := []viewCell{
			{text: marker(row.Active, "▶")},
			{text: fit(row.Name, nameWidth), expand: true},
		}
		if failed {
			cells = append(cells, viewCell{text: marker(row.Err != nil, "!")})
		}
		view.rows = append(view.rows, cells)
		view.keys = append(view.keys, row.Name)
	}
	return view
}

func (a *App) selectedContextRow() (contextRow, bool) {
	index, ok := a.pane(store.FocusContexts).selectedIndex()
	if !ok || index >= len(a.contextRows) {
		return contextRow{}, false
	}
	return a.contextRows[index], true
}

func (a *App) activateContext() {
	row, ok := a.selectedContextRow()
	if !ok {
		return
	}
	if row.Name != a.store.GetState().SelectedContext {
		a.store.ActivateContext(row.Name)
		a.resetPanes(store.FocusNamespaces, store.FocusServices)
		a.renderResourcePanes()
		a.renderChrome()
	}
	a.focusPane(store.FocusNamespaces)
}

// resetPanes clears the filters of the given panes and moves their cursors to the active or first row.
func (a *App) resetPanes(areas ...store.FocusArea) {
	for _, area := range areas {
		p := a.pane(area)
		p.filter = ""
		p.resetCursor()
	}
}
