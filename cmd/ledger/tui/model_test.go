package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/llm"
)

// fakeSession emits configured events, then returns the configured outcome.
type fakeSession struct {
	events  []llm.CycleEvent
	outcome Outcome
	runErr  error
	closed  bool
}

func (s *fakeSession) Run(ctx context.Context, _ string, sink loop.EventSink) (Outcome, error) {
	for _, ev := range s.events {
		if err := sink.Emit(ctx, ev); err != nil {
			return Outcome{}, err
		}
	}
	return s.outcome, s.runErr
}

func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

// blockingSession blocks until ctx is cancelled.
type blockingSession struct{}

func (blockingSession) Run(ctx context.Context, _ string, _ loop.EventSink) (Outcome, error) {
	<-ctx.Done()
	return Outcome{}, ctx.Err()
}

func (blockingSession) Close() error { return nil }

func newTestModel(session Session) model {
	m := newModel(context.Background(), []Option{{
		Label: "test agent",
		Start: func(context.Context) (Session, error) { return session, nil },
	}})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(model)
}

// chatModel selects the option so the returned model is in stateChat.
func chatModel(t *testing.T, session Session) model {
	t.Helper()
	m := newTestModel(session)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("enter on the start screen should produce a command")
	}
	ready, ok := cmd().(sessionReadyMsg)
	if !ok {
		t.Fatal("start command should yield a sessionReadyMsg")
	}
	next, _ = m.Update(ready)
	return next.(model)
}

func driveTurn(t *testing.T, m model) model {
	t.Helper()
	for i := 0; i < 100 && m.running; i++ {
		msg := waitForTurn(m.events, m.done)()
		next, _ := m.Update(msg)
		m = next.(model)
	}
	if m.running {
		t.Fatal("turn did not finish")
	}
	return m
}

func TestModelSelectStartsSession(t *testing.T) {
	fake := &fakeSession{}
	m := newTestModel(fake)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("enter should produce a start-session command")
	}
	msg := cmd()
	ready, ok := msg.(sessionReadyMsg)
	if !ok {
		t.Fatalf("command result = %T, want sessionReadyMsg", msg)
	}

	next, _ = m.Update(ready)
	m = next.(model)
	if m.state != stateChat {
		t.Fatalf("state = %v, want stateChat", m.state)
	}
	if m.session != fake {
		t.Error("session was not stored on the model")
	}
}

func TestModelRunTurnStreamsEvents(t *testing.T) {
	fake := &fakeSession{
		events: []llm.CycleEvent{
			{Kind: llm.EventModelOutput, Content: "drafting the entry"},
			{Kind: llm.EventValidationError, Content: "credits short of debits"},
			{Kind: llm.EventObservation, Content: "posted journal entry E1"},
		},
		outcome: Outcome{Turns: 2, Summary: "posted entry E1"},
	}
	m := chatModel(t, fake)
	m.input.SetValue("pay the AWS bill")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.running {
		t.Fatal("model should be running right after submit")
	}

	m = driveTurn(t, m)
	if m.running {
		t.Fatal("model should be idle after the turn finishes")
	}

	// user request + 3 cycle events + system summary
	wantKinds := []lineKind{lineUser, lineModel, lineValidation, lineObservation, lineSystem}
	if len(m.lines) != len(wantKinds) {
		t.Fatalf("transcript has %d lines, want %d", len(m.lines), len(wantKinds))
	}
	for i, want := range wantKinds {
		if m.lines[i].kind != want {
			t.Errorf("line %d kind = %v, want %v", i, m.lines[i].kind, want)
		}
	}
	if m.lines[0].text != "pay the AWS bill" {
		t.Errorf("first line = %q, want the user request", m.lines[0].text)
	}
}

func TestModelCtrlCQuitsFromSelect(t *testing.T) {
	m := newTestModel(&fakeSession{})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c should produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c on the start screen should quit, got %T", cmd())
	}
}

func TestModelEscCancelsRunningTurn(t *testing.T) {
	m := chatModel(t, blockingSession{})
	m.input.SetValue("do something slow")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.running {
		t.Fatal("model should be running")
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("esc during a turn must cancel the turn, not quit")
		}
	}
	if m.state != stateChat {
		t.Fatalf("state = %v, want stateChat: cancelling a turn keeps the session open", m.state)
	}

	m = driveTurn(t, m)
	if m.running {
		t.Fatal("the cancelled turn should have finished")
	}
	last := m.lines[len(m.lines)-1]
	if last.kind != lineSystem {
		t.Errorf("last line kind = %v, want lineSystem (cancellation note)", last.kind)
	}
}

func TestModelCtrlCQuitsDuringTurn(t *testing.T) {
	m := chatModel(t, blockingSession{})
	m.input.SetValue("do something slow")

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.running {
		t.Fatal("model should be running")
	}

	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c should produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c during a turn should quit, got %T", cmd())
	}
}

func TestModelEscClosesSessionAndReturns(t *testing.T) {
	fake := &fakeSession{}
	m := chatModel(t, fake)

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if m.state != stateSelect {
		t.Fatalf("state = %v, want stateSelect after esc", m.state)
	}
	if !fake.closed {
		t.Error("esc should close the session before returning to the start screen")
	}
}

func TestModelViewDoesNotPanic(t *testing.T) {
	m := newTestModel(&fakeSession{})
	if m.View().Content == "" {
		t.Error("select view is empty")
	}
	chat := chatModel(t, &fakeSession{})
	if chat.View().Content == "" {
		t.Error("chat view is empty")
	}
}

func TestModelRendersModelOutputAsMarkdown(t *testing.T) {
	m := newTestModel(&fakeSession{})
	if m.md == nil {
		t.Fatal("layout should have built the Glamour renderer")
	}
	width := max(m.viewport.Width()-2, 20)

	// lineModel is Markdown-rendered: Glamour consumes inline-code backticks.
	got := m.renderBody(line{kind: lineModel, text: "run `go test`"}, width)
	if strings.Contains(got, "`") {
		t.Errorf("model output should be Markdown-rendered, backticks remain: %q", got)
	}
	plain := m.renderBody(line{kind: lineSystem, text: "run `go test`"}, width)
	if !strings.Contains(plain, "`") {
		t.Errorf("non-model line should stay literal, got %q", plain)
	}
}

func TestEventLineKindMapsToolResult(t *testing.T) {
	if got := eventLineKind(llm.EventToolResult); got != lineTool {
		t.Errorf("eventLineKind(EventToolResult) = %v, want lineTool", got)
	}
}
