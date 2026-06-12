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

func stagingLedger(t *testing.T) (accounting.LedgerRepository, bookkeeping.EventBus) {
	t.Helper()
	repo := memory.NewAccountingRepository()
	if err := closingScenario().Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, &bookkeeping.ApplyJournal{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return repo, bus
}

func stagingClock() bookkeeping.Clock {
	return func() time.Time { return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC) }
}

func cashSale(amount int64) accounting.JournalIntent {
	return accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 12),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "cash sale",
		Lines: []accounting.JournalLine{
			{AccountCode: "1000", Side: accounting.SideDebit, Amount: amount, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: amount, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	}
}

func TestStaging_BuffersUntilCommit(t *testing.T) {
	ctx := context.Background()
	repo, bus := stagingLedger(t)
	st := bookkeeping.NewStaging(repo, bus)
	uc := bookkeeping.PostJournal{Repo: st.Repo(), Publisher: st.Publisher(), Clock: stagingClock()}

	if _, err := uc.Handle(ctx, cashSale(100)); err != nil {
		t.Fatalf("stage entry 1: %v", err)
	}
	if _, err := uc.Handle(ctx, cashSale(200)); err != nil {
		t.Fatalf("stage entry 2: %v", err)
	}

	if st.Pending() != 2 {
		t.Fatalf("expected 2 buffered events, got %d", st.Pending())
	}
	if n, _ := repo.EntryCount(ctx); n != 0 {
		t.Fatalf("nothing should be projected before commit, got EntryCount %d", n)
	}
}

func TestStaging_CommitNumbersBatchDensely(t *testing.T) {
	ctx := context.Background()
	repo, bus := stagingLedger(t)
	st := bookkeeping.NewStaging(repo, bus)
	uc := bookkeeping.PostJournal{Repo: st.Repo(), Publisher: st.Publisher(), Clock: stagingClock()}

	for _, amt := range []int64{100, 200, 300} {
		if _, err := uc.Handle(ctx, cashSale(amt)); err != nil {
			t.Fatalf("stage %d: %v", amt, err)
		}
	}
	if err := st.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	entries, err := repo.Entries(ctx)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries after commit, got %d", len(entries))
	}
	for i, want := range []string{"JE-0001", "JE-0002", "JE-0003"} {
		if entries[i].ID != want {
			t.Errorf("entry %d id = %s, want %s (dense, contiguous batch)", i, entries[i].ID, want)
		}
	}
	if st.Pending() != 0 {
		t.Errorf("buffer should be empty after commit, got %d", st.Pending())
	}
}

func TestStaging_AbortPublishesNothing(t *testing.T) {
	ctx := context.Background()
	repo, bus := stagingLedger(t)
	st := bookkeeping.NewStaging(repo, bus)
	uc := bookkeeping.PostJournal{Repo: st.Repo(), Publisher: st.Publisher(), Clock: stagingClock()}

	if _, err := uc.Handle(ctx, cashSale(100)); err != nil {
		t.Fatalf("stage: %v", err)
	}
	st.Abort()

	if st.Pending() != 0 {
		t.Errorf("abort should clear the buffer, got %d", st.Pending())
	}
	if n, _ := repo.EntryCount(ctx); n != 0 {
		t.Errorf("abort must publish nothing, got EntryCount %d", n)
	}
}

func TestStaging_ReverseThenRepostInOneBatch(t *testing.T) {
	ctx := context.Background()
	repo, bus := stagingLedger(t)

	// JE-0001 already on the ledger (posted normally, not staged).
	first := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: stagingClock()}
	if _, err := first.Handle(ctx, cashSale(105)); err != nil {
		t.Fatalf("seed first entry: %v", err)
	}

	st := bookkeeping.NewStaging(repo, bus)
	reverse := bookkeeping.ReverseJournal{Repo: st.Repo(), Publisher: st.Publisher(), Clock: stagingClock()}
	repost := bookkeeping.PostJournal{Repo: st.Repo(), Publisher: st.Publisher(), Clock: stagingClock()}

	if _, err := reverse.Handle(ctx, bookkeeping.ReverseIntent{EntryID: "JE-0001", Reason: accounting.ReasonAmountError}); err != nil {
		t.Fatalf("stage reversal: %v", err)
	}
	if _, err := repost.Handle(ctx, cashSale(95)); err != nil {
		t.Fatalf("stage repost: %v", err)
	}
	if err := st.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	entries, err := repo.Entries(ctx)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	ids := map[string]bool{}
	for _, e := range entries {
		ids[e.ID] = true
	}
	for _, want := range []string{"JE-0001", "JE-0002", "JE-0003"} {
		if !ids[want] {
			t.Errorf("missing %s; got %v (reverse JE-0002 + repost JE-0003 should be dense)", want, ids)
		}
	}
	rels, err := repo.RelationsTo(ctx, "JE-0001")
	if err != nil {
		t.Fatalf("relations: %v", err)
	}
	if len(rels) != 1 || rels[0].FromEntry != "JE-0002" || rels[0].Type != accounting.RelationReverses {
		t.Errorf("reversal relation not committed: %+v", rels)
	}
}
