package tui

import (
	"github.com/rivo/tview"
)

func (a *App) RenderMessageView() *tview.TextView {
	messageView := tview.NewTextView()
	messageView.SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(false).
		SetBorder(true).
		SetTitle(" Message ")
	a.ApplyViewStyles(messageView)

	messageView.SetText("")

	return messageView
}
