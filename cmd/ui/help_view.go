package ui

import (
	"github.com/rivo/tview"
)

func renderHelpView(a *App) *tview.TextView {
	helpView := tview.NewTextView()
	helpView.SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(false).
		SetBorder(true).
		SetTitle("Help")
	helpView.SetBackgroundColor(backgroundColor)
	
	updateHelpText(helpView, "context")
	
	return helpView
}

func updateHelpText(helpView *tview.TextView, focusType string) {
	var baseText string
	
	switch focusType {
	case "context":
		baseText = getColorTag(textColor) + "Tab: Next (Namespaces)\nEnter: Select context\nCtrl+P: Register all services\nCtrl+C: Exit"
	case "namespace":
		baseText = getColorTag(textColor) + "Tab: Next (Services)\nShift+Tab: Previous (Context)\nEnter: Select namespace\nCtrl+C: Exit"
	case "services":
		baseText = getColorTag(textColor) + "Tab: Next (Tunnel)\nShift+Tab: Previous (Namespaces)\nEnter: Register & port forward service\nCtrl+C: Exit"
	case "tunnel":
		baseText = getColorTag(textColor) + "Tab: Next (Context)\nShift+Tab: Previous (Services)\nDelete: Delete port forward\nCtrl+C: Exit"
	default:
		baseText = getColorTag(textColor) + "Tab: Navigate\nEnter: Select\nCtrl+C: Exit"
	}
	
	helpView.SetText(baseText)
}

func (a *App) updateHelpForFocus() {
	focus := a.app.GetFocus()
	var focusType string
	
	switch focus {
	case a.contextList:
		focusType = "context"
	case a.sidebar:
		focusType = "namespace"
	case a.mainView:
		focusType = "services"
	case a.dnsView:
		focusType = "tunnel"
	default:
		focusType = "default"
	}
	
	updateHelpText(a.helpView, focusType)
}

