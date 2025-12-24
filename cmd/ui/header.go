package ui

import (
	"fmt"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/display"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func renderHeader(a *App) *tview.Flex {
	header := tview.NewFlex().SetDirection(tview.FlexRow)
	header.SetBackgroundColor(backgroundColor)

	titleText := getColorTag(textColor) + "Kubernetes Service Tunnel"
	title := tview.NewTextView().
		SetText(titleText).
		SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true)
	title.SetBackgroundColor(backgroundColor)

	contextList := tview.NewList()
	contextList.ShowSecondaryText(false).
		SetHighlightFullLine(true).
		SetBorder(true).
		SetTitle("Context")
	contextList.SetBackgroundColor(backgroundColor)

	a.contextList = contextList

	updateContextList := func() {
		contextList.Clear()
		contexts := a.uiRenderer.GetContexts()
		selectedContext := a.uiRenderer.GetSelectedContext()
		selectedIndex := display.FindContextIndex(contexts, selectedContext)
		for i, ctx := range contexts {
			text := ctx.Name
			if i == selectedIndex {
				text += " (current)"
			}
			contextList.AddItem(text, "", 0, nil)
		}
		if selectedIndex >= 0 && selectedIndex < len(contexts) {
			contextList.SetCurrentItem(selectedIndex)
		}
	}

	updateContextList()

	contextList.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		contexts := a.uiRenderer.GetContexts()
		if index < len(contexts) {
			contextName := contexts[index].Name
			if err := a.uiRenderer.SetSelectedContext(contextName); err != nil {
				a.setMessage(fmt.Sprintf("Error loading namespaces: %v", err))
			} else {
				a.setMessage(fmt.Sprintf("Context selected: %s", contextName))
				a.updateSidebar()
				updateContextList()
			}
		}
	})

	helpView := renderHelpView(a)
	a.helpView = helpView

	messageView := a.messageView
	if messageView == nil {
		messageView = renderMessageView(a)
		a.messageView = messageView
	}

	contentFlex := tview.NewFlex().
		AddItem(contextList, 0, 7, true).
		AddItem(helpView, 0, 30, false)
	contentFlex.SetBackgroundColor(backgroundColor)

	header.AddItem(title, 1, 0, false)
	header.AddItem(messageView, 3, 0, false)
	header.AddItem(contentFlex, 0, 1, true)

	contextList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.sidebar)
			return nil
		case tcell.KeyCtrlP:
			contexts := a.uiRenderer.GetContexts()
			selectedIndex := contextList.GetCurrentItem()
			if selectedIndex >= 0 && selectedIndex < len(contexts) {
				contextName := contexts[selectedIndex].Name

				if a.isLoading {
					return nil
				}

				a.isLoading = true
				loadingDone := make(chan bool)
				loadingDots := []string{".", "..", "...", "...."}
				index := 0

				go func() {
					ticker := time.NewTicker(500 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-loadingDone:
							return
						case <-ticker.C:
							loadingText := "Loading" + loadingDots[index%len(loadingDots)]
							a.app.QueueUpdateDraw(func() {
								a.setMessage(loadingText)
							})
							index++
						}
					}
				}()

				go func() {
					defer func() {
						close(loadingDone)
						a.app.QueueUpdateDraw(func() {
							a.isLoading = false
						})
					}()
					if err := a.manager.RegisterAllByContext(contextName); err != nil {
						a.app.QueueUpdateDraw(func() {
							a.setMessage(fmt.Sprintf("Failed to register all services: %v", err))
						})
					} else {
						a.app.QueueUpdateDraw(func() {
							a.updateDNSView()
							a.setMessage(fmt.Sprintf("All services registered for context: %s", contextName))
						})
					}
				}()
			}
			return nil
		}
		return event
	})

	contextList.SetFocusFunc(func() {
		a.updateHelpForFocus()
	})

	return header
}
