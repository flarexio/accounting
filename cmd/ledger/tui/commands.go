package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// command is one slash command shown by /help and dispatched by handleCommand.
type command struct {
	name  string
	usage string
	desc  string
}

// commands is the slash-command registry. Add a row plus a case in
// handleCommand to expose a new command.
var commands = []command{
	{name: "branch", usage: "/branch [id]", desc: "switch branch by id, or open a picker with no id"},
	{name: "counterparties", usage: "/counterparties [add]", desc: "list counterparties, or open the add form (alias /cp)"},
	{name: "help", usage: "/help", desc: "show this help"},
}

// handleCommand runs a /-prefixed input as a slash command instead of sending
// it to the agent. The input has already had its leading slash and surrounding
// space; results are appended to the transcript as system lines.
func (m model) handleCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.TrimPrefix(input, "/"))
	if len(fields) == 0 {
		m.appendLine(line{kind: lineSystem, text: helpText()})
		return m, nil
	}
	switch fields[0] {
	case "help":
		m.appendLine(line{kind: lineSystem, text: helpText()})
		return m, nil
	case "branch":
		return m.cmdBranch(fields[1:])
	case "counterparties", "cp":
		return m.cmdCounterparties(fields[1:])
	default:
		m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("unknown command %q — type /help", "/"+fields[0])})
		return m, nil
	}
}

// cmdBranch switches the active branch, rebuilding the session for it. With an
// id it switches directly (CLI style); with no id it opens the picker overlay.
func (m model) cmdBranch(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		if len(m.options) == 1 {
			m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("only one branch: %s (%s)", m.options[0].Label, m.options[0].Hint)})
			return m, nil
		}
		m.picking = true
		m.cursor = m.current
		return m, nil
	}
	id := args[0]
	for i, opt := range m.options {
		if opt.Hint != id {
			continue
		}
		if i == m.current {
			m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("already on branch %s (%s)", opt.Label, opt.Hint)})
			return m, nil
		}
		m.current = i
		_ = m.session.Close()
		m.session = nil
		return m, m.startSession(opt)
	}
	m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("unknown branch %q\n%s", id, m.branchList())})
	return m, nil
}

func helpText() string {
	var b strings.Builder
	b.WriteString("commands:")
	for _, c := range commands {
		fmt.Fprintf(&b, "\n  %-14s %s", c.usage, c.desc)
	}
	return b.String()
}

func (m model) branchList() string {
	var b strings.Builder
	b.WriteString("branches (use /branch <id>):")
	for i, opt := range m.options {
		marker := "  "
		if i == m.current {
			marker = "* "
		}
		fmt.Fprintf(&b, "\n  %s%s (%s)", marker, opt.Label, opt.Hint)
	}
	return b.String()
}
