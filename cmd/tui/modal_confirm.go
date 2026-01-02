package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	confirmPage         = "confirm"
	confirmTitle        = " Register all "
	confirmMaxWidth     = 76
	confirmSideSpace    = 2
	confirmVerticalRows = 6
)

// showConfirm opens the bulk confirmation. Cancel has the initial focus; y registers, n and Esc cancel.
func (a *App) showConfirm(plan bulkPlan) {
	decided := false
	decide := func(register bool) {
		if decided {
			return
		}
		decided = true
		a.closeModal()
		if register {
			a.startBulk(plan)
		}
	}
	text := tview.Escape(confirmText(plan))
	dialog, buttons := a.newConfirmDialog(text, decide)
	heightFor := func(width int) int {
		return wrappedHeight(text, width-borderCells-2*confirmSideSpace) + confirmVerticalRows
	}
	a.openModal(confirmPage, scopeConfirm, newCentered(dialog, confirmMaxWidth, heightFor), buttons)
}

func (a *App) newConfirmDialog(text string, decide func(register bool)) (*tview.Frame, *tview.Form) {
	t := a.theme
	message := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter).SetText(text)
	message.SetBackgroundColor(t.Background)
	message.SetTextColor(t.Text)

	buttons := tview.NewForm().
		SetButtonsAlign(tview.AlignCenter).
		SetButtonStyle(t.buttonStyle()).
		SetButtonActivatedStyle(t.selectedStyle()).
		AddButton("Cancel", func() { decide(false) }).
		AddButton("Register", func() { decide(true) }).
		SetCancelFunc(func() { decide(false) })
	buttons.SetBorderPadding(0, 0, 0, 0).SetBackgroundColor(t.Background)
	for i := range buttons.GetButtonCount() {
		buttons.GetButton(i).SetInputCapture(arrowsMoveBetweenButtons)
	}
	buttons.SetFocus(0)

	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(message, 0, 1, false).
		AddItem(tview.NewBox().SetBackgroundColor(t.Background), 1, 0, false).
		AddItem(buttons, 1, 0, false)
	dialog := tview.NewFrame(content).SetBorders(1, 1, 0, 0, confirmSideSpace, confirmSideSpace)
	dialog.SetBorder(true).SetTitle(confirmTitle).SetTitleColor(t.Title).SetBorderColor(t.FocusBorder).
		SetBackgroundColor(t.Background)
	dialog.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if scrollsMessage(event.Key()) {
			message.InputHandler()(event, func(tview.Primitive) {})
			return nil
		}
		if event.Key() != tcell.KeyRune {
			return event
		}
		switch event.Rune() {
		case 'y', 'Y':
			decide(true)
		case 'n', 'N':
			decide(false)
		default:
			return event
		}
		return nil
	})
	return dialog, buttons
}

// scrollsMessage reports keys that scroll the confirmation text, which can be taller than a small terminal.
func scrollsMessage(key tcell.Key) bool {
	switch key {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		return true
	}
	return false
}

func arrowsMoveBetweenButtons(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyRight:
		return tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone)
	case tcell.KeyLeft:
		return tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone)
	}
	return event
}
