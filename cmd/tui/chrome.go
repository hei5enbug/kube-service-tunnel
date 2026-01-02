package tui

import (
	"fmt"
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
	"github.com/rivo/tview"
)

const (
	appName     = "kube-service-tunnel"
	historyHint = " (F2)"
)

func (a *App) renderChrome() {
	a.renderHeader()
	a.renderStatus()
	a.renderHints()
}

func (a *App) renderHeader() {
	state := a.store.GetState()
	left := headerLeft(state, len(a.tunnels))
	if a.layout.mode == layoutCompact {
		left = " " + tabStrip(state.Focus)
	}
	right := ""
	if state.Unread > 0 {
		right = fmt.Sprintf("⚠ %d unread · F2 ", state.Unread)
	}
	width := a.layout.width
	leftWidth := max(width-displayWidth(right)-1, 0)
	if width <= 0 {
		leftWidth = displayWidth(left)
	}
	left = truncate(left, leftWidth)
	padding := strings.Repeat(" ", max(width-displayWidth(left)-displayWidth(right), 1))
	text := colorTag(a.theme.Title) + tview.Escape(left)
	if right != "" {
		text += padding + colorTag(a.theme.Warning) + right
	}
	a.header.SetText(text)
}

func headerLeft(state store.State, tunnels int) string {
	contextName := state.SelectedContext
	if contextName == "" {
		contextName = "-"
	}
	return fmt.Sprintf(" %s · ctx %s · %s", appName, contextName, plural(tunnels, "tunnel"))
}

func tabStrip(focus store.FocusArea) string {
	parts := make([]string, len(paneOrder))
	for i, area := range paneOrder {
		parts[i] = paneHeading(area)
		if area == focus {
			parts[i] = "[" + parts[i] + "]"
		}
	}
	return strings.Join(parts, " ")
}

func (a *App) renderStatus() {
	status := a.store.GetState().Status
	a.status.SetTextColor(a.theme.levelColor(status.Level))
	a.status.SetText(statusLine(status, a.layout.width))
}

func levelBadge(level store.Level) string {
	switch level {
	case store.LevelSuccess:
		return "✓"
	case store.LevelWarning:
		return "⚠ WARN"
	case store.LevelError:
		return "✗ ERROR"
	default:
		return "ℹ"
	}
}

// statusLine formats an event for the one-line status view. It shows the first line, escaped, and adds (F2)
// when the text had more lines or did not fit. A width of zero or less disables truncation.
func statusLine(event store.Event, width int) string {
	if event.ID == 0 {
		return ""
	}
	prefix := " " + levelBadge(event.Level) + " "
	text, multiLine := singleLine(event.Text)
	suffix := ""
	if multiLine {
		suffix = historyHint
	}
	if width > 0 {
		available := width - displayWidth(prefix) - displayWidth(suffix)
		if displayWidth(text) > available {
			suffix = historyHint
			text = truncate(text, width-displayWidth(prefix)-displayWidth(suffix))
		}
	}
	return prefix + tview.Escape(text) + suffix
}
