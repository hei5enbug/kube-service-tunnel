package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// matchFilter reports whether any field contains the query, ignoring case. An empty query matches everything.
func matchFilter(query string, fields ...string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

// filterRows returns a new slice with the matching rows and never modifies the input.
func filterRows[T any](rows []T, keep func(T) bool) []T {
	kept := make([]T, 0, len(rows))
	for _, row := range rows {
		if keep(row) {
			kept = append(kept, row)
		}
	}
	return kept
}

func (a *App) newFilterInput() *tview.InputField {
	input := tview.NewInputField()
	input.SetChangedFunc(a.onFilterChanged)
	input.SetDoneFunc(a.onFilterDone)
	input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyF1 {
			a.showHelp()
			return nil
		}
		return event
	})
	return input
}

func (a *App) openFilter(p *pane) {
	a.filterPane = p.area
	a.filterInput.SetLabel(fmt.Sprintf(" Filter %s /", paneLabels[p.area]))
	a.filterInput.SetText(p.filter)
	a.statusRow.Clear().AddItem(a.filterInput, 0, 1, false)
	a.app.SetFocus(a.filterInput)
	a.applyFocusBorders()
	a.renderHints()
}

func (a *App) closeFilter() {
	a.filterPane = ""
	a.statusRow.Clear().AddItem(a.status, 0, 1, false)
	a.renderStatus()
}

func (a *App) onFilterChanged(text string) {
	if a.filterPane == "" {
		return
	}
	p := a.pane(a.filterPane)
	if p.filter == text {
		return
	}
	p.filter = text
	a.renderPane(p)
}

func (a *App) onFilterDone(key tcell.Key) {
	if a.filterPane == "" {
		return
	}
	p := a.pane(a.filterPane)
	switch key {
	case tcell.KeyEnter:
		a.closeFilter()
		a.focusPane(p.area)
	case tcell.KeyEscape:
		p.filter = ""
		a.renderPane(p)
		a.closeFilter()
		a.focusPane(p.area)
	case tcell.KeyTab:
		a.closeFilter()
		a.focusPane(neighborArea(p.area, 1))
	case tcell.KeyBacktab:
		a.closeFilter()
		a.focusPane(neighborArea(p.area, -1))
	}
}

func (a *App) escapePane(p *pane) {
	if p.filter != "" {
		p.filter = ""
		a.renderPane(p)
		return
	}
	a.store.ClearStatus()
	a.renderStatus()
}
