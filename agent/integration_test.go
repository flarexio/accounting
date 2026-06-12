package agent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm/openai"
)

// TestAgent_OpenAI hits the real OpenAI API; gated by ACCOUNTING_RUN_OPENAI_TESTS so
// `go test ./...` never silently spends tokens even with OPENAI_API_KEY set.
func TestAgent_OpenAI(t *testing.T) {
	if os.Getenv("ACCOUNTING_RUN_OPENAI_TESTS") == "" {
		t.Skip("set ACCOUNTING_RUN_OPENAI_TESTS=1 to run OpenAI integration tests")
	}
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Fatal("ACCOUNTING_RUN_OPENAI_TESTS is set but OPENAI_API_KEY is empty")
	}

	_, repo := awsBillScenario(t)
	bus := wireBus(t, repo)

	renderer, err := agent.NewPromptRenderer(context.Background(), repo)
	if err != nil {
		t.Fatalf("new renderer: %v", err)
	}

	engine, err := openai.NewAdapter(openai.Config[bookkeeping.Intent]{
		APIKey:       apiKey,
		Model:        "gpt-5.5",
		IntentSchema: bookkeeping.IntentSchema(),
		Renderer:     renderer,
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	agent := agent.Bookkeeper{
		Engine:    engine,
		Repo:      repo,
		Publisher: bus,
		Clock:     func() time.Time { return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC) },
		MaxTurns:  3,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := agent.Book(ctx, "Paid AWS bill 100 USD using company credit card on 12 May 2026.")
	if err != nil {
		t.Fatalf("bookkeeping run failed: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("expected one posted entry, got %d", len(res.Entries))
	}
	entry := res.Entries[0]
	if entry.PeriodID != "2026-05" {
		t.Errorf("expected entry posted to open May 2026 period, got %q", entry.PeriodID)
	}

	var debit, credit int64
	for _, line := range entry.Lines {
		switch line.Side {
		case accounting.SideDebit:
			debit += line.Amount
		case accounting.SideCredit:
			credit += line.Amount
		}
	}
	if debit == 0 || credit == 0 || debit != credit {
		t.Errorf("posted entry should be balanced, got debit=%d credit=%d", debit, credit)
	}

	t.Logf("turns=%d entry=%s currency=%s debit=%d credit=%d", res.Turns, entry.ID, entry.Currency, debit, credit)
	for _, line := range entry.Lines {
		t.Logf("  %s %s %d (%s) memo=%q", line.AccountCode, line.Side, line.Amount, line.Dimensions.BranchID, line.Memo)
	}
}
