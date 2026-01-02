package tui

import (
	"fmt"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Theme maps semantic roles to colors. A monochrome theme uses terminal default colors, shows severity by icon
// and word only, and marks the selection with reverse video.
type Theme struct {
	Name               string
	Background         tcell.Color
	Text               tcell.Color
	Dim                tcell.Color
	Border             tcell.Color
	FocusBorder        tcell.Color
	Title              tcell.Color
	Header             tcell.Color
	SelectedBackground tcell.Color
	SelectedText       tcell.Color
	Info               tcell.Color
	Success            tcell.Color
	Warning            tcell.Color
	Error              tcell.Color
	Monochrome         bool
}

var themes = []Theme{
	{
		Name:               "Dark",
		Background:         tcell.NewHexColor(0x0d1117),
		Text:               tcell.NewHexColor(0xe6edf3),
		Dim:                tcell.NewHexColor(0x7d8590),
		Border:             tcell.NewHexColor(0x484f58),
		FocusBorder:        tcell.NewHexColor(0x58a6ff),
		Title:              tcell.NewHexColor(0xe6edf3),
		Header:             tcell.NewHexColor(0xa5b4c3),
		SelectedBackground: tcell.NewHexColor(0x1f4a7a),
		SelectedText:       tcell.NewHexColor(0xffffff),
		Info:               tcell.NewHexColor(0x58a6ff),
		Success:            tcell.NewHexColor(0x3fb950),
		Warning:            tcell.NewHexColor(0xd29922),
		Error:              tcell.NewHexColor(0xf85149),
	},
	{
		Name:               "Light",
		Background:         tcell.NewHexColor(0xffffff),
		Text:               tcell.NewHexColor(0x1f2328),
		Dim:                tcell.NewHexColor(0x6e7781),
		Border:             tcell.NewHexColor(0xafb8c1),
		FocusBorder:        tcell.NewHexColor(0x0969da),
		Title:              tcell.NewHexColor(0x1f2328),
		Header:             tcell.NewHexColor(0x57606a),
		SelectedBackground: tcell.NewHexColor(0xb6e3ff),
		SelectedText:       tcell.NewHexColor(0x1f2328),
		Info:               tcell.NewHexColor(0x0969da),
		Success:            tcell.NewHexColor(0x1a7f37),
		Warning:            tcell.NewHexColor(0x9a6700),
		Error:              tcell.NewHexColor(0xcf222e),
	},
	{
		Name:               "Mono",
		Background:         tcell.ColorDefault,
		Text:               tcell.ColorDefault,
		Dim:                tcell.ColorDefault,
		Border:             tcell.ColorDefault,
		FocusBorder:        tcell.ColorDefault,
		Title:              tcell.ColorDefault,
		Header:             tcell.ColorDefault,
		SelectedBackground: tcell.ColorDefault,
		SelectedText:       tcell.ColorDefault,
		Info:               tcell.ColorDefault,
		Success:            tcell.ColorDefault,
		Warning:            tcell.ColorDefault,
		Error:              tcell.ColorDefault,
		Monochrome:         true,
	},
}

func (t Theme) style(foreground tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(foreground).Background(t.Background)
}

func (t Theme) textStyle() tcell.Style {
	return t.style(t.Text)
}

func (t Theme) dimStyle() tcell.Style {
	return t.style(t.Dim).Dim(t.Monochrome)
}

func (t Theme) headerStyle() tcell.Style {
	return t.style(t.Header).Bold(true)
}

func (t Theme) selectedStyle() tcell.Style {
	if t.Monochrome {
		return tcell.StyleDefault.Reverse(true)
	}
	return tcell.StyleDefault.Foreground(t.SelectedText).Background(t.SelectedBackground)
}

func (t Theme) buttonStyle() tcell.Style {
	if t.Monochrome {
		return tcell.StyleDefault
	}
	return tcell.StyleDefault.Foreground(t.Text).Background(t.Border)
}

func (t Theme) levelColor(level store.Level) tcell.Color {
	switch level {
	case store.LevelSuccess:
		return t.Success
	case store.LevelWarning:
		return t.Warning
	case store.LevelError:
		return t.Error
	default:
		return t.Info
	}
}

// colorTag returns a tview color tag; the terminal default color resets to the view's own text color.
func colorTag(color tcell.Color) string {
	if color == tcell.ColorDefault {
		return "[-]"
	}
	return fmt.Sprintf("[#%06x]", color.Hex())
}

func (a *App) nextTheme() {
	a.themeIndex = (a.themeIndex + 1) % len(themes)
	a.theme = themes[a.themeIndex]
	a.applyTheme()
	a.renderResourcePanes()
	a.refreshTunnelViews()
	a.renderChrome()
	a.store.Notify(store.LevelInfo, "Theme: "+a.theme.Name)
}

func (a *App) applyTheme() {
	t := a.theme
	a.pages.SetBackgroundColor(t.Background)
	for _, view := range []*tview.TextView{a.header, a.status, a.hints} {
		view.SetBackgroundColor(t.Background)
		view.SetTextColor(t.Text)
	}
	for _, p := range a.panes {
		p.table.SetBackgroundColor(t.Background)
		p.table.SetTitleColor(t.Title)
		p.table.SetSelectedStyle(t.selectedStyle())
	}
	a.filterInput.SetBackgroundColor(t.Background)
	a.filterInput.SetLabelStyle(t.style(t.Title).Bold(true))
	a.filterInput.SetFieldStyle(t.textStyle().Underline(t.Monochrome))
	a.applyFocusBorders()
}

// applyFocusBorders colors pane borders by focus. The pane whose filter is being edited counts as focused.
func (a *App) applyFocusBorders() {
	for _, p := range a.panes {
		color := a.theme.Border
		if p.table.HasFocus() || a.filterPane == p.area {
			color = a.theme.FocusBorder
		}
		p.table.SetBorderColor(color)
	}
}

func (a *App) styleTextModal(view *tview.TextView) {
	t := a.theme
	view.SetBackgroundColor(t.Background)
	view.SetTextColor(t.Text)
	view.SetBorderColor(t.FocusBorder)
	view.SetTitleColor(t.Title)
}
