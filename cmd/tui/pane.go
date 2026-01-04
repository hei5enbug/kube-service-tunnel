package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Empty-state texts use one line per table row, short enough for the narrow left column.
const noMatchesText = "No matches.\nEsc clears."

var paneOrder = []store.FocusArea{
	store.FocusContexts,
	store.FocusNamespaces,
	store.FocusServices,
	store.FocusTunnels,
}

var paneLabels = map[store.FocusArea]string{
	store.FocusContexts:   "Context",
	store.FocusNamespaces: "Namespace",
	store.FocusServices:   "Services",
	store.FocusTunnels:    "Tunnels",
}

// paneHeading is the numbered name used in titles and the compact tab strip, such as "3 Services".
func paneHeading(area store.FocusArea) string {
	return fmt.Sprintf("%d %s", slices.Index(paneOrder, area)+1, paneLabels[area])
}

type cellKind int

const (
	cellNormal cellKind = iota
	cellDim
	cellHeader
)

// viewCell holds already truncated and escaped text.
type viewCell struct {
	text   string
	kind   cellKind
	expand bool
}

type tableView struct {
	title  string
	header []viewCell
	rows   [][]viewCell
	keys   []string
	empty  string
	active string
}

// pane is one table with its filter and the identity keys of the rows it renders.
type pane struct {
	area        store.FocusArea
	table       *tview.Table
	filter      string
	keys        []string
	offset      int
	cursorKey   string
	cursorIndex int
	hasCursor   bool
}

func (a *App) newPane(area store.FocusArea, fixedRows int) *pane {
	p := &pane{area: area, table: tview.NewTable()}
	p.table.SetBorders(false).
		SetSelectable(true, false).
		SetFixed(fixedRows, 0).
		SetEvaluateAllRows(true).
		SetBorder(true).
		SetTitleAlign(tview.AlignLeft)
	p.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey { return a.handleTableKey(p, event) })
	p.table.SetFocusFunc(func() { a.onPaneFocus(p) })
	return p
}

func (a *App) pane(area store.FocusArea) *pane {
	for _, p := range a.panes {
		if p.area == area {
			return p
		}
	}
	return a.panes[0]
}

func (a *App) focusedPane() *pane {
	return a.pane(a.store.GetState().Focus)
}

func neighborArea(area store.FocusArea, step int) store.FocusArea {
	index := slices.Index(paneOrder, area)
	return paneOrder[((index+step)%len(paneOrder)+len(paneOrder))%len(paneOrder)]
}

// resetCursor makes the next render place the cursor on the active row, or the first row.
func (p *pane) resetCursor() {
	p.hasCursor = false
}

func (p *pane) selectedIndex() (int, bool) {
	row, _ := p.table.GetSelection()
	index := row - p.offset
	return index, index >= 0 && index < len(p.keys)
}

func (p *pane) rememberCursor() {
	if len(p.keys) == 0 {
		return
	}
	row, _ := p.table.GetSelection()
	index := clamp(row-p.offset, 0, len(p.keys)-1)
	p.cursorKey, p.cursorIndex, p.hasCursor = p.keys[index], index, true
}

// renameKey makes the next render keep the cursor on a row whose identity key changed from old to renamed.
func (p *pane) renameKey(old, renamed string) {
	index := slices.Index(p.keys, old)
	if index < 0 {
		return
	}
	keys := slices.Clone(p.keys)
	keys[index] = renamed
	p.keys = keys
}

func (p *pane) cursorTarget(active string) int {
	if p.hasCursor {
		if index := slices.Index(p.keys, p.cursorKey); index >= 0 {
			return index
		}
		return min(p.cursorIndex, len(p.keys)-1)
	}
	return max(slices.Index(p.keys, active), 0)
}

func (a *App) drawTable(p *pane, view tableView) {
	p.rememberCursor()
	p.table.Clear()
	p.table.SetTitle(view.title)
	if len(view.rows) == 0 {
		p.keys, p.offset = nil, 0
		for row, line := range strings.Split(view.empty, "\n") {
			p.table.SetCell(row, 0, a.newCell(viewCell{text: line, kind: cellDim}).SetSelectable(false))
		}
		return
	}
	p.offset = 0
	if view.header != nil {
		for column, cell := range view.header {
			p.table.SetCell(0, column, a.newCell(cell).SetSelectable(false))
		}
		p.offset = 1
	}
	for index, cells := range view.rows {
		for column, cell := range cells {
			p.table.SetCell(p.offset+index, column, a.newCell(cell))
		}
	}
	p.keys = view.keys
	index := p.cursorTarget(view.active)
	p.table.Select(p.offset+index, 0)
	p.cursorKey, p.cursorIndex, p.hasCursor = p.keys[index], index, true
}

func (a *App) newCell(cell viewCell) *tview.TableCell {
	style := a.theme.textStyle()
	switch cell.kind {
	case cellDim:
		style = a.theme.dimStyle()
	case cellHeader:
		style = a.theme.headerStyle()
	}
	tableCell := tview.NewTableCell(cell.text).SetStyle(style)
	if cell.expand {
		tableCell.SetExpansion(1)
	}
	return tableCell
}

func (a *App) renderPane(p *pane) {
	switch p.area {
	case store.FocusContexts:
		a.renderContexts()
	case store.FocusNamespaces:
		a.renderNamespaces()
	case store.FocusServices:
		a.renderServices()
	default:
		a.renderTunnels()
	}
}

func paneTitle(base, query string, matched, total int) string {
	title := " " + tview.Escape(base)
	if query != "" {
		title += fmt.Sprintf(" /%s (%d/%d)", tview.Escape(query), matched, total)
	}
	return title + " "
}

func emptyText(total int, base string) string {
	if total == 0 {
		return base
	}
	return noMatchesText
}

func marker(on bool, symbol string) string {
	if on {
		return symbol
	}
	return " "
}

// column describes one flexible table column in priority order: natural is the widest content and minimum the
// width reserved before lower-priority columns grow.
type column struct {
	natural int
	minimum int
}

// allocateWidths shares available cells among columns. Each column first gets its minimum (or natural width when
// smaller), then columns grow to their natural width in priority order.
func allocateWidths(available int, columns []column) []int {
	widths := make([]int, len(columns))
	remaining := available
	for i, c := range columns {
		widths[i] = min(c.natural, c.minimum)
		remaining -= widths[i]
	}
	for i := len(columns) - 1; i >= 0 && remaining < 0; i-- {
		cut := min(widths[i]-1, -remaining)
		if cut > 0 {
			widths[i] -= cut
			remaining += cut
		}
	}
	for i, c := range columns {
		grow := min(c.natural-widths[i], max(remaining, 0))
		widths[i] += grow
		remaining -= grow
	}
	return widths
}
