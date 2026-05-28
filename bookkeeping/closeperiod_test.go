package bookkeeping_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
)

func closingScenario() accounting.Scenario {
	return accounting.Scenario{
		Company: accounting.Company{
			ID:                   "acme",
			Name:                 "Acme Co.",
			TimeZone:             "UTC",
			RetainedEarningsCode: "3300",
		},
		Accounts: []accounting.Account{
			{Code: "1000", Name: "Cash", Type: accounting.AccountAsset, Active: true},
			{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
			{Code: "3300", Name: "Retained Earnings", Type: accounting.AccountEquity, Active: true},
			{Code: "4000", Name: "Service Revenue", Type: accounting.AccountRevenue, Active: true},
			{Code: "5200", Name: "Cloud Infrastructure", Type: accounting.AccountExpense, Active: true},
		},
		Branches: []accounting.Branch{{ID: "main", Name: "Main"}},
		Periods: []accounting.Period{
			{
				ID:     "2026-05",
				Start:  accounting.NewDate(2026, 5, 1),
				End:    accounting.NewDate(2026, 5, 31),
				Status: accounting.PeriodOpen,
			},
		},
	}
}

func closingLedger(t *testing.T) (accounting.LedgerRepository, bookkeeping.EventBus) {
	t.Helper()
	repo := memory.NewAccountingRepository()
	if err := closingScenario().Seed(context.Background(), repo); err != nil {
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

func postClosingActivity(t *testing.T, repo accounting.LedgerRepository, bus bookkeeping.EventBus) []accounting.JournalEntry {
	t.Helper()
	uc := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}

	revenue, err := uc.Handle(context.Background(), accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 10),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Service revenue billed",
		Lines: []accounting.JournalLine{
			{AccountCode: "1000", Side: accounting.SideDebit, Amount: 30000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: 30000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	})
	if err != nil {
		t.Fatalf("seed revenue entry: %v", err)
	}

	expense, err := uc.Handle(context.Background(), accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 12),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Paid cloud bill",
		Lines: []accounting.JournalLine{
			{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	})
	if err != nil {
		t.Fatalf("seed expense entry: %v", err)
	}
	return []accounting.JournalEntry{revenue, expense}
}

func closingClock() time.Time {
	return time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
}

func TestClosePeriod_PostsClosingEntryAndFlipsPeriod(t *testing.T) {
	ctx := context.Background()
	repo, bus := closingLedger(t)
	source := postClosingActivity(t, repo, bus)

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	result, err := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if result.AlreadyClosed {
		t.Fatalf("expected fresh close, got already_closed result")
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected one closing entry, got %d", len(result.Entries))
	}

	closing := result.Entries[0]
	if closing.PeriodID != "2026-05" {
		t.Fatalf("closing entry period %q, want %q", closing.PeriodID, "2026-05")
	}
	if closing.Date.String() != "2026-05-31" {
		t.Fatalf("closing date %s, want 2026-05-31", closing.Date)
	}
	if !strings.Contains(closing.Description, "Close period 2026-05") {
		t.Fatalf("closing description %q must reference the period", closing.Description)
	}

	var dr, cr int64
	for _, line := range closing.Lines {
		if line.Dimensions.BranchID != "main" {
			t.Fatalf("closing line branch %q, want main", line.Dimensions.BranchID)
		}
		switch line.Side {
		case accounting.SideDebit:
			dr += line.Amount
		case accounting.SideCredit:
			cr += line.Amount
		}
	}
	if dr != cr {
		t.Fatalf("closing entry not balanced: dr=%d cr=%d", dr, cr)
	}

	lineByCode := map[string]accounting.JournalLine{}
	for _, line := range closing.Lines {
		lineByCode[line.AccountCode] = line
	}
	rev, ok := lineByCode["4000"]
	if !ok || rev.Side != accounting.SideDebit || rev.Amount != 30000 {
		t.Fatalf("expected DR 30000 on revenue 4000, got %+v", rev)
	}
	exp, ok := lineByCode["5200"]
	if !ok || exp.Side != accounting.SideCredit || exp.Amount != 10000 {
		t.Fatalf("expected CR 10000 on expense 5200, got %+v", exp)
	}
	re, ok := lineByCode["3300"]
	if !ok || re.Side != accounting.SideCredit || re.Amount != 20000 {
		t.Fatalf("expected CR 20000 net income to retained earnings 3300, got %+v", re)
	}

	relations, err := repo.RelationsFrom(ctx, closing.ID)
	if err != nil {
		t.Fatalf("RelationsFrom: %v", err)
	}
	if len(relations) != len(source) {
		t.Fatalf("expected %d closes relations, got %d", len(source), len(relations))
	}
	for _, rel := range relations {
		if rel.Type != accounting.RelationCloses {
			t.Fatalf("relation type %q, want %q", rel.Type, accounting.RelationCloses)
		}
		if rel.Reason != accounting.ReasonPeriodEnd {
			t.Fatalf("relation reason %q, want %q", rel.Reason, accounting.ReasonPeriodEnd)
		}
		if rel.FromEntry != closing.ID {
			t.Fatalf("relation from %q, want %q", rel.FromEntry, closing.ID)
		}
	}

	period, ok, err := repo.Period(ctx, "2026-05")
	if err != nil || !ok {
		t.Fatalf("Period: %v ok=%t", err, ok)
	}
	if period.Status != accounting.PeriodClosed {
		t.Fatalf("period status %q, want %q", period.Status, accounting.PeriodClosed)
	}
}

func TestClosePeriod_IdempotentWhenAlreadyClosed(t *testing.T) {
	ctx := context.Background()
	repo, bus := closingLedger(t)
	postClosingActivity(t, repo, bus)

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	if _, err := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err != nil {
		t.Fatalf("first close: %v", err)
	}
	entriesAfterFirst, _ := repo.Entries(ctx)

	result, err := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"})
	if err != nil {
		t.Fatalf("second close: %v", err)
	}
	if !result.AlreadyClosed {
		t.Fatalf("expected already_closed on second invocation, got %+v", result)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("expected no new entries on second invocation, got %d", len(result.Entries))
	}

	entriesAfterSecond, _ := repo.Entries(ctx)
	if len(entriesAfterFirst) != len(entriesAfterSecond) {
		t.Fatalf("expected no new entries posted, before=%d after=%d", len(entriesAfterFirst), len(entriesAfterSecond))
	}
}

func TestClosePeriod_RefusesPeriodNotYetEnded(t *testing.T) {
	ctx := context.Background()
	repo, bus := closingLedger(t)
	postClosingActivity(t, repo, bus)

	uc := bookkeeping.ClosePeriod{
		Repo:      repo,
		Publisher: bus,
		Clock:     func() time.Time { return time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC) },
	}
	if err := uc.Validate(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err == nil {
		t.Fatal("expected Validate to refuse closing a period that has not ended in the company's tz")
	}
	if _, err := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err == nil {
		t.Fatal("expected Handle to refuse closing a period that has not ended in the company's tz")
	}
	if entries, _ := repo.Entries(ctx); len(entries) != 2 {
		t.Fatalf("expected no closing entry posted, got %d total entries", len(entries))
	}
}

func TestClosePeriod_RefusesStillOpenInCompanyTimezone(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	scenario := closingScenario()
	scenario.Company.TimeZone = "Asia/Taipei"
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := inproc.NewAccountingBus()
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	postClosingActivity(t, repo, bus)

	// 2026-05-31 23:30 UTC is already 2026-06-01 in Asia/Taipei (UTC+8) -- but
	// 2026-05-31 15:00 UTC is still 2026-05-31 23:00 in Taipei, so the period
	// has not yet ended.
	uc := bookkeeping.ClosePeriod{
		Repo:      repo,
		Publisher: bus,
		Clock:     func() time.Time { return time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC) },
	}
	if err := uc.Validate(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err == nil {
		t.Fatal("expected Validate to refuse closing when company-tz date is still inside the period")
	}
}

func TestClosePeriod_RefusesWhenNoRevenueOrExpenseActivity(t *testing.T) {
	ctx := context.Background()
	repo, bus := closingLedger(t)

	// Post only a balance-sheet entry (asset <-> liability), no rev/exp.
	post := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	if _, err := post.Handle(ctx, accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 12),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Borrow on credit card",
		Lines: []accounting.JournalLine{
			{AccountCode: "1000", Side: accounting.SideDebit, Amount: 5000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 5000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	}); err != nil {
		t.Fatalf("post balance-sheet entry: %v", err)
	}

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	if err := uc.Validate(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err == nil {
		t.Fatal("expected Validate to refuse closing when no revenue/expense activity exists")
	}
	period, _, _ := repo.Period(ctx, "2026-05")
	if period.Status != accounting.PeriodOpen {
		t.Fatalf("expected period to remain open after refused close, got %q", period.Status)
	}
}

func TestClosePeriod_RefusesUnknownPeriod(t *testing.T) {
	ctx := context.Background()
	repo, bus := closingLedger(t)

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	if err := uc.Validate(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "1999-12"}); err == nil {
		t.Fatal("expected Validate to refuse an unknown period_id")
	}
}

func TestClosePeriod_RefusesMissingRetainedEarningsCode(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	scenario := closingScenario()
	scenario.Company.RetainedEarningsCode = ""
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := inproc.NewAccountingBus()
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	postClosingActivity(t, repo, bus)

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	if err := uc.Validate(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"}); err == nil {
		t.Fatal("expected Validate to refuse closing when company has no retained_earnings_code")
	}
}

func TestClosePeriod_MultipleBranches(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	scenario := closingScenario()
	scenario.Branches = append(scenario.Branches, accounting.Branch{ID: "annex", Name: "Annex"})
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	bus := inproc.NewAccountingBus()
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	post := bookkeeping.PostJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	if _, err := post.Handle(ctx, accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 10),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Main branch revenue",
		Lines: []accounting.JournalLine{
			{AccountCode: "1000", Side: accounting.SideDebit, Amount: 15000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: 15000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	}); err != nil {
		t.Fatalf("post main: %v", err)
	}
	if _, err := post.Handle(ctx, accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 11),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Annex branch expense",
		Lines: []accounting.JournalLine{
			{AccountCode: "5200", Side: accounting.SideDebit, Amount: 4000, Dimensions: accounting.Dimensions{BranchID: "annex"}},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 4000, Dimensions: accounting.Dimensions{BranchID: "annex"}},
		},
	}); err != nil {
		t.Fatalf("post annex: %v", err)
	}

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus, Clock: closingClock}
	result, err := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: "2026-05"})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if len(result.Entries) != 2 {
		t.Fatalf("expected one closing entry per branch (2), got %d", len(result.Entries))
	}
	branches := map[string]bool{}
	for _, e := range result.Entries {
		for _, line := range e.Lines {
			branches[line.Dimensions.BranchID] = true
		}
	}
	if !branches["main"] || !branches["annex"] {
		t.Fatalf("expected closing entries for both branches, got %v", branches)
	}
}
