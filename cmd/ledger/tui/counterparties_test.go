package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/accounting"
)

type fakeCounterpartyAdmin struct {
	fakeSession
	list  []accounting.Counterparty
	added []accounting.Counterparty
}

func (s *fakeCounterpartyAdmin) Counterparties(context.Context) ([]accounting.Counterparty, error) {
	return s.list, nil
}

func (s *fakeCounterpartyAdmin) AddCounterparty(_ context.Context, draft accounting.Counterparty) (accounting.Counterparty, error) {
	draft.ID = accounting.FormatCounterpartyID(uint64(len(s.list)) + 1)
	draft.Active = true
	s.list = append(s.list, draft)
	s.added = append(s.added, draft)
	return draft, nil
}

func TestCounterpartiesListCommand(t *testing.T) {
	admin := &fakeCounterpartyAdmin{list: []accounting.Counterparty{
		{ID: "CP-0001", Name: "Acme", Kind: accounting.CounterpartyCustomer, Active: true},
	}}
	m := chatModel(t, admin)

	m, cmd := submitInput(t, m, "/cp")
	if cmd != nil {
		t.Fatal("listing counterparties should not run a command")
	}
	last := m.lines[len(m.lines)-1]
	if last.kind != lineSystem || !strings.Contains(last.text, "CP-0001") || !strings.Contains(last.text, "Acme") {
		t.Errorf("/cp should list counterparties, got %+v", last)
	}
}

func TestCounterpartiesAddFormRegisters(t *testing.T) {
	admin := &fakeCounterpartyAdmin{}
	m := chatModel(t, admin)

	m, _ = submitInput(t, m, "/cp add")
	if !m.adding {
		t.Fatal("/cp add should open the form overlay")
	}
	if m.View().Content == "" {
		t.Error("form view should render")
	}

	m.form.inputs[0].SetValue("Globex")

	// move to the kind chooser and cycle customer -> supplier with →.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(model)
	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = next.(model)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("submitting a valid draft should run AddCounterparty")
	}
	added, ok := cmd().(cpAddedMsg)
	if !ok || added.err != nil {
		t.Fatalf("submit should yield a clean cpAddedMsg, got %#v", cmd())
	}
	next, _ = m.Update(added)
	m = next.(model)

	if m.adding {
		t.Fatal("a successful add should close the form")
	}
	if len(admin.added) != 1 || admin.added[0].Name != "Globex" || admin.added[0].Kind != accounting.CounterpartySupplier {
		t.Fatalf("AddCounterparty got the wrong draft: %+v", admin.added)
	}
	last := m.lines[len(m.lines)-1]
	if !strings.Contains(last.text, "registered") || !strings.Contains(last.text, "CP-0001") {
		t.Errorf("expected a registered-confirmation line, got %q", last.text)
	}
}

func TestCounterpartiesAddFormRejectsEmptyName(t *testing.T) {
	m := chatModel(t, &fakeCounterpartyAdmin{})

	m, _ = submitInput(t, m, "/cp add")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // no name typed
	m = next.(model)
	if cmd != nil {
		t.Fatal("an invalid draft should not run a command")
	}
	if !m.adding {
		t.Fatal("the form should stay open on a validation error")
	}
	if m.form.err == "" {
		t.Error("the form should surface the validation error")
	}
}

func TestCounterpartiesAddFormCancel(t *testing.T) {
	m := chatModel(t, &fakeCounterpartyAdmin{})

	m, _ = submitInput(t, m, "/cp add")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if cmd != nil {
		t.Fatal("cancelling the form should not run a command")
	}
	if m.adding {
		t.Fatal("esc should close the form")
	}
}
