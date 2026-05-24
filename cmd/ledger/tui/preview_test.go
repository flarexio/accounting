package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/stoa/llm"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

func postJournalEventContent(t *testing.T, intent accounting.JournalIntent) string {
	t.Helper()
	raw, err := json.Marshal(bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal, Post: &intent})
	if err != nil {
		t.Fatalf("marshal intent: %v", err)
	}
	return fmt.Sprintf("rationale: balanced sale\nintent: %s", raw)
}

func sampleIntent() accounting.JournalIntent {
	return accounting.JournalIntent{
		Date:        time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PeriodID:    "2026-05",
		Currency:    "TWD",
		Description: "現金銷售",
		Lines: []accounting.JournalLine{
			{AccountCode: "1101", Side: accounting.SideDebit, Amount: 105000},
			{AccountCode: "4101", Side: accounting.SideCredit, Amount: 100000},
			{AccountCode: "2191", Side: accounting.SideCredit, Amount: 5000},
		},
	}
}

var sampleAccountNames = map[string]string{
	"1101": "庫存現金",
	"4101": "銷貨收入",
	"2191": "銷項稅額",
}

func sampleAccountResolver(code string) string { return sampleAccountNames[code] }

func TestParseIntentReturnsFalseWithoutMarker(t *testing.T) {
	if _, ok := parseIntent("rationale: just thinking\ntool calls:\n  find_accounts {}"); ok {
		t.Error("tool-call output should not parse as an intent")
	}
	if _, ok := parseIntent("plain text without marker"); ok {
		t.Error("unmarked content should not parse as an intent")
	}
}

func TestRenderJournalPreviewLaysOutDebitsAndCredits(t *testing.T) {
	intent := sampleIntent()
	parsed, ok := parseIntent(postJournalEventContent(t, intent))
	if !ok || parsed.Kind != bookkeeping.IntentPostJournal || parsed.Post == nil {
		t.Fatalf("parseIntent did not yield a post_journal intent: ok=%v intent=%+v", ok, parsed)
	}
	preview := renderJournalPreview(parsed.Post, sampleAccountResolver)
	if preview == "" {
		t.Fatal("expected non-empty preview for post_journal intent")
	}

	lines := strings.Split(preview, "\n")
	if len(lines) != 4 {
		t.Fatalf("preview has %d lines, want 4 (header + 3 entries)", len(lines))
	}
	if !strings.Contains(lines[0], "2026-05-10") || !strings.Contains(lines[0], "TWD") {
		t.Errorf("header missing date/currency: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "  1101 庫存現金") {
		t.Errorf("debit line should show code + name flush-left, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "105,000") {
		t.Errorf("debit amount not formatted with thousands separator: %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "       4101 銷貨收入") {
		t.Errorf("credit line should show code + name indented, got %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "       2191 銷項稅額") {
		t.Errorf("credit line should show code + name indented, got %q", lines[3])
	}
}

func TestRenderJournalPreviewFallsBackToCodeWhenNoResolver(t *testing.T) {
	intent := sampleIntent()
	preview := renderJournalPreview(&intent, nil)
	if strings.Contains(preview, "庫存現金") {
		t.Errorf("expected no account name with nil resolver, got %q", preview)
	}
	if !strings.Contains(preview, "1101") {
		t.Errorf("expected account code in preview, got %q", preview)
	}
}

func TestModelAppendsPreviewAfterPostJournalEvent(t *testing.T) {
	fake := &fakeSession{
		events: []llm.CycleEvent{{
			Kind:    llm.EventModelOutput,
			Content: postJournalEventContent(t, sampleIntent()),
		}},
		outcome: Outcome{Turns: 1},
	}
	m := chatModel(t, fake)
	m.input.SetValue("post sale")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = driveTurn(t, m)

	wantKinds := []lineKind{lineUser, lineModel, linePreview, lineSystem}
	if len(m.lines) != len(wantKinds) {
		t.Fatalf("transcript has %d lines, want %d (%v)", len(m.lines), len(wantKinds), wantKinds)
	}
	for i, want := range wantKinds {
		if m.lines[i].kind != want {
			t.Errorf("line %d kind = %v, want %v", i, m.lines[i].kind, want)
		}
	}
	if !strings.Contains(m.lines[2].text, "1101") || !strings.Contains(m.lines[2].text, "105,000") {
		t.Errorf("preview missing line content: %q", m.lines[2].text)
	}
}

func TestModelAppendsPreviewForEachAdjustment(t *testing.T) {
	fake := &fakeSession{
		events: []llm.CycleEvent{
			{Kind: llm.EventModelOutput, Content: postJournalEventContent(t, sampleIntent())},
			{Kind: llm.EventValidationError, Content: "branch_id missing"},
			{Kind: llm.EventModelOutput, Content: postJournalEventContent(t, sampleIntent())},
		},
		outcome: Outcome{Turns: 2},
	}
	m := chatModel(t, fake)
	m.input.SetValue("post sale")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = driveTurn(t, m)

	previews := 0
	for _, l := range m.lines {
		if l.kind == linePreview {
			previews++
		}
	}
	if previews != 2 {
		t.Errorf("got %d previews, want 2 (one per model_output)", previews)
	}
}

// lookupSession is a fakeSession that also satisfies EntryLookup + AccountLookup.
type lookupSession struct {
	*fakeSession
	entries  map[string]accounting.JournalEntry
	accounts map[string]accounting.Account
	calls    []string
}

func (s *lookupSession) LookupEntry(_ context.Context, entryID string) (accounting.JournalEntry, bool, error) {
	s.calls = append(s.calls, entryID)
	entry, ok := s.entries[entryID]
	return entry, ok, nil
}

func (s *lookupSession) LookupAccount(_ context.Context, code string) (accounting.Account, bool, error) {
	acc, ok := s.accounts[code]
	return acc, ok, nil
}

func sampleAccounts() map[string]accounting.Account {
	out := make(map[string]accounting.Account, len(sampleAccountNames))
	for code, name := range sampleAccountNames {
		out[code] = accounting.Account{Code: code, Name: name, Active: true}
	}
	return out
}

func reverseJournalEventContent(t *testing.T, entryID, reason string) string {
	t.Helper()
	raw, err := json.Marshal(bookkeeping.Intent{
		Kind:    bookkeeping.IntentReverseJournal,
		Reverse: &bookkeeping.ReverseIntent{EntryID: entryID, Reason: reason},
	})
	if err != nil {
		t.Fatalf("marshal intent: %v", err)
	}
	return fmt.Sprintf("rationale: mistake to fix\nintent: %s", raw)
}

func TestRenderReversePreviewFlipsSides(t *testing.T) {
	orig := accounting.JournalEntry{
		ID:          "JE-0001",
		Date:        time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PeriodID:    "2026-05",
		Currency:    "TWD",
		Description: "現金銷售",
		Lines:       sampleIntent().Lines,
	}
	preview := renderReversePreview(orig, "過帳錯誤", sampleAccountResolver)

	lines := strings.Split(preview, "\n")
	if len(lines) != 4 {
		t.Fatalf("preview has %d lines, want 4", len(lines))
	}
	if !strings.Contains(lines[0], "Reversal of JE-0001") || !strings.Contains(lines[0], "過帳錯誤") {
		t.Errorf("header missing reversal hint: %q", lines[0])
	}
	// Original was Dr,Cr,Cr; flipped is Cr,Dr,Dr but the preview must show
	// debits first per accounting convention: Dr 4101, Dr 2191, then Cr 1101.
	if !strings.HasPrefix(lines[1], "  4101 銷貨收入") {
		t.Errorf("line 1 should be debit (flush-left) with name, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "  2191 銷項稅額") {
		t.Errorf("line 2 should be debit (flush-left) with name, got %q", lines[2])
	}
	if !strings.HasPrefix(lines[3], "       1101 庫存現金") {
		t.Errorf("line 3 should be credit (indented) with name, got %q", lines[3])
	}
}

func TestRenderJournalPreviewSortsDebitsBeforeCredits(t *testing.T) {
	intent := accounting.JournalIntent{
		Date:     time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PeriodID: "2026-05",
		Currency: "TWD",
		Lines: []accounting.JournalLine{
			{AccountCode: "4101", Side: accounting.SideCredit, Amount: 100},
			{AccountCode: "1101", Side: accounting.SideDebit, Amount: 100},
		},
	}
	preview := renderJournalPreview(&intent, nil)
	lines := strings.Split(preview, "\n")
	if !strings.HasPrefix(lines[1], "  1101") {
		t.Errorf("debit should sort to first line regardless of input order, got %q", lines[1])
	}
	if !strings.HasPrefix(lines[2], "       4101") {
		t.Errorf("credit should sort after debits, got %q", lines[2])
	}
}

func TestModelAppendsPreviewForReverseJournal(t *testing.T) {
	orig := accounting.JournalEntry{
		ID:          "JE-0001",
		Date:        time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PeriodID:    "2026-05",
		Currency:    "TWD",
		Description: "現金銷售",
		Lines:       sampleIntent().Lines,
	}
	session := &lookupSession{
		fakeSession: &fakeSession{
			events: []llm.CycleEvent{{
				Kind:    llm.EventModelOutput,
				Content: reverseJournalEventContent(t, "JE-0001", "過帳錯誤"),
			}},
			outcome: Outcome{Turns: 1, Summary: "posted entry JE-0002"},
		},
		entries:  map[string]accounting.JournalEntry{"JE-0001": orig},
		accounts: sampleAccounts(),
	}
	m := chatModel(t, session)
	m.input.SetValue("reverse JE-0001")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = driveTurn(t, m)

	if len(session.calls) != 1 || session.calls[0] != "JE-0001" {
		t.Errorf("LookupEntry calls = %v, want [JE-0001]", session.calls)
	}
	previews := 0
	for _, l := range m.lines {
		if l.kind == linePreview {
			previews++
		}
	}
	if previews != 1 {
		t.Fatalf("got %d previews, want 1 for reverse_journal", previews)
	}
	last := m.lines[len(m.lines)-2] // last is lineSystem (summary)
	if last.kind != linePreview {
		t.Fatalf("expected linePreview just before system summary, got %v", last.kind)
	}
	if !strings.Contains(last.text, "Reversal of JE-0001") {
		t.Errorf("reverse preview missing header: %q", last.text)
	}
}

func TestModelSkipsReversePreviewWhenSessionLacksLookup(t *testing.T) {
	fake := &fakeSession{
		events: []llm.CycleEvent{{
			Kind:    llm.EventModelOutput,
			Content: reverseJournalEventContent(t, "JE-0001", ""),
		}},
		outcome: Outcome{Turns: 1},
	}
	m := chatModel(t, fake)
	m.input.SetValue("reverse JE-0001")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	m = driveTurn(t, m)

	for _, l := range m.lines {
		if l.kind == linePreview {
			t.Fatalf("did not expect a preview line without lookup support, got %q", l.text)
		}
	}
}

func TestFormatAmountThousandsSeparator(t *testing.T) {
	cases := map[int64]string{
		0:       "0",
		7:       "7",
		1000:    "1,000",
		105000:  "105,000",
		1234567: "1,234,567",
	}
	for in, want := range cases {
		if got := formatAmount(in); got != want {
			t.Errorf("formatAmount(%d) = %q, want %q", in, got, want)
		}
	}
}
