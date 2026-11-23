package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jabiOP/climail/internal/output"
)

// Colors adapt to light and dark terminals.
var (
	accent   = lipgloss.AdaptiveColor{Light: "#1a73e8", Dark: "#8ab4f8"}
	muted    = lipgloss.AdaptiveColor{Light: "#5f6368", Dark: "#9aa0a6"}
	rule     = lipgloss.AdaptiveColor{Light: "#dadce0", Dark: "#3c4043"}
	cursorBg = lipgloss.AdaptiveColor{Light: "#e8f0fe", Dark: "#263449"}
	starred  = lipgloss.AdaptiveColor{Light: "#e37400", Dark: "#fdd663"}
	danger   = lipgloss.AdaptiveColor{Light: "#d93025", Dark: "#f28b82"}
)

var (
	plain     = lipgloss.NewStyle()
	bold      = lipgloss.NewStyle().Bold(true)
	dim       = lipgloss.NewStyle().Foreground(muted)
	accentFg  = lipgloss.NewStyle().Foreground(accent)
	title     = lipgloss.NewStyle().Foreground(accent).Bold(true)
	starStyle = lipgloss.NewStyle().Foreground(starred)
	errStyle  = lipgloss.NewStyle().Foreground(danger)
	ruleStyle = lipgloss.NewStyle().Foreground(rule)
	quoted    = lipgloss.NewStyle().Foreground(muted).Italic(true)
	helpBox   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1)
)

// width is the number of terminal cells s takes, ANSI codes aside. Every
// width in this package comes from here (never len(): "—" is 3 bytes, CJK
// runes and most emoji take 2 cells).
func width(s string) int { return lipgloss.Width(s) }

// fit truncates plain text s to w cells (ending in "…") and pads it to exactly
// w. Style the result afterwards: cutting styled text would cut its codes.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = output.Clean(s) // mail can carry terminal escape sequences
	if sw := width(s); sw <= w {
		return s + strings.Repeat(" ", w-sw)
	}
	out := ""
	for _, r := range s {
		next := out + string(r)
		if width(next) > w-1 {
			break
		}
		out = next
	}
	out += "…"
	return out + strings.Repeat(" ", max(w-width(out), 0))
}

// fitRight is fit, but pads on the left (for dates and counts).
func fitRight(s string, w int) string {
	if width(s) >= w {
		return fit(s, w)
	}
	return strings.Repeat(" ", w-width(s)) + s
}

// row draws a line from styled segments over a base style (e.g. the cursor's
// background), so the background runs through the gaps as well.
func row(base lipgloss.Style, segs ...seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.style.Inherit(base).Render(output.Clean(s.text))) // also what didn't go through fit
	}
	return b.String()
}

type seg struct {
	text  string // already fitted to its width
	style lipgloss.Style
}

func s(text string, style lipgloss.Style) seg { return seg{text, style} }

// accountColors are Google Calendar's event colors (as clindar draws them),
// given to accounts in the order they were added, so their mail is told
// apart at a glance.
var accountColors = []string{
	"#039be5", // Peacock
	"#d50000", // Tomato
	"#0b8043", // Basil
	"#f6bf26", // Banana
	"#8e24aa", // Grape
	"#f4511e", // Tangerine
	"#3f51b5", // Blueberry
	"#e67c73", // Flamingo
	"#33b679", // Sage
	"#7986cb", // Lavender
	"#616161", // Graphite
}

// accountStyle draws in the account's color.
func (m Model) accountStyle(account string) lipgloss.Style {
	i := slices.Index(m.accounts, account)
	if i < 0 {
		return dim
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(accountColors[i%len(accountColors)]))
}

// labelStyle draws a label in its Gmail color, if it has one.
func labelStyle(color string) lipgloss.Style {
	var r, g, b int
	if _, err := fmt.Sscanf(color, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return dim
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
