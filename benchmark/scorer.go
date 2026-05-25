package benchmark

import (
	"fmt"
	"sort"
	"strings"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

// Score is the per-run grading across deterministic axes. Notes describe
// every mismatch so the report can explain why a result lost points.
type Score struct {
	KindMatch       bool     `json:"kind_match"`
	PayloadMatch    bool     `json:"payload_match"`
	ValidationClean bool     `json:"validation_clean"`
	Turns           int      `json:"turns"`
	Notes           []string `json:"notes,omitempty"`
}

// Compare grades result against gold. The model's proposal (result.Intent)
// drives the comparison; result.Entry is ignored because validation failures
// leave it empty and would distort the kind and payload signals.
// validation_clean is the separate axis for whether any validation_error or
// execution_error events appeared during the cycle.
func Compare(result agent.Result, gold Gold) Score {
	s := Score{Turns: result.Turns, ValidationClean: !hasErrorEvent(result.Events)}
	s.KindMatch = result.Intent.Kind == gold.Kind
	if !s.KindMatch {
		s.Notes = append(s.Notes, fmt.Sprintf("kind: want %q, got %q", gold.Kind, result.Intent.Kind))
		return s
	}
	switch gold.Kind {
	case bookkeeping.IntentPostJournal:
		s.PayloadMatch, s.Notes = comparePost(result.Intent.Post, gold.Post, s.Notes)
	case bookkeeping.IntentReverseJournal:
		s.PayloadMatch, s.Notes = compareReverse(result.Intent.Reverse, gold.Reverse, s.Notes)
	case bookkeeping.IntentReject:
		s.PayloadMatch = true
	}
	return s
}

func comparePost(got *accounting.JournalIntent, want *GoldPost, notes []string) (bool, []string) {
	if got == nil {
		return false, append(notes, "payload: post_journal payload missing")
	}
	ok := true
	if got.PeriodID != want.PeriodID {
		ok = false
		notes = append(notes, fmt.Sprintf("payload: period_id want %q, got %q", want.PeriodID, got.PeriodID))
	}
	if got.Currency != want.Currency {
		ok = false
		notes = append(notes, fmt.Sprintf("payload: currency want %q, got %q", want.Currency, got.Currency))
	}
	gotKeys := make([]goldLineKey, len(got.Lines))
	for i, l := range got.Lines {
		gotKeys[i] = lineKey(l)
	}
	wantKeys := make([]goldLineKey, len(want.Lines))
	for i, l := range want.Lines {
		wantKeys[i] = l.key()
	}
	if !lineSetsEqual(gotKeys, wantKeys) {
		ok = false
		notes = append(notes, fmt.Sprintf("payload: lines want %s, got %s",
			formatLineKeys(sortedLineKeys(wantKeys)),
			formatLineKeys(sortedLineKeys(gotKeys))))
	}
	return ok, notes
}

func compareReverse(got *bookkeeping.ReverseIntent, want *GoldReverse, notes []string) (bool, []string) {
	if got == nil {
		return false, append(notes, "payload: reverse_journal payload missing")
	}
	if got.EntryID != want.EntryID {
		return false, append(notes, fmt.Sprintf("payload: reverse entry_id want %q, got %q", want.EntryID, got.EntryID))
	}
	return true, notes
}

// lineSetsEqual treats lines as a multiset on (account_code, side, amount, branch_id).
func lineSetsEqual(a, b []goldLineKey) bool {
	if len(a) != len(b) {
		return false
	}
	as := sortedLineKeys(a)
	bs := sortedLineKeys(b)
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

func hasErrorEvent(events []llm.CycleEvent) bool {
	for _, e := range events {
		if e.Kind == llm.EventValidationError || e.Kind == llm.EventExecutionError {
			return true
		}
	}
	return false
}

func sortedLineKeys(keys []goldLineKey) []goldLineKey {
	out := make([]goldLineKey, len(keys))
	copy(out, keys)
	sort.Slice(out, func(i, j int) bool {
		if out[i].AccountCode != out[j].AccountCode {
			return out[i].AccountCode < out[j].AccountCode
		}
		if out[i].Side != out[j].Side {
			return out[i].Side < out[j].Side
		}
		if out[i].Amount != out[j].Amount {
			return out[i].Amount < out[j].Amount
		}
		return out[i].BranchID < out[j].BranchID
	})
	return out
}

func formatLineKeys(keys []goldLineKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s/%s/%d@%s", k.AccountCode, k.Side, k.Amount, k.BranchID)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
