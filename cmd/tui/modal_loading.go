package tui

import (
	"fmt"
	"sync"
	"time"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/rivo/tview"
)

const loadingFrameInterval = 80 * time.Millisecond

var loadingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (a *App) SetupLoadingSubscription() {
	var prevState store.State
	var mu sync.Mutex
	a.store.Subscribe(func(s store.State) {
		mu.Lock()
		defer mu.Unlock()
		if s.IsLoading != prevState.IsLoading {
			a.app.QueueUpdateDraw(func() {
				if s.IsLoading {
					a.showLoadingModal()
				} else {
					a.hideLoadingModal()
				}
			})
		}
		prevState = s
	})
}

func (a *App) showLoadingModal() {
	if a.pages.HasPage("loading") {
		return
	}

	content := tview.NewTextView()
	content.SetTextAlign(tview.AlignCenter).
		SetDynamicColors(true).
		SetBackgroundColor(backgroundColor)

	contentBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(content, 3, 1, false).
		AddItem(nil, 0, 1, false)

	contentBox.SetBorder(true).
		SetBorderColor(systemColor).
		SetBackgroundColor(backgroundColor)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(contentBox, 7, 1, true).
			AddItem(nil, 0, 1, false), 30, 1, true).
		AddItem(nil, 0, 1, false)

	a.pages.AddPage("loading", modal, true, true)

	stop := make(chan struct{})
	a.loadingStop = stop
	go a.animateLoading(content, colorToHex(systemColor), stop)
}

func (a *App) animateLoading(content *tview.TextView, colorHex string, stop <-chan struct{}) {
	ticker := time.NewTicker(loadingFrameInterval)
	defer ticker.Stop()

	for i := 0; ; i++ {
		frame := fmt.Sprintf("\n[%s]%s[%s] Loading...[-]", colorHex, loadingFrames[i%len(loadingFrames)], colorHex)
		select {
		case <-stop:
			return
		default:
		}
		a.app.QueueUpdateDraw(func() { content.SetText(frame) })

		select {
		case <-stop:
			return
		case <-ticker.C:
		}
	}
}

func (a *App) hideLoadingModal() {
	if a.loadingStop != nil {
		close(a.loadingStop)
		a.loadingStop = nil
	}

	if a.pages.HasPage("loading") {
		a.pages.RemovePage("loading")

		state := a.store.GetState()
		target := a.getWidgetForFocus(state.Focus)
		a.app.SetFocus(target)
	}
}
