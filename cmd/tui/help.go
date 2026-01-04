package tui

import "strings"

const helpPage = "help"

var helpSections = []struct {
	title string
	keys  []hint
}{
	{"Everywhere", []hint{{"Ctrl+C", "quit, also while loading"}}},
	{"Tables", []hint{
		{"Tab / Shift+Tab", "next / previous pane"},
		{"1-4", "jump to a pane"},
		{"↑↓ j k g G PgUp PgDn", "move the cursor"},
		{"/", "filter the pane"},
		{"i", "details of the row under the cursor"},
		{"r", "refresh the active context"},
		{"? or F1", "this help"},
		{"F2", "history"},
		{"F4", "next theme: Dark, Light, Mono"},
		{"Esc", "clear the filter, otherwise the status line"},
	}},
	{"Context", []hint{
		{"Enter", "activate the context"},
		{"Ctrl+P", "register all services of the context under the cursor"},
	}},
	{"Namespace", []hint{{"Enter", "activate the namespace"}}},
	{"Services", []hint{{"Enter", "register the service"}}},
	{"Tunnels", []hint{
		{"e", "edit the hostname and local port"},
		{"", "settings last until you quit; Enter and Ctrl+P reuse them"},
		{"d, Delete, Backspace", "remove the tunnel"},
		{"c", "copy the URL"},
	}},
	{"Filter input", []hint{
		{"Enter", "keep the filter"},
		{"Esc", "clear the filter"},
		{"Tab / Shift+Tab", "keep the filter and move to another pane"},
	}},
	{"Markers", []hint{
		{"▶", "active context or namespace"},
		{"●", "registered; the note shows a custom hostname"},
		{"○", "another tunnel uses the hostname; the note names its context"},
		{"✗", "not registrable; the note gives the reason"},
		{"!", "discovery failed; i shows the error"},
	}},
}

func helpText(theme Theme) string {
	var lines []string
	for i, section := range helpSections {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, colorTag(theme.Title)+"[::b]"+section.title+"[::-]"+colorTag(theme.Text))
		for _, key := range section.keys {
			lines = append(lines, "  "+padRight(key.key, 22)+key.text)
		}
	}
	return strings.Join(lines, "\n")
}

func padRight(text string, width int) string {
	return text + strings.Repeat(" ", max(width-displayWidth(text), 1))
}

func (a *App) showHelp() {
	a.showTextModal(helpPage, "Help", helpText(a.theme), false)
}
