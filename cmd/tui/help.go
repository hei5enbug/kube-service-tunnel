package tui

import (
	"github.com/rivo/tview"
)

func (a *App) RenderHelpView() *tview.TextView {
	helpView := tview.NewTextView()
	helpView.SetDynamicColors(true).
		SetWordWrap(true).
		SetScrollable(false).
		SetBorder(true).
		SetTitle(" Help ")
	a.ApplyViewStyles(helpView)

	UpdateHelpText(helpView, "context")

	return helpView
}

func UpdateHelpText(helpView *tview.TextView, focusType string) {
	var baseText string

	switch focusType {
	case "context":
		baseText = "Tab: Next (Namespaces)\nEnter: Select context\nCtrl+P: Register all services\nCtrl+B: Change background color\nCtrl+T: Change text color\nCtrl+C: Exit"
	case "namespace":
		baseText = "Tab: Next (Services)\nShift+Tab: Previous (Context)\nEnter: Select namespace\nCtrl+B: Change background color\nCtrl+T: Change text color\nCtrl+C: Exit"
	case "services":
		baseText = "Tab: Next (Tunnel)\nShift+Tab: Previous (Namespaces)\nEnter: Register & port forward service\nCtrl+B: Change background color\nCtrl+T: Change text color\nCtrl+C: Exit"
	case "tunnel":
		baseText = "Tab: Next (Context)\nShift+Tab: Previous (Services)\nDelete: Delete port forward\nCtrl+B: Change background color\nCtrl+T: Change text color\nCtrl+C: Exit"
	default:
		baseText = "Tab: Navigate\nEnter: Select\nCtrl+B: Change background color\nCtrl+T: Change text color\nCtrl+C: Exit"
	}

	helpView.SetText(baseText)
}
