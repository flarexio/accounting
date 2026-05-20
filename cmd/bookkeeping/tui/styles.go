package tui

import "charm.land/lipgloss/v2"

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	cursorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	hintStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	footerStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))

	// transcript line labels, keyed by lineKind
	userStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	modelStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	validationStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	executionStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	observationStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("78"))
	toolStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("45"))
	systemStyle      = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("245"))
	errorStyle       = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
)
