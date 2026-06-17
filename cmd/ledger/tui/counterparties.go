package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/accounting"
)

var cpFields = []struct{ label, placeholder string }{
	{"name", "legal or trade name"},
	{"kind", ""},
	{"tax_id", "optional, stored verbatim"},
	{"aliases", "optional, comma-separated"},
	{"description", "optional"},
}

// cpKindField is the index of the kind chooser; it is selected with ←/→, not typed.
const cpKindField = 1

var cpKinds = []accounting.CounterpartyKind{
	accounting.CounterpartyCustomer,
	accounting.CounterpartySupplier,
	accounting.CounterpartyBoth,
}

// cpForm is the /counterparties add overlay state.
type cpForm struct {
	inputs []textinput.Model
	focus  int
	kind   int // index into cpKinds
	err    string
}

type cpAddedMsg struct {
	cp  accounting.Counterparty
	err error
}

func newCPForm() cpForm {
	inputs := make([]textinput.Model, len(cpFields))
	for i, f := range cpFields {
		ti := textinput.New()
		ti.Placeholder = f.placeholder
		ti.Prompt = ""
		inputs[i] = ti
	}
	inputs[0].Focus()
	return cpForm{inputs: inputs}
}

func (f *cpForm) focusNext() { f.move(1) }
func (f *cpForm) focusPrev() { f.move(-1) }

func (f *cpForm) move(delta int) {
	f.inputs[f.focus].Blur()
	f.focus = (f.focus + delta + len(f.inputs)) % len(f.inputs)
	f.inputs[f.focus].Focus()
}

func (f *cpForm) cycleKind(delta int) {
	f.kind = (f.kind + delta + len(cpKinds)) % len(cpKinds)
}

func (f cpForm) draft() accounting.Counterparty {
	get := func(i int) string { return strings.TrimSpace(f.inputs[i].Value()) }
	cp := accounting.Counterparty{
		Name:        get(0),
		Kind:        cpKinds[f.kind],
		TaxID:       get(2),
		Description: get(4),
	}
	for a := range strings.SplitSeq(get(3), ",") {
		if a = strings.TrimSpace(a); a != "" {
			cp.Aliases = append(cp.Aliases, a)
		}
	}
	return cp
}

// cmdCounterparties lists counterparties, or opens the add form on `add`.
func (m model) cmdCounterparties(args []string) (tea.Model, tea.Cmd) {
	admin, ok := m.session.(CounterpartyAdmin)
	if !ok {
		m.appendLine(line{kind: lineSystem, text: "this branch cannot manage counterparties"})
		return m, nil
	}
	if len(args) == 0 {
		list, err := admin.Counterparties(m.ctx)
		if err != nil {
			m.appendLine(line{kind: lineError, text: err.Error()})
			return m, nil
		}
		m.appendLine(line{kind: lineSystem, text: counterpartyList(list)})
		return m, nil
	}
	switch args[0] {
	case "add":
		m.adding = true
		m.form = newCPForm()
		return m, textinput.Blink
	default:
		m.appendLine(line{kind: lineSystem, text: "usage: /counterparties [add]"})
		return m, nil
	}
}

func (m model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.adding = false
		m.form = cpForm{}
		return m, nil
	case "tab", "down":
		m.form.focusNext()
		return m, nil
	case "shift+tab", "up":
		m.form.focusPrev()
		return m, nil
	case "enter":
		draft := m.form.draft()
		if err := draft.Validate(); err != nil {
			m.form.err = err.Error()
			return m, nil
		}
		m.form.err = ""
		return m, m.submitCounterparty(draft)
	}
	if m.form.focus == cpKindField {
		switch msg.String() {
		case "left", "h":
			m.form.cycleKind(-1)
		case "right", "l", " ":
			m.form.cycleKind(1)
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.form.inputs[m.form.focus], cmd = m.form.inputs[m.form.focus].Update(msg)
	return m, cmd
}

func (m model) submitCounterparty(draft accounting.Counterparty) tea.Cmd {
	admin, ok := m.session.(CounterpartyAdmin)
	if !ok {
		return func() tea.Msg { return cpAddedMsg{err: errNoCounterpartyAdmin} }
	}
	ctx := m.ctx
	return func() tea.Msg {
		cp, err := admin.AddCounterparty(ctx, draft)
		return cpAddedMsg{cp: cp, err: err}
	}
}

func (m model) finishCounterparty(msg cpAddedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.form.err = msg.err.Error()
		return m, nil
	}
	m.adding = false
	m.form = cpForm{}
	m.appendLine(line{kind: lineSystem, text: fmt.Sprintf("registered %s — %s (%s)", msg.cp.ID, msg.cp.Name, msg.cp.Kind)})
	return m, nil
}

func (m model) counterpartyFormView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Register counterparty"))
	b.WriteString("\n")
	b.WriteString(divider(min(m.width, 60)))
	b.WriteString("\n\n")
	for i, in := range m.form.inputs {
		marker := "  "
		if i == m.form.focus {
			marker = keyStyle.Render("❯ ")
		}
		val := in.View()
		if i == cpKindField {
			val = renderKindChoice(m.form.kind)
		}
		fmt.Fprintf(&b, "%s%-12s %s\n", marker, cpFields[i].label, val)
	}
	if m.form.err != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(m.form.err))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(keyHints(
		[2]string{"tab", "next field"},
		[2]string{"←/→", "change kind"},
		[2]string{"enter", "save"},
		[2]string{"esc", "cancel"},
	))
	return b.String()
}

func renderKindChoice(sel int) string {
	parts := make([]string, len(cpKinds))
	for i, k := range cpKinds {
		if i == sel {
			parts[i] = keyStyle.Render("[" + string(k) + "]")
		} else {
			parts[i] = hintStyle.Render(" " + string(k) + " ")
		}
	}
	return strings.Join(parts, " ")
}

func counterpartyList(cps []accounting.Counterparty) string {
	if len(cps) == 0 {
		return "no counterparties — /counterparties add to register one"
	}
	var b strings.Builder
	b.WriteString("counterparties (use /counterparties add to register one):")
	for _, c := range cps {
		status := "active"
		if !c.Active {
			status = "inactive"
		}
		fmt.Fprintf(&b, "\n  %-8s %-9s %s  [%s, %s]", c.ID, c.TaxID, c.Name, c.Kind, status)
	}
	return b.String()
}
