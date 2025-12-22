package ui

import (
	"github.com/rivo/tview"
)

func renderMessageView(a *App) *tview.TextView {
	messageView := tview.NewTextView()
	messageView.SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(false).
		SetBorder(true).
		SetTitle("Message")
	messageView.SetBackgroundColor(backgroundColor)
	
	messageView.SetText("")
	
	return messageView
}

func (a *App) setMessage(message string) {
	if a.messageView != nil {
		if message != "" {
			a.messageView.SetText(getColorTag(textColor) + message)
		} else {
			a.messageView.SetText("")
		}
	}
}

func (a *App) clearMessage() {
	if a.messageView != nil {
		a.messageView.SetText("")
	}
}

