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

// chatModel starts the first branch's session so the returned model is in chat.
func chatModel(t *testing.T, session Session) model {
	t.Helper()
	m := newTestModel(session)
	ready, ok := m.startSession(m.options[0])().(sessionReadyMsg)
	if !ok {
		t.Fatal("startSession should yield a sessionReadyMsg")
	}
	next, _ := m.Update(ready)
	return next.(model)
}

// twoBranchModel starts a model with two branches (hq, tc) already in chat.
func twoBranchModel(t *testing.T) (model, *fakeSession, *fakeSession) {
	t.Helper()
	hq, tc := &fakeSession{}, &fakeSession{}
	m := newModel(context.Background(), []Option{
		{Label: "HQ", Hint: "hq", Start: func(context.Context) (Session, error) { return hq, nil }},
		{Label: "Taichung", Hint: "tc", Start: func(context.Context) (Session, error) { return tc, nil }},
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(model)
	ready := m.startSession(m.options[0])().(sessionReadyMsg)
	next, _ = m.Update(ready)
	return next.(model), hq, tc
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

func TestModelAutoStartsFirstBranch(t *testing.T) {
	fake := &fakeSession{}
	m := newTestModel(fake)

	// No select screen: Init starts the first branch's session straight away.
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init should auto-start a session")
	}
	ready, ok := m.startSession(m.options[0])().(sessionReadyMsg)
	if !ok {
		t.Fatalf("startSession should yield a sessionReadyMsg")
	}
	next, _ := m.Update(ready)
	m = next.(model)
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

func TestModelCtrlCQuitsBeforeSession(t *testing.T) {
	m := newTestModel(&fakeSession{})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c should produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+c should quit even before the session is up, got %T", cmd())
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
	if m.session == nil {
		t.Fatal("cancelling a turn keeps the session open")
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

func TestModelEscReconnectsSession(t *testing.T) {
	fake := &fakeSession{}
	m := chatModel(t, fake)

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if !fake.closed {
		t.Error("esc should close the open session")
	}
	if cmd == nil {
		t.Fatal("esc should reconnect a fresh session")
	}
	if _, ok := cmd().(tea.QuitMsg); ok {
		t.Fatal("esc must never quit; only ctrl+c/ctrl+d quit")
	}
	if _, ok := cmd().(sessionReadyMsg); !ok {
		t.Fatalf("esc should start a fresh session, got %T", cmd())
	}
}

// submitInput types text and presses Enter.
func submitInput(t *testing.T, m model, text string) (model, tea.Cmd) {
	t.Helper()
	m.input.SetValue(text)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(model), cmd
}

func TestModelBranchCommandSwitchesSession(t *testing.T) {
	m, hq, tc := twoBranchModel(t)
	if m.session != hq || m.current != 0 {
		t.Fatalf("should start on the first branch (hq)")
	}

	m, cmd := submitInput(t, m, "/branch tc")
	if cmd == nil {
		t.Fatal("/branch <id> should start a new session")
	}
	if !hq.closed {
		t.Error("/branch should close the previous branch's session")
	}
	if m.current != 1 {
		t.Fatalf("current = %d, want 1 (tc)", m.current)
	}
	ready, ok := cmd().(sessionReadyMsg)
	if !ok {
		t.Fatalf("/branch should yield a sessionReadyMsg, got %T", cmd())
	}
	next, _ := m.Update(ready)
	m = next.(model)
	if m.session != tc {
		t.Error("session should now be the tc branch")
	}
}

func TestModelBranchCommandUnknownId(t *testing.T) {
	m, _, _ := twoBranchModel(t)

	m, cmd := submitInput(t, m, "/branch zz")
	if cmd != nil {
		t.Fatal("an unknown branch should not switch session")
	}
	if m.picking {
		t.Fatal("an unknown branch id should not open the picker")
	}
	if last := m.lines[len(m.lines)-1]; last.kind != lineSystem || !strings.Contains(last.text, "unknown branch") {
		t.Errorf("expected an unknown-branch system note, got %+v", last)
	}
}

func TestModelBranchPickerSwitches(t *testing.T) {
	m, hq, tc := twoBranchModel(t)

	// /branch with no id opens the picker on the current branch.
	m, cmd := submitInput(t, m, "/branch")
	if cmd != nil || !m.picking || m.cursor != 0 {
		t.Fatalf("/branch should open the picker at current; picking=%v cursor=%d", m.picking, m.cursor)
	}
	if m.View().Content == "" {
		t.Error("picker view should render")
	}

	// move down to tc, enter to switch.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.picking {
		t.Fatal("enter should close the picker")
	}
	if m.current != 1 || !hq.closed {
		t.Fatalf("enter should switch to tc and close hq: current=%d hqClosed=%v", m.current, hq.closed)
	}
	ready := cmd().(sessionReadyMsg)
	next, _ = m.Update(ready)
	if next.(model).session != tc {
		t.Error("session should be the tc branch after switching")
	}
}

func TestModelBranchPickerCancel(t *testing.T) {
	m, hq, _ := twoBranchModel(t)

	m, _ = submitInput(t, m, "/branch")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // move, but cancel
	m = next.(model)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = next.(model)
	if m.picking {
		t.Fatal("esc should close the picker")
	}
	if cmd != nil {
		t.Fatal("cancelling the picker should not switch session")
	}
	if m.current != 0 || hq.closed {
		t.Fatal("cancel must leave the current branch and its session untouched")
	}
}

func TestModelHelpAndUnknownCommand(t *testing.T) {
	m := chatModel(t, &fakeSession{})

	m, _ = submitInput(t, m, "/help")
	if last := m.lines[len(m.lines)-1]; !strings.Contains(last.text, "/branch") || !strings.Contains(last.text, "/help") {
		t.Errorf("/help should list commands, got %q", last.text)
	}

	m, cmd := submitInput(t, m, "/nope")
	if cmd != nil {
		t.Fatal("an unknown command should not run a turn")
	}
	if last := m.lines[len(m.lines)-1]; !strings.Contains(last.text, "unknown command") {
		t.Errorf("expected unknown-command note, got %q", last.text)
	}
	if m.running {
		t.Fatal("a slash command must not start an agent turn")
	}
}

func TestModelArrowKeysScrollTranscript(t *testing.T) {
	m := chatModel(t, &fakeSession{})
	for range 40 {
		m.appendLine(line{kind: lineSystem, text: "transcript line"})
	}
	if !m.viewport.AtBottom() {
		t.Fatal("a fresh transcript should be pinned to the bottom")
	}

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m = next.(model)
	if m.viewport.AtBottom() {
		t.Fatal("up should scroll the transcript up, away from the bottom")
	}
	if m.running {
		t.Fatal("scrolling must not start a turn")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	if !m.viewport.AtBottom() {
		t.Fatal("down should scroll one line back to the bottom")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	m = next.(model)
	if !m.viewport.AtTop() {
		t.Fatal("home should jump to the top of the transcript")
	}

	next, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m = next.(model)
	if !m.viewport.AtBottom() {
		t.Fatal("end should jump to the bottom of the transcript")
	}
}

func TestModelCtrlDQuits(t *testing.T) {
	m := chatModel(t, &fakeSession{})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+d should produce a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+d should quit, got %T", cmd())
	}
}

func TestModelViewDoesNotPanic(t *testing.T) {
	m := newTestModel(&fakeSession{})
	if m.View().Content == "" {
		t.Error("connecting view is empty")
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
