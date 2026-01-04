package tui

import (
	"fmt"
	"slices"

	"github.com/byoungmin/kube-service-tunnel/cmd/dns"
	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const (
	editPage       = "edit"
	editTitle      = " Edit tunnel "
	editMaxWidth   = 64
	editLabelWidth = 12
	editSideSpace  = 1
	// editFixedRows counts the two border rows, the service, the two fields, the hint and the buttons.
	editFixedRows   = 7
	editButtonGap   = 2
	buttonPadding   = 4
	editHintText    = "Empty fields use the default."
	editErrorPrefix = "✗ "
)

// editDialog holds the widgets of the edit dialog. It is touched only on the UI goroutine.
type editDialog struct {
	tunnel   dns.DNSTunnel
	hostname *tview.InputField
	port     *tview.InputField
	message  *tview.TextView
	cancel   *tview.Button
	save     *tview.Button
	done     bool
}

func (a *App) editTunnel() {
	tunnel, ok := a.selectedTunnel()
	if !ok {
		a.store.Notify(store.LevelInfo, "No tunnel under the cursor")
		return
	}
	d := &editDialog{tunnel: tunnel}
	frame := a.newEditFrame(d)
	a.openModal(editPage, scopeEdit, newCentered(frame, editMaxWidth, d.heightFor), d.hostname)
}

func (a *App) newEditFrame(d *editDialog) *tview.Frame {
	t := a.theme
	hostname, port := editPrefill(d.tunnel)
	d.hostname = a.newEditField("Hostname", hostname, defaultTunnelHostname(d.tunnel)+" (default)")
	d.port = a.newEditField("Local port", port, fmt.Sprintf("auto · now %d", d.tunnel.LocalPort))
	d.port.SetAcceptanceFunc(acceptPortText)
	d.message = a.newEditText("", t.Error).SetWrap(true).SetWordWrap(true)
	d.cancel = a.newEditButton("Cancel")
	d.save = a.newEditButton("Save")

	content := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.newEditText(padRight("Service", editLabelWidth)+tunnelLabel(d.tunnel), t.Text), 1, 0, false).
		AddItem(d.hostname, 1, 0, true).
		AddItem(d.port, 1, 0, false).
		AddItem(a.newEditText(editHintText, t.Dim), 1, 0, false).
		AddItem(d.message, 0, 1, false).
		AddItem(a.newEditButtons(d), 1, 0, false)
	frame := tview.NewFrame(content).SetBorders(0, 0, 0, 0, editSideSpace, editSideSpace)
	frame.SetBorder(true).SetTitle(editTitle).SetTitleColor(t.Title).SetBorderColor(t.FocusBorder).
		SetBackgroundColor(t.Background)
	frame.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey { return a.handleEditKey(d, event) })
	return frame
}

// heightFor gives the error line as many rows as its wrapped text needs, and one blank row when it is empty.
func (d *editDialog) heightFor(width int) int {
	return editFixedRows + wrappedHeight(d.message.GetText(false), width-borderCells-2*editSideSpace)
}

func (a *App) newEditField(label, text, placeholder string) *tview.InputField {
	t := a.theme
	field := tview.NewInputField().SetLabel(label).SetLabelWidth(editLabelWidth).SetText(text).SetPlaceholder(placeholder)
	field.SetLabelStyle(t.textStyle()).SetFieldStyle(t.fieldStyle()).SetPlaceholderStyle(t.placeholderStyle())
	field.SetBackgroundColor(t.Background)
	field.SetFocusFunc(func() { field.SetLabelStyle(t.focusedLabelStyle()) })
	field.SetBlurFunc(func() { field.SetLabelStyle(t.textStyle()) })
	return field
}

// newEditText shows text literally in one color.
func (a *App) newEditText(text string, color tcell.Color) *tview.TextView {
	view := tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetText(tview.Escape(text))
	view.SetTextColor(color)
	view.SetBackgroundColor(a.theme.Background)
	return view
}

// newEditButton has no selected func because handleEditKey handles Enter for every dialog item.
func (a *App) newEditButton(label string) *tview.Button {
	button := tview.NewButton(label)
	button.SetStyle(a.theme.buttonStyle()).SetActivatedStyle(a.theme.selectedStyle())
	return button
}

func (a *App) newEditButtons(d *editDialog) *tview.Flex {
	spacer := func() *tview.Box { return tview.NewBox().SetBackgroundColor(a.theme.Background) }
	return tview.NewFlex().
		AddItem(spacer(), 0, 1, false).
		AddItem(d.cancel, displayWidth(d.cancel.GetLabel())+buttonPadding, 0, false).
		AddItem(spacer(), editButtonGap, 0, false).
		AddItem(d.save, displayWidth(d.save.GetLabel())+buttonPadding, 0, false).
		AddItem(spacer(), 0, 1, false)
}

// handleEditKey moves the focus, saves or cancels. Other keys reach the focused field or button.
func (a *App) handleEditKey(d *editDialog, event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyTab:
		a.moveEditFocus(d, 1)
	case tcell.KeyBacktab:
		a.moveEditFocus(d, -1)
	case tcell.KeyEscape:
		a.cancelEdit(d)
	case tcell.KeyEnter:
		a.enterEdit(d)
	default:
		return event
	}
	return nil
}

func (a *App) enterEdit(d *editDialog) {
	switch a.app.GetFocus() {
	case d.hostname:
		a.focusEditItem(d.port)
	case d.cancel:
		a.cancelEdit(d)
	default:
		a.saveEdit(d)
	}
}

func (a *App) moveEditFocus(d *editDialog, step int) {
	items := []tview.Primitive{d.hostname, d.port, d.cancel, d.save}
	index := max(slices.Index(items, a.app.GetFocus()), 0)
	a.focusEditItem(items[((index+step)%len(items)+len(items))%len(items)])
}

// focusEditItem also records the item as the dialog focus, which closing the loading dialog restores.
func (a *App) focusEditItem(item tview.Primitive) {
	if a.modal != nil {
		a.modal.focus = item
	}
	a.app.SetFocus(item)
}

func (a *App) cancelEdit(d *editDialog) {
	if d.done {
		return
	}
	d.done = true
	a.closeModal()
}

// saveEdit validates before it closes the dialog. An invalid field keeps the dialog open with the error shown.
func (a *App) saveEdit(d *editDialog) {
	if d.done {
		return
	}
	request, invalid, err := parseEdit(d.tunnel, d.hostname.GetText(), d.port.GetText())
	if err != nil {
		d.message.SetText(tview.Escape(editErrorPrefix + err.Error()))
		a.focusEditItem(d.field(invalid))
		return
	}
	d.done = true
	a.closeModal()
	a.startUpdate(request)
}

func (d *editDialog) field(field editField) *tview.InputField {
	if field == editPortField {
		return d.port
	}
	return d.hostname
}
