package tui

import (
	"time"

	"github.com/rivo/tview"
)

const (
	loadingPage          = "loading"
	loadingFrameInterval = 80 * time.Millisecond
	loadingWidth         = 64
	loadingHeight        = 5
	defaultLoadingLabel  = "Loading…"
)

var loadingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// loadingView is touched only on the UI goroutine, except stop, which the animator reads.
type loadingView struct {
	text  *tview.TextView
	label string
	frame int
	stop  chan struct{}
}

func (a *App) syncLoading() {
	state := a.store.GetState()
	switch {
	case state.IsLoading && a.loading == nil:
		a.showLoading(state.LoadingLabel)
	case state.IsLoading:
		a.loading.label = state.LoadingLabel
		a.drawLoadingFrame()
	case a.loading != nil:
		a.hideLoading()
	}
}

func (a *App) showLoading(label string) {
	text := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter)
	text.SetBorder(true)
	a.styleTextModal(text)
	text.SetBorderColor(a.theme.Border)
	a.loading = &loadingView{text: text, label: label, stop: make(chan struct{})}
	a.drawLoadingFrame()
	a.pages.AddPage(loadingPage, newCentered(text, loadingWidth, fixedHeight(loadingHeight)), true, true)
	a.app.SetFocus(text)
	a.renderHints()
	go a.animateLoading(a.loading.stop)
}

func (a *App) drawLoadingFrame() {
	view := a.loading
	label := view.label
	if label == "" {
		label = defaultLoadingLabel
	}
	frame := loadingFrames[view.frame%len(loadingFrames)]
	view.text.SetText("\n" + colorTag(a.theme.Info) + frame + colorTag(a.theme.Text) + " " + tview.Escape(label))
}

func (a *App) hideLoading() {
	close(a.loading.stop)
	a.loading = nil
	a.pages.RemovePage(loadingPage)
	a.restoreFocus()
	a.renderChrome()
}

func (a *App) restoreFocus() {
	switch {
	case a.modal != nil:
		a.app.SetFocus(a.modal.focus)
	case a.filterPane != "":
		a.app.SetFocus(a.filterInput)
	default:
		a.focusPane(a.store.GetState().Focus)
	}
}

// frameTicker returns the channel that advances the loading animation and the function that releases it.
type frameTicker func() (ticks <-chan time.Time, stop func())

func newFrameTicker() (<-chan time.Time, func()) {
	ticker := time.NewTicker(loadingFrameInterval)
	return ticker.C, ticker.Stop
}

// animateLoading runs on its own goroutine, so it may queue UI updates.
func (a *App) animateLoading(stop <-chan struct{}) {
	ticks, stopTicks := a.frameTicker()
	defer stopTicks()
	for {
		select {
		case <-stop:
			return
		case <-a.ctx.Done():
			return
		case <-ticks:
		}
		a.app.QueueUpdateDraw(func() {
			if a.loading == nil || a.loading.stop != stop {
				return
			}
			a.loading.frame++
			a.drawLoadingFrame()
		})
	}
}
