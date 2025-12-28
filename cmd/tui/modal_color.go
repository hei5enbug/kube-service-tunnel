package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func (a *App) showColorInputModal(title string, callback func(string)) {
	inputField := tview.NewInputField().
		SetLabel("Color: ").
		SetFieldWidth(20)

	closeModalAndUpdate := func(colorName string) {
		a.pages.RemovePage("modal")
		a.pages.SwitchToPage("main")
		callback(colorName)
	}

	closeModal := func() {
		a.pages.RemovePage("modal")
		a.pages.SwitchToPage("main")
	}

	inputField.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEnter {
			colorName := inputField.GetText()
			closeModalAndUpdate(colorName)
		} else if key == tcell.KeyEscape {
			closeModal()
		}
	})

	contentFlex := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(tview.NewTextView().
			SetText(fmt.Sprintf("Enter %s\n(e.g., black, white, blue, #000000)", title)).
			SetTextAlign(tview.AlignCenter).
			SetDynamicColors(true), 0, 1, false).
		AddItem(inputField, 0, 1, true).
		AddItem(tview.NewFlex().
			AddItem(nil, 0, 1, false).
			AddItem(tview.NewButton("OK").SetSelectedFunc(func() {
				colorName := inputField.GetText()
				closeModalAndUpdate(colorName)
			}), 0, 1, true).
			AddItem(tview.NewButton("Cancel").SetSelectedFunc(func() {
				closeModal()
			}), 0, 1, true).
			AddItem(nil, 0, 1, false), 0, 1, false)

	contentFlex.SetBorder(true).SetTitle(" " + title + " ")
	contentFlex.SetBackgroundColor(backgroundColor)

	modal := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(contentFlex, 0, 2, true).
			AddItem(nil, 0, 1, false), 0, 2, true).
		AddItem(nil, 0, 1, false)

	a.pages.AddPage("modal", modal, true, true)
	a.app.SetFocus(inputField)
}

func (a *App) changeBackgroundColor(colorName string) {
	newColor := parseColor(colorName)
	if newColor == tcell.ColorDefault && colorName != "" {
		a.SetMessage(fmt.Sprintf("Invalid color: %s", colorName))
		return
	}
	if newColor == tcell.ColorDefault {
		newColor = tcell.NewRGBColor(0, 0, 0)
	}

	backgroundColor = newColor
	tview.Styles.PrimitiveBackgroundColor = backgroundColor
	tview.Styles.ContrastBackgroundColor = backgroundColor
	tview.Styles.MoreContrastBackgroundColor = backgroundColor

	a.UpdateAllColors()
	a.SetMessage(fmt.Sprintf("Background color changed to: %s", colorName))
}

func (a *App) changeTextColor(colorName string) {
	newColor := parseColor(colorName)
	if newColor == tcell.ColorDefault && colorName != "" {
		a.SetMessage(fmt.Sprintf("Invalid color: %s", colorName))
		return
	}
	if newColor == tcell.ColorDefault {
		newColor = tcell.ColorWhite
	}

	textColor = newColor
	tview.Styles.PrimaryTextColor = textColor
	tview.Styles.SecondaryTextColor = textColor

	a.UpdateAllColors()
	a.SetMessage(fmt.Sprintf("Text color changed to: %s", colorName))
}
