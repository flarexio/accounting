package main

import (
	"context"
	"sort"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

// scriptedBookEngine is a deterministic, offline llm.ReasoningEngine for the
// book-run demo. It first proposes a JournalIntent the accounting Validator
// rejects (credits short of debits), then -- once validation feedback appears
// -- proposes a balanced intent derived from the seeded repository, exercising
// the reason -> validate -> publish -> apply -> feedback loop without an LLM.
type scriptedBookEngine struct {
	repo     accounting.LedgerRepository
	amount   int64
	currency string
}

func newScriptedBookEngine(repo accounting.LedgerRepository, amount int64, currency string) *scriptedBookEngine {
	return &scriptedBookEngine{repo: repo, amount: amount, currency: currency}
}

func (e *scriptedBookEngine) Predict(ctx context.Context, input llm.ReasoningInput) (llm.ReasoningResult[bookkeeping.Intent], error) {
	if hasValidationFeedback(input.Events) {
		intent, rationale, err := e.recover(ctx)
		if err != nil {
			return llm.ReasoningResult[bookkeeping.Intent]{}, err
		}
		return llm.ReasoningResult[bookkeeping.Intent]{
			Evidence: []llm.EvidenceRef{
				{Source: "validator", Fact: "previous intent failed validation; rebalancing credit to match debit"},
			},
			Rationale: rationale,
			Intent:    postIntent(intent),
		}, nil
	}

	intent, rationale, err := e.firstAttempt(ctx)
	if err != nil {
		return llm.ReasoningResult[bookkeeping.Intent]{}, err
	}
	return llm.ReasoningResult[bookkeeping.Intent]{
		Evidence: []llm.EvidenceRef{
			{Source: "scenario", Fact: "first attempt: drafted from the bookkeeping request"},
		},
		Rationale: rationale,
		Intent:    postIntent(intent),
	}, nil
}

func postIntent(intent accounting.JournalIntent) bookkeeping.Intent {
	return bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal, Post: &intent}
}

func (e *scriptedBookEngine) firstAttempt(ctx context.Context) (accounting.JournalIntent, string, error) {
	intent, err := e.balancedIntent(ctx)
	if err != nil {
		return accounting.JournalIntent{}, "", err
	}
	if len(intent.Lines) >= 2 {
		intent.Lines[1].Amount = intent.Lines[0].Amount - intent.Lines[0].Amount/10
	}
	return intent, "first attempt: misread the bill; credit short of debit", nil
}

func (e *scriptedBookEngine) recover(ctx context.Context) (accounting.JournalIntent, string, error) {
	intent, err := e.balancedIntent(ctx)
	if err != nil {
		return accounting.JournalIntent{}, "", err
	}
	return intent, "recover: rebalance credit to match debit", nil
}

func (e *scriptedBookEngine) balancedIntent(ctx context.Context) (accounting.JournalIntent, error) {
	period, err := firstOpenPeriod(ctx, e.repo)
	if err != nil {
		return accounting.JournalIntent{}, err
	}
	expense, err := firstActiveAccount(ctx, e.repo, accounting.AccountExpense)
	if err != nil {
		return accounting.JournalIntent{}, err
	}
	liability, err := firstActiveAccount(ctx, e.repo, accounting.AccountLiability)
	if err != nil {
		return accounting.JournalIntent{}, err
	}

	dims := accounting.Dimensions{}
	branch, err := firstBranch(ctx, e.repo)
	if err != nil {
		return accounting.JournalIntent{}, err
	}
	if branch != "" {
		dims.BranchID = branch
	}

	return accounting.JournalIntent{
		Date:        period.Start,
		PeriodID:    period.ID,
		Currency:    e.currency,
		Description: "Demo bookkeeping post",
		Lines: []accounting.JournalLine{
			{
				AccountCode: expense,
				Side:        accounting.SideDebit,
				Amount:      e.amount,
				Memo:        "Demo expense",
				Dimensions:  dims,
			},
			{
				AccountCode: liability,
				Side:        accounting.SideCredit,
				Amount:      e.amount,
				Memo:        "Demo liability",
				Dimensions:  dims,
			},
		},
	}, nil
}

func firstActiveAccount(ctx context.Context, repo accounting.LedgerRepository, t accounting.AccountType) (string, error) {
	accounts, err := repo.Accounts(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })
	for _, a := range accounts {
		if a.Type == t && a.Active {
			return a.Code, nil
		}
	}
	return "", nil
}

func firstOpenPeriod(ctx context.Context, repo accounting.LedgerRepository) (accounting.Period, error) {
	periods, err := repo.Periods(ctx)
	if err != nil {
		return accounting.Period{}, err
	}
	sort.Slice(periods, func(i, j int) bool { return periods[i].ID < periods[j].ID })
	for _, p := range periods {
		if p.Status == accounting.PeriodOpen {
			return p, nil
		}
	}
	return accounting.Period{}, nil
}

func firstBranch(ctx context.Context, repo accounting.LedgerRepository) (string, error) {
	branches, err := repo.Branches(ctx)
	if err != nil {
		return "", err
	}
	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	if len(branches) > 0 {
		return branches[0].ID, nil
	}
	return "", nil
}

func hasValidationFeedback(events []llm.CycleEvent) bool {
	for _, ev := range events {
		if ev.Kind == llm.EventValidationError || ev.Kind == llm.EventExecutionError {
			return true
		}
	}
	return false
}
