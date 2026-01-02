package tui

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
	"github.com/rivo/uniseg"
)

const ellipsis = "…"

func displayWidth(text string) int {
	return uniseg.StringWidth(text)
}

// truncate shortens text to at most width terminal cells, ending with an ellipsis when it was cut.
func truncate(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if displayWidth(text) <= width {
		return text
	}
	var builder strings.Builder
	used, state, rest := 0, -1, text
	for rest != "" {
		var cluster string
		var clusterWidth int
		cluster, rest, clusterWidth, state = uniseg.FirstGraphemeClusterInString(rest, state)
		if used+clusterWidth > width-1 {
			break
		}
		builder.WriteString(cluster)
		used += clusterWidth
	}
	return builder.String() + ellipsis
}

// fit truncates text to width cells and escapes it for widgets that interpret style tags.
func fit(text string, width int) string {
	return tview.Escape(truncate(text, width))
}

// singleLine returns the first line of text with tabs and carriage returns turned into spaces,
// and whether anything followed that line.
func singleLine(text string) (string, bool) {
	first, rest, multiLine := strings.Cut(text, "\n")
	first = strings.NewReplacer("\r", " ", "\t", " ").Replace(first)
	return first, multiLine && strings.TrimSpace(rest) != ""
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func clamp(value, low, high int) int {
	return max(low, min(value, high))
}
