package tui

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) RenderContextView() *tview.Table {
	contextList := tview.NewTable()
	contextList.SetBorders(false).
		SetSelectable(true, false).
		SetTitle(" Context ").
		SetBorder(true)
	a.ApplyViewStyles(contextList)

	contextList.SetSelectedFunc(func(row, column int) {
		go func() {
			contexts := a.GetContexts()
			if row < 0 || row >= len(contexts) {
				return
			}
			contextName := contexts[row].Name

			a.app.QueueUpdateDraw(func() {
				a.SetMessage(fmt.Sprintf("Loading context: %s...", contextName))
			})

			err := a.SetSelectedContext(contextName)

			a.app.QueueUpdateDraw(func() {
				if err != nil {
					a.SetMessage(fmt.Sprintf("Error loading namespaces: %v", err))
				} else {
					a.InitMainView()
					a.SetMessage(fmt.Sprintf("Context selected: %s", contextName))
					a.UpdateNamespaceView()
					a.UpdateContextList()
					a.UpdateMainView()
				}
			})
		}()
	})

	contextList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			a.app.SetFocus(a.namespaceView)
			return nil
		case tcell.KeyCtrlP:
			selectedIndex, _ := contextList.GetSelection()
			go func() {
				c, err := a.kubeAdapter.ListContexts(a.ctx)
				if err != nil || selectedIndex < 0 || selectedIndex >= len(c) {
					return
				}
				contextName := c[selectedIndex].Name

				a.app.QueueUpdateDraw(func() {
					if a.isLoading {
						return
					}

					a.isLoading = true
					loadingDone := make(chan bool)
					loadingDots := []string{".", "..", "...", "...."}
					idx := 0

					go func() {
						ticker := time.NewTicker(500 * time.Millisecond)
						defer ticker.Stop()
						for {
							select {
							case <-loadingDone:
								return
							case <-ticker.C:
								loadingText := "Loading" + loadingDots[idx%len(loadingDots)]
								a.app.QueueUpdateDraw(func() {
									a.SetMessage(loadingText)
								})
								idx++
							case <-a.ctx.Done():
								return
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
								a.SetMessage(fmt.Sprintf("Failed to register all services: %v", err))
							})
						} else {
							a.app.QueueUpdateDraw(func() {
								a.UpdateDNSView()
								a.SetMessage(fmt.Sprintf("All services registered for context: %s", contextName))
							})
						}
					}()
				})
			}()
			return nil
		}
		return event
	})

	contextList.SetFocusFunc(func() {
		contextList.SetBorderColor(tcell.ColorGreen)
		a.UpdateHelpForFocus()
	})

	contextList.SetBlurFunc(func() {
		contextList.SetBorderColor(tcell.ColorWhite)
	})

	return contextList
}
