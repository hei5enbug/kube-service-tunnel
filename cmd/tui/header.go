package tui

import (
	"github.com/rivo/tview"
)

func (a *App) RenderHeader() *tview.Flex {
	header := tview.NewFlex().SetDirection(tview.FlexRow)
	a.ApplyViewStyles(header)

	titleText := "Kubernetes Service Tunnel"
	title := tview.NewTextView().
		SetText(titleText).
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true)
	a.ApplyViewStyles(title)
	title.SetTextColor(systemColor)

	contentFlex := tview.NewFlex().
		AddItem(a.contextList, 0, 1, true).
		AddItem(a.helpView, 0, 4, false)
	a.ApplyViewStyles(contentFlex)

	header.AddItem(title, 1, 0, false)
	header.AddItem(a.messageView, 3, 0, false)
	header.AddItem(contentFlex, 0, 1, true)

	return header
}
