package tui

import (
	"strings"

	"github.com/byoungmin/kube-service-tunnel/cmd/tui/store"
)

type hintScope int

const (
	scopeContexts hintScope = iota
	scopeNamespaces
	scopeServices
	scopeTunnels
	scopeFilter
	scopeText
	scopeConfirm
	scopeEdit
	scopeLoading
)

type hint struct {
	key  string
	text string
}

const hintGap = "  "

var (
	commonTableHints = []hint{{"/", "filter"}, {"i", "details"}, {"r", "refresh"}, {"1-4", "pane"}}
	tableTailHints   = []hint{{"?", "help"}, {"^C", "quit"}}
	quitHints        = []hint{{"^C", "quit"}}
	scopeHints       = map[hintScope][]hint{
		scopeContexts:   {{"↵", "activate"}, {"^P", "register all"}},
		scopeNamespaces: {{"↵", "activate"}},
		scopeServices:   {{"↵", "register"}},
		scopeTunnels:    {{"e", "edit"}, {"d", "delete"}, {"c", "copy"}},
		scopeFilter:     {{"type", "to filter"}, {"↵", "keep"}, {"esc", "clear"}, {"⇥", "next pane"}, {"F1", "help"}},
		scopeText:       {{"↑↓", "scroll"}, {"esc", "close"}},
		scopeConfirm:    {{"y", "register"}, {"n", "cancel"}, {"←→", "choose"}, {"↵", "select"}, {"↑↓", "scroll"}, {"esc", "cancel"}},
		scopeEdit:       {{"⇥", "next"}, {"↵", "save"}, {"esc", "cancel"}},
	}
)

func (a *App) hintScope() hintScope {
	switch {
	case a.loading != nil:
		return scopeLoading
	case a.modal != nil:
		return a.modal.scope
	case a.filterPane != "":
		return scopeFilter
	}
	switch a.store.GetState().Focus {
	case store.FocusNamespaces:
		return scopeNamespaces
	case store.FocusServices:
		return scopeServices
	case store.FocusTunnels:
		return scopeTunnels
	default:
		return scopeContexts
	}
}

// hintsFor returns the hints of a scope. Table scopes show their own keys, then shared table keys while they
// fit, then help and quit. Modal and input scopes drop their last keys first, so quit always stays visible.
func hintsFor(scope hintScope, width int) []hint {
	if scope == scopeLoading {
		return quitHints
	}
	head := scopeHints[scope]
	if scope == scopeFilter || scope == scopeText || scope == scopeConfirm || scope == scopeEdit {
		return fitHints(nil, head, quitHints, width)
	}
	return fitHints(head, commonTableHints, tableTailHints, width)
}

// fitHints keeps fixed and tail, and adds optional hints in order while the line still fits width.
func fitHints(fixed, optional, tail []hint, width int) []hint {
	result := append([]hint(nil), fixed...)
	used := hintsWidth(fixed) + hintsWidth(tail) + len(hintGap)
	for _, h := range optional {
		next := hintsWidth([]hint{h}) + len(hintGap)
		if width > 0 && used+next > width {
			break
		}
		result = append(result, h)
		used += next
	}
	return append(result, tail...)
}

func hintsWidth(hints []hint) int {
	width := 0
	for i, h := range hints {
		if i > 0 {
			width += len(hintGap)
		}
		width += displayWidth(h.key) + 1 + displayWidth(h.text)
	}
	return width + 1
}

func (a *App) renderHints() {
	hints := hintsFor(a.hintScope(), a.layout.width)
	keyTag, textTag := colorTag(a.theme.Text)+"[::b]", colorTag(a.theme.Dim)+"[::-]"
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = keyTag + h.key + textTag + " " + h.text
	}
	a.hints.SetText(" " + strings.Join(parts, hintGap))
}
