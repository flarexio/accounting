package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

func sampleEntry(id string, seq uint64) accounting.JournalPosted {
	return accounting.JournalPosted{
		Subject:  "accounting.journal",
		Sequence: seq,
		Entry: accounting.JournalEntry{
			ID:          id,
			Date:        time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
			PeriodID:    "2026-05",
			Currency:    "USD",
			Description: "AWS bill",
			Lines: []accounting.JournalLine{
				{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
				{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
			},
			PostedAt: time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC),
		},
	}
}

func TestRepository_ApplyAndRead(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	if err := repo.Apply(ctx, sampleEntry("JE-0001", 1)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, ok, err := repo.Entry(ctx, "JE-0001")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !ok {
		t.Fatal("expected applied entry to be readable")
	}
	if got.ID != "JE-0001" || got.Currency != "USD" || len(got.Lines) != 2 {
		t.Fatalf("unexpected entry: %+v", got)
	}
}

func TestRepository_AppliedEntryCannotBeMutatedThroughReturnedValue(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	if err := repo.Apply(ctx, sampleEntry("JE-0001", 1)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, _, _ := repo.Entry(ctx, "JE-0001")
	got.Lines[0].Amount = 1
	got.Lines[0].AccountCode = "tampered"

	stored, _, _ := repo.Entry(ctx, "JE-0001")
	if stored.Lines[0].Amount != 10000 || stored.Lines[0].AccountCode != "5200" {
		t.Fatalf("stored entry was mutated through returned value: %+v", stored.Lines[0])
	}

	listed, _ := repo.Entries(ctx)
	listed[0].Lines[0].Amount = 1
	stored2, _, _ := repo.Entry(ctx, "JE-0001")
	if stored2.Lines[0].Amount != 10000 {
		t.Fatal("stored entry was mutated through Entries() snapshot")
	}
}

func TestRepository_AppliedEntryIsolatedFromEventLines(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	evt := sampleEntry("JE-0001", 1)
	if err := repo.Apply(ctx, evt); err != nil {
		t.Fatalf("apply: %v", err)
	}

	evt.Entry.Lines[0].Amount = 1

	stored, _, _ := repo.Entry(ctx, "JE-0001")
	if stored.Lines[0].Amount != 10000 {
		t.Fatalf("stored entry was mutated through the originating event: %+v", stored.Lines[0])
	}
}

func TestRepository_LastSequenceTracksPerSubject(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()

	if got, _ := repo.LastSequence(ctx, "accounting.journal"); got != 0 {
		t.Fatalf("expected zero LastSequence before any apply, got %d", got)
	}

	if err := repo.Apply(ctx, sampleEntry("JE-0001", 1)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Apply(ctx, sampleEntry("JE-0002", 2)); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.LastSequence(ctx, "accounting.journal")
	if got != 2 {
		t.Fatalf("expected LastSequence=2 after two posts, got %d", got)
	}
	if other, _ := repo.LastSequence(ctx, "some.other.subject"); other != 0 {
		t.Fatalf("expected LastSequence=0 for unrelated subject, got %d", other)
	}
}

func TestRepository_SeedAndListings(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	scenario, err := accounting.LoadScenarioFile("../../seed/aws_bill.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	accounts, err := repo.Accounts(ctx)
	if err != nil || len(accounts) == 0 {
		t.Fatalf("expected accounts seeded, got %d err=%v", len(accounts), err)
	}
	periods, err := repo.Periods(ctx)
	if err != nil || len(periods) == 0 {
		t.Fatalf("expected periods seeded, got %d err=%v", len(periods), err)
	}
	branches, err := repo.Branches(ctx)
	if err != nil || len(branches) == 0 {
		t.Fatalf("expected branches seeded, got %d err=%v", len(branches), err)
	}
}
