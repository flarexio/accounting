//go:build integration

// Package-level note: this file is gated behind the "integration" build tag
// because it requires the local compose.yaml stack (postgres + NATS JetStream)
// and a freshly migrated schema. Run with:
//
//	docker compose up -d
//	migrate -path persistence/postgres/migrations \
//	  -database "postgres://stoa:stoa@localhost:5432/accounting?sslmode=disable" up
//	go test -tags=integration ./cmd/ledger/...
//
// The test owns its own postgres rows: it picks an isolated company id every
// run and only inspects rows it produced, so it is safe to run repeatedly
// against a long-lived compose stack.

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/embedding/openai"
	natsmsg "github.com/flarexio/accounting/messaging/nats"
	pgrepo "github.com/flarexio/accounting/persistence/postgres"
)

const (
	defaultIntegrationDSN  = "postgres://stoa:stoa@localhost:5432/accounting?sslmode=disable"
	defaultIntegrationNATS = "nats://localhost:4222"
)

func integrationDSN() string {
	if v := os.Getenv("ACCOUNTING_TEST_POSTGRES_DSN"); v != "" {
		return v
	}
	return defaultIntegrationDSN
}

func integrationNATSURL() string {
	if v := os.Getenv("ACCOUNTING_TEST_NATS_URL"); v != "" {
		return v
	}
	return defaultIntegrationNATS
}

// TestIntegration_ClosePeriod_EndToEnd posts revenue/expense entries against
// real postgres + NATS, runs `ledger close`, and verifies the closing entries,
// `closes` relations, and the period flip all landed via the event bus. The
// projection write is asynchronous (NATS deliver -> Apply / ApplyPeriodClosure)
// so the test polls until the expected state appears or fails after a generous
// timeout.
func TestIntegration_ClosePeriod_EndToEnd(t *testing.T) {
	ctx := context.Background()

	// Isolate every run under its own company so the test never sees state
	// from a prior failed run, and another developer can hit the same db at
	// the same time without collision.
	suffix := time.Now().UTC().Format("20060102T150405.000000000")
	companyID := "itest-" + suffix
	periodID := "2026-05"

	// 1. Open a control connection used only by the assertions; production
	// code goes through the postgres repository adapter below.
	control, err := pgxpool.New(ctx, integrationDSN())
	if err != nil {
		t.Fatalf("control pool: %v", err)
	}
	t.Cleanup(func() { control.Close() })

	t.Cleanup(func() {
		// Best-effort cleanup of the rows this run wrote. The compose stack
		// is shared so we leave other rows alone.
		_, _ = control.Exec(context.Background(),
			`DELETE FROM journal_relations WHERE from_entry IN (SELECT id FROM journal_entries WHERE period_id = $1)`, periodID)
		_, _ = control.Exec(context.Background(),
			`DELETE FROM journal_lines WHERE entry_id IN (SELECT id FROM journal_entries WHERE period_id = $1)`, periodID)
		_, _ = control.Exec(context.Background(),
			`DELETE FROM journal_entries WHERE period_id = $1`, periodID)
		_, _ = control.Exec(context.Background(),
			`DELETE FROM subject_offsets WHERE subject IN ($1, $2)`,
			accounting.SubjectJournalPosted, accounting.SubjectPeriodClosure)
		_, _ = control.Exec(context.Background(), `DELETE FROM periods WHERE id = $1`, periodID)
		_, _ = control.Exec(context.Background(), `DELETE FROM companies WHERE id = $1`, companyID)
	})

	// 2. Build the repository. The embedder is a no-op stub so we do not
	// depend on the OpenAI key here.
	repo, repoCloser, err := pgrepo.NewAccountingRepository(ctx, integrationDSN(), openai.NewEmbedder("text-embedding-3-small", 1536))
	if err != nil {
		t.Fatalf("postgres repo: %v", err)
	}
	t.Cleanup(func() { _ = repoCloser.Close() })

	// 3. Seed the minimum scenario the use case needs.
	scenario := accounting.Scenario{
		Company: accounting.Company{
			ID:                   companyID,
			Name:                 "Integration Co.",
			TimeZone:             "UTC",
			RetainedEarningsCode: "3300",
		},
		Accounts: []accounting.Account{
			{Code: "1000", Name: "Cash", Type: accounting.AccountAsset, Active: true},
			{Code: "2100", Name: "AP", Type: accounting.AccountLiability, Active: true},
			{Code: "3300", Name: "Retained Earnings", Type: accounting.AccountEquity, Active: true},
			{Code: "4000", Name: "Revenue", Type: accounting.AccountRevenue, Active: true},
			{Code: "5200", Name: "Expense", Type: accounting.AccountExpense, Active: true},
		},
		Branches: []accounting.Branch{{ID: "main", Name: "Main"}},
		Periods: []accounting.Period{{
			ID:     periodID,
			Start:  accounting.NewDate(2026, 5, 1),
			End:    accounting.NewDate(2026, 5, 31),
			Status: accounting.PeriodOpen,
		}},
	}
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// 4. Open NATS with a consumer name unique to this run so it does not
	// clash with prior runs that left durable state in the stream.
	bus, err := natsmsg.NewAccountingBus(ctx, natsmsg.Config{
		URL:      integrationNATSURL(),
		Stream:   "ACCOUNTING_ITEST",
		Consumer: "itest-" + suffix,
	})
	if err != nil {
		t.Fatalf("nats bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, &bookkeeping.ApplyJournal{Repo: repo}).
		On(accounting.SubjectPeriodClosure, &bookkeeping.ApplyPeriodClosure{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// 5. Post two source entries: one revenue, one expense.
	post := bookkeeping.PostJournal{Repo: repo, Publisher: bus}
	rev, err := post.Handle(ctx, accounting.JournalIntent{
		Date: accounting.NewDate(2026, 5, 10), PeriodID: periodID, Currency: "USD",
		Description: "Sales",
		Lines: []accounting.JournalLine{
			{AccountCode: "1000", Side: accounting.SideDebit, Amount: 30000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "4000", Side: accounting.SideCredit, Amount: 30000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	})
	if err != nil {
		t.Fatalf("post revenue: %v", err)
	}
	if !waitForEntry(t, control, rev.ID, 5*time.Second) {
		t.Fatalf("revenue entry never landed in projection")
	}

	exp, err := post.Handle(ctx, accounting.JournalIntent{
		Date: accounting.NewDate(2026, 5, 12), PeriodID: periodID, Currency: "USD",
		Description: "Cloud bill",
		Lines: []accounting.JournalLine{
			{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "main"}},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "main"}},
		},
	})
	if err != nil {
		t.Fatalf("post expense: %v", err)
	}
	if !waitForEntry(t, control, exp.ID, 5*time.Second) {
		t.Fatalf("expense entry never landed in projection")
	}

	// 6. Run ClosePeriod against a clock just past the period end so the
	// timezone gate accepts the close.
	closeUC := bookkeeping.ClosePeriod{
		Repo:      repo,
		Publisher: bus,
		Clock:     func() time.Time { return time.Date(2026, 6, 1, 1, 0, 0, 0, time.UTC) },
	}
	res, err := closeUC.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: periodID})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if res.AlreadyClosed {
		t.Fatalf("expected fresh close, got AlreadyClosed=true")
	}
	if len(res.Entries) != 1 {
		t.Fatalf("expected 1 closing entry, got %d", len(res.Entries))
	}
	closingID := res.Entries[0].ID

	// 7. The publisher returns synchronously but the projection writer runs
	// off the consumer; poll until the state lands.
	if !waitForEntry(t, control, closingID, 5*time.Second) {
		t.Fatal("closing entry never landed in projection")
	}
	if !waitForClosesRelations(t, control, closingID, 2, 5*time.Second) {
		t.Fatalf("expected 2 closes relations from %s, gave up waiting", closingID)
	}
	if !waitForPeriodClosed(t, control, periodID, 5*time.Second) {
		t.Fatalf("period never flipped to closed")
	}

	// 8. Re-invoke: should be a no-op AlreadyClosed.
	res2, err := closeUC.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: periodID})
	if err != nil {
		t.Fatalf("close (retry): %v", err)
	}
	if !res2.AlreadyClosed {
		t.Fatalf("expected AlreadyClosed=true on retry, got %+v", res2)
	}
	if len(res2.Entries) != 0 {
		t.Fatalf("expected no new entries on retry, got %d", len(res2.Entries))
	}
}

func waitForEntry(t *testing.T, db *pgxpool.Pool, id string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		err := db.QueryRow(context.Background(), `SELECT count(*) FROM journal_entries WHERE id = $1`, id).Scan(&n)
		if err == nil && n == 1 {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForClosesRelations(t *testing.T, db *pgxpool.Pool, fromID string, want int, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		err := db.QueryRow(context.Background(),
			`SELECT count(*) FROM journal_relations WHERE from_entry = $1 AND type = 'closes'`, fromID).Scan(&n)
		if err == nil && n == want {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func waitForPeriodClosed(t *testing.T, db *pgxpool.Pool, periodID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status string
		err := db.QueryRow(context.Background(), `SELECT status FROM periods WHERE id = $1`, periodID).Scan(&status)
		if err == nil && status == string(accounting.PeriodClosed) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

