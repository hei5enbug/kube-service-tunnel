package tui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	modalMargin      = 2
	minModalWidth    = 30
	maxModalWidth    = 100
	modalChromeCells = 4
)

// centered draws its inner primitive at most maxWidth wide in the middle of its own area. heightFor gives the
// height the content needs at a given width.
type centered struct {
	*tview.Box
	inner     tview.Primitive
	maxWidth  int
	heightFor func(width int) int
}

func newCentered(inner tview.Primitive, maxWidth int, heightFor func(width int) int) *centered {
	return &centered{Box: tview.NewBox(), inner: inner, maxWidth: maxWidth, heightFor: heightFor}
}

func fixedHeight(height int) func(int) int {
	return func(int) int { return height }
}

func (c *centered) Draw(screen tcell.Screen) {
	x, y, width, height := c.GetRect()
	innerWidth := max(min(c.maxWidth, width-modalMargin), 1)
	innerHeight := max(min(c.heightFor(innerWidth), height-modalMargin), 1)
	c.inner.SetRect(x+(width-innerWidth)/2, y+(height-innerHeight)/2, innerWidth, innerHeight)
	c.inner.Draw(screen)
}

func (c *centered) Focus(delegate func(p tview.Primitive)) {
	delegate(c.inner)
}

func (c *centered) HasFocus() bool {
	return c.inner.HasFocus()
}

func (c *centered) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return c.inner.InputHandler()
}

// openModal tracks the one dialog that can be open at a time and the focus to restore when it closes.
type openModal struct {
	name      string
	scope     hintScope
	focus     tview.Primitive
	prevFocus tview.Primitive
}

func (a *App) openModal(name string, scope hintScope, page, focus tview.Primitive) {
	if a.modal != nil {
		a.closeModal()
	}
	a.modal = &openModal{name: name, scope: scope, focus: focus, prevFocus: a.app.GetFocus()}
	a.pages.AddPage(name, page, true, true)
	a.app.SetFocus(focus)
	a.renderHints()
}

func (a *App) closeModal() {
	modal := a.modal
	if modal == nil {
		return
	}
	a.modal = nil
	a.pages.RemovePage(modal.name)
	if modal.prevFocus != nil {
		a.app.SetFocus(modal.prevFocus)
	} else {
		a.focusPane(a.store.GetState().Focus)
	}
	a.renderHints()
}

// showTextModal opens a scrollable, read-only dialog. text must already be escaped.
func (a *App) showTextModal(name, title, text string, scrollToEnd bool) {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true).
		SetWrap(true).
		SetWordWrap(true).
		SetText(text)
	view.SetBorder(true).SetTitle(" "+title+" ").SetBorderPadding(0, 0, 1, 1)
	a.styleTextModal(view)
	view.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			a.closeModal()
		}
	})
	if scrollToEnd {
		view.ScrollToEnd()
	}
	width := clamp(taggedWidth(strings.Split(text, "\n"))+modalChromeCells, minModalWidth, maxModalWidth)
	heightFor := func(width int) int { return wrappedHeight(text, width-modalChromeCells) + modalMargin }
	a.openModal(name, scopeText, newCentered(view, width, heightFor), view)
}

func wrappedHeight(text string, width int) int {
	return max(len(tview.WordWrap(text, max(width, 1))), 1)
}

func taggedWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, tview.TaggedStringWidth(line))
	}
	return width
}
