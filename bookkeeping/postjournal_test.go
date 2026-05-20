package bookkeeping_test

import (
	"context"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
)

func ledgerScenario() accounting.Scenario {
	return accounting.Scenario{
		Company: accounting.Company{ID: "acme", Name: "Acme Co."},
		Accounts: []accounting.Account{
			{Code: "5200", Name: "Cloud Infrastructure", Type: accounting.AccountExpense, Active: true},
			{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
		},
		Periods: []accounting.Period{
			{
				ID:     "2026-05",
				Start:  time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
				End:    time.Date(2026, 5, 31, 23, 59, 59, 0, time.UTC),
				Status: accounting.PeriodOpen,
			},
		},
	}
}

// seededLedger wires the same publish -> handler -> Apply path the CLI uses.
func seededLedger(t *testing.T) (accounting.LedgerRepository, bookkeeping.EventBus) {
	t.Helper()
	repo := memory.NewAccountingRepository()
	if err := ledgerScenario().Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := inproc.NewAccountingBus()
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return repo, bus
}

func balancedIntent() accounting.JournalIntent {
	return accounting.JournalIntent{
		Date:        time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Paid cloud bill on company credit card",
		Lines: []accounting.JournalLine{
			{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000},
		},
	}
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
}

func TestPostJournal_HandlePostsWithoutLLM(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)

	uc := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	entry, err := uc.Handle(ctx, balancedIntent())
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if entry.ID == "" {
		t.Fatal("expected a posted entry with an ID")
	}
	if !entry.PostedAt.Equal(fixedClock()) {
		t.Fatalf("expected PostedAt stamped from the injected clock, got %s", entry.PostedAt)
	}

	stored, err := repo.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].ID != entry.ID {
		t.Fatalf("expected one stored entry matching the returned ID, got %+v", stored)
	}
}

func TestPostJournal_HandleRejectsInvalidIntent(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)

	intent := balancedIntent()
	intent.Lines[1].Amount = 9000

	uc := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	if _, err := uc.Handle(ctx, intent); err == nil {
		t.Fatal("expected Handle to reject an unbalanced intent")
	}

	stored, err := repo.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 0 {
		t.Fatalf("expected no entry posted for an invalid intent, got %d", len(stored))
	}
}

func TestPostJournal_ValidateAndExecuteAreSeparable(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)

	uc := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	intent := balancedIntent()

	if err := uc.Validate(ctx, intent); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if stored, _ := repo.Entries(ctx); len(stored) != 0 {
		t.Fatalf("expected Validate to post nothing, got %d entries", len(stored))
	}

	entry, err := uc.Execute(ctx, intent)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stored, _ := repo.Entries(ctx); len(stored) != 1 || stored[0].ID != entry.ID {
		t.Fatalf("expected Execute to post the validated entry, got %+v", stored)
	}
}
