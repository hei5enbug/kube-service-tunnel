package tui

import (
	"sync"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
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

	messageView.SetBorderColor(systemColor)
	messageView.SetTitleColor(systemColor)
	messageView.SetTextColor(systemColor)
	messageView.SetText("")

	var prevState store.State
	var mu sync.Mutex
	a.store.Subscribe(func(s store.State) {
		mu.Lock()
		defer mu.Unlock()
		if s.Message != prevState.Message && s.Message != "" {
			a.app.QueueUpdateDraw(func() {
				a.SetMessage(s.Message)
			})
		}
		prevState = s
	})

	return messageView
}
