package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette — a cohesive modern dark theme (Catppuccin Mocha).
var (
	cBase   = lipgloss.Color("#1E1E2E")
	cMuted  = lipgloss.Color("#6C7086")
	cSubtle = lipgloss.Color("#45475A")

	cPrimary = lipgloss.Color("#CBA6F7") // mauve
	cBlue    = lipgloss.Color("#89B4FA")
	cLav     = lipgloss.Color("#B4BEFE")
	cPeach   = lipgloss.Color("#FAB387")
	cRed     = lipgloss.Color("#F38BA8")
	cGreen   = lipgloss.Color("#A6E3A1")
	cTeal    = lipgloss.Color("#94E2D5")
)

var (
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(cPrimary)
	hintStyle    = lipgloss.NewStyle().Foreground(cMuted)
	footerStyle  = lipgloss.NewStyle().Foreground(cMuted)
	keyStyle     = lipgloss.NewStyle().Bold(true).Foreground(cPrimary)
	systemStyle  = lipgloss.NewStyle().Italic(true).Foreground(cMuted)
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(cRed)
	dividerStyle = lipgloss.NewStyle().Foreground(cSubtle)
)

// roleColor is the accent each transcript line kind wears as a badge.
var roleColor = map[lineKind]color.Color{
	lineUser:        cBlue,
	lineModel:       cLav,
	lineValidation:  cPeach,
	lineExecution:   cRed,
	lineObservation: cGreen,
	lineTool:        cTeal,
	linePreview:     cPrimary,
	lineError:       cRed,
	lineSystem:      cMuted,
}

// badge renders a filled, padded pill — the transcript role label.
func badge(label string, c color.Color) string {
	return lipgloss.NewStyle().Bold(true).Foreground(cBase).Background(c).Padding(0, 1).Render(label)
}

// divider renders a full-width horizontal rule.
func divider(width int) string {
	if width < 1 {
		width = 1
	}
	return dividerStyle.Render(strings.Repeat("─", width))
}

// keyHint renders a "key desc" pair with the key highlighted, for footers.
func keyHint(key, desc string) string {
	return keyStyle.Render(key) + footerStyle.Render(" "+desc)
}

// keyHints joins several key hints with a muted separator.
func keyHints(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = keyHint(p[0], p[1])
	}
	return strings.Join(parts, footerStyle.Render("  ·  "))
}
