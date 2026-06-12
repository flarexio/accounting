package benchmark_test

import (
	"context"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/benchmark"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

type fakeEngine struct {
	intent bookkeeping.Intent
}

func (f fakeEngine) Predict(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
	return llm.IntentOutput(f.intent, nil, "test"), nil
}

func goldIntent() bookkeeping.Intent {
	return bookkeeping.Intent{
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
		Final: true,
	}
}

func TestRunner_ScoresPerfectRunAcrossRepeats(t *testing.T) {
	c, err := benchmark.LoadCaseFile("../seed/bench/aws_bill_basic_payment.case.yaml")
	if err != nil {
		t.Fatalf("load case: %v", err)
	}

	runner := benchmark.Runner{
		Cases:           []*benchmark.Case{c},
		Models:          []benchmark.ModelConfig{{Name: "fake", Model: "fake"}},
		DefaultMaxTurns: 3,
		Repeats:         2,
		Engine: func(_ context.Context, _ accounting.LedgerRepository, _ benchmark.ModelConfig) (llm.ReasoningEngine[bookkeeping.Intent], error) {
			return fakeEngine{intent: goldIntent()}, nil
		},
	}
	results, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (1 case * 1 model * 2 repeats), got %d", len(results))
	}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("run error: %s", r.Error)
		}
		if !r.Score.KindMatch || !r.Score.PayloadMatch || !r.Score.ValidationClean {
			t.Fatalf("expected all axes true, got %+v", r.Score)
		}
		if r.Entry.ID == "" {
			t.Fatalf("expected entry to be posted, got empty entry")
		}
	}

	report := benchmark.BuildReport(results, 1, 1, runner.Repeats)
	if len(report.Aggregate) != 1 {
		t.Fatalf("expected 1 aggregate row, got %d", len(report.Aggregate))
	}
	row := report.Aggregate[0]
	if row.KindRate != 1.0 || row.PayloadRate != 1.0 || row.ValidationCleanRate != 1.0 {
		t.Fatalf("expected perfect aggregate rates, got %+v", row)
	}
	if row.Iterations != 2 {
		t.Fatalf("iterations: %d", row.Iterations)
	}
}

func TestRunner_MixesGoodAndBadModels(t *testing.T) {
	c, err := benchmark.LoadCaseFile("../seed/bench/aws_bill_basic_payment.case.yaml")
	if err != nil {
		t.Fatalf("load case: %v", err)
	}

	badIntent := goldIntent()
	badIntent.Post.Lines[1].Amount = 999999

	runner := benchmark.Runner{
		Cases:           []*benchmark.Case{c},
		Models:          []benchmark.ModelConfig{{Name: "good", Model: "good"}, {Name: "bad", Model: "bad"}},
		DefaultMaxTurns: 3,
		Repeats:         1,
		Engine: func(_ context.Context, _ accounting.LedgerRepository, m benchmark.ModelConfig) (llm.ReasoningEngine[bookkeeping.Intent], error) {
			if m.Name == "good" {
				return fakeEngine{intent: goldIntent()}, nil
			}
			return fakeEngine{intent: badIntent}, nil
		},
	}
	results, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results: %d", len(results))
	}

	report := benchmark.BuildReport(results, 1, 2, runner.Repeats)
	if len(report.Aggregate) != 2 {
		t.Fatalf("aggregate rows: %d", len(report.Aggregate))
	}
	rates := map[string]float64{}
	for _, row := range report.Aggregate {
		rates[row.Model] = row.PayloadRate
	}
	if rates["good"] != 1.0 {
		t.Fatalf("good payload rate: %v", rates["good"])
	}
	if rates["bad"] != 0.0 {
		t.Fatalf("bad payload rate: %v", rates["bad"])
	}
}
