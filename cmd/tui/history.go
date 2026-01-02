package tui

import (
	"fmt"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/rivo/tview"
)

const (
	historyPage       = "history"
	historyTimeLayout = "15:04:05"
	historyIndent     = "               "
)

func (a *App) showHistory() {
	history := a.store.GetState().History
	var shownThrough uint64
	if len(history) > 0 {
		shownThrough = history[len(history)-1].ID
	}
	a.store.MarkHistoryRead(shownThrough)
	a.renderHeader()
	a.showTextModal(historyPage, "History", historyText(history, a.theme), true)
}

// historyText renders events as "15:04:05 LEVEL text", oldest first, keeping every line of the text.
func historyText(history []store.Event, theme Theme) string {
	if len(history) == 0 {
		return "No events yet."
	}
	lines := make([]string, 0, len(history))
	for _, event := range history {
		textLines := strings.Split(tview.Escape(event.Text), "\n")
		head := fmt.Sprintf("%s %s%-5s%s %s", event.Time.Format(historyTimeLayout),
			colorTag(theme.levelColor(event.Level)), event.Level, colorTag(theme.Text), textLines[0])
		lines = append(lines, head)
		for _, line := range textLines[1:] {
			lines = append(lines, historyIndent+line)
		}
	}
	return strings.Join(lines, "\n")
}
