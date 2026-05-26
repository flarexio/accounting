package benchmark_test

import (
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/benchmark"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

func awsBillGold() benchmark.Gold {
	return benchmark.Gold{
		Kind: bookkeeping.IntentPostJournal,
		Post: &benchmark.GoldPost{
			PeriodID: "2026-05",
			Currency: "USD",
			Lines: []benchmark.GoldLine{
				{AccountCode: "5200", Side: accounting.SideDebit, Amount: 120000, BranchID: "hq"},
				{AccountCode: "2100", Side: accounting.SideCredit, Amount: 120000, BranchID: "hq"},
			},
		},
	}
}

func awsBillResult() agent.Result {
	return agent.Result{
		Intent: bookkeeping.Intent{
			Kind: bookkeeping.IntentPostJournal,
			Post: &accounting.JournalIntent{
				Date:        accounting.NewDate(2026, 5, 12),
				PeriodID:    "2026-05",
				Currency:    "USD",
				Description: "AWS bill",
				Lines: []accounting.JournalLine{
					{AccountCode: "5200", Side: accounting.SideDebit, Amount: 120000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
					{AccountCode: "2100", Side: accounting.SideCredit, Amount: 120000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
				},
			},
		},
		Turns: 1,
	}
}

func TestCompare_PerfectPostJournal(t *testing.T) {
	got := benchmark.Compare(awsBillResult(), awsBillGold())
	if !got.KindMatch || !got.PayloadMatch || !got.ValidationClean {
		t.Fatalf("expected all axes true, got %+v", got)
	}
	if got.Turns != 1 {
		t.Fatalf("turns: %d", got.Turns)
	}
}

func TestCompare_KindMismatchShortCircuits(t *testing.T) {
	res := awsBillResult()
	res.Intent.Kind = bookkeeping.IntentReject
	res.Intent.Post = nil
	res.Intent.Reject = &bookkeeping.RejectIntent{Reason: "no"}

	got := benchmark.Compare(res, awsBillGold())
	if got.KindMatch || got.PayloadMatch {
		t.Fatalf("expected kind+payload false, got %+v", got)
	}
	if len(got.Notes) == 0 {
		t.Fatal("expected a note explaining the kind mismatch")
	}
}

func TestCompare_LinesAreSetEqual(t *testing.T) {
	res := awsBillResult()
	res.Intent.Post.Lines[0], res.Intent.Post.Lines[1] = res.Intent.Post.Lines[1], res.Intent.Post.Lines[0]
	got := benchmark.Compare(res, awsBillGold())
	if !got.PayloadMatch {
		t.Fatalf("expected order-insensitive payload match, got %+v", got)
	}
}

func TestCompare_LinesDifferentAmountIsMismatch(t *testing.T) {
	res := awsBillResult()
	res.Intent.Post.Lines[1].Amount = 99999
	got := benchmark.Compare(res, awsBillGold())
	if got.PayloadMatch {
		t.Fatalf("expected payload mismatch on amount diff, got %+v", got)
	}
}

func TestCompare_PeriodAndCurrencyMustMatch(t *testing.T) {
	res := awsBillResult()
	res.Intent.Post.PeriodID = "2026-06"
	got := benchmark.Compare(res, awsBillGold())
	if got.PayloadMatch {
		t.Fatalf("expected payload mismatch on period diff, got %+v", got)
	}
}

func TestCompare_ValidationErrorTaintsCleanFlag(t *testing.T) {
	res := awsBillResult()
	res.Events = []llm.CycleEvent{{Kind: llm.EventValidationError, Content: "unbalanced"}}
	got := benchmark.Compare(res, awsBillGold())
	if got.ValidationClean {
		t.Fatalf("expected validation_clean=false, got %+v", got)
	}
}

func TestCompare_RejectMatchesOnKindAlone(t *testing.T) {
	res := agent.Result{Intent: bookkeeping.Intent{Kind: bookkeeping.IntentReject, Reject: &bookkeeping.RejectIntent{Reason: "closed"}}, Turns: 1}
	gold := benchmark.Gold{Kind: bookkeeping.IntentReject, Reject: &benchmark.GoldReject{}}
	got := benchmark.Compare(res, gold)
	if !got.KindMatch || !got.PayloadMatch {
		t.Fatalf("reject should match on kind alone, got %+v", got)
	}
}
