package agent_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/llm"
)

type fakeEngineFunc func(ctx context.Context, input llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error)

func (f fakeEngineFunc) Predict(ctx context.Context, input llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
	return f(ctx, input)
}

func awsBillRepo(t *testing.T) accounting.LedgerRepository {
	t.Helper()
	scenario, err := accounting.LoadScenarioFile("../seed/aws_bill.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	repo := memory.NewAccountingRepository()
	if err := scenario.Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return repo
}

func awsBillScenario(t *testing.T) (accounting.Scenario, accounting.LedgerRepository) {
	t.Helper()
	scenario, err := accounting.LoadScenarioFile("../seed/aws_bill.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	repo := memory.NewAccountingRepository()
	if err := scenario.Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return scenario, repo
}

func wireBus(t *testing.T, repo accounting.LedgerRepository) bookkeeping.EventBus {
	t.Helper()
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, &bookkeeping.ApplyJournal{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return bus
}

func balancedAWSIntent() accounting.JournalIntent {
	return accounting.JournalIntent{
		Date:        accounting.NewDate(2026, 5, 12),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Paid AWS bill on company credit card",
		Lines: []accounting.JournalLine{
			{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
			{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
		},
	}
}

func postIntent(intent accounting.JournalIntent) bookkeeping.Intent {
	return bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal, Post: &intent, Final: true}
}

func fixedClock() time.Time {
	return time.Date(2026, 5, 12, 9, 0, 0, 0, time.UTC)
}

func TestAgent_PostsBalancedJournal(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		return llm.IntentOutput(
			postIntent(balancedAWSIntent()),
			nil,
			"AWS invoice paid on credit card; expense debit, liability credit",
		), nil
	})

	agent := agent.Bookkeeper{
		Engine:    engine,
		Repo:      repo,
		Publisher: bus,
		Clock:     fixedClock,
		MaxTurns:  3,
	}
	res, err := agent.Book(context.Background(), "Paid AWS bill 100 USD using company credit card")
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if res.Turns != 1 {
		t.Fatalf("expected 1 turn, got %d", res.Turns)
	}
	if res.Intent.Kind != bookkeeping.IntentPostJournal {
		t.Fatalf("expected a post_journal intent, got %q", res.Intent.Kind)
	}
	if res.Entry.ID == "" {
		t.Fatal("expected posted entry to be returned")
	}
	if !strings.HasPrefix(res.Entry.ID, "JE-") {
		t.Fatalf("unexpected entry id format: %q", res.Entry.ID)
	}
	got, err := repo.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != res.Entry.ID {
		t.Fatalf("expected one stored entry matching returned ID, got %+v", got)
	}
}

func TestAgent_CorrectsAfterValidationFeedback(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	calls := 0
	engine := fakeEngineFunc(func(_ context.Context, input llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		calls++
		switch calls {
		case 1:
			intent := balancedAWSIntent()
			intent.Lines[1].Amount = 9000
			return llm.IntentOutput(postIntent(intent), nil, "first pass: assume $90 surcharge waived"), nil
		default:
			sawValidationErr := false
			for _, e := range input.Events {
				if e.Kind == llm.EventValidationError {
					sawValidationErr = true
				}
			}
			if !sawValidationErr {
				t.Errorf("expected validation_error event on retry, got events %+v", input.Events)
			}
			return llm.IntentOutput(
				postIntent(balancedAWSIntent()),
				nil,
				"corrected: rebalance credit to match $100 debit",
			), nil
		}
	})

	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}
	res, err := agent.Book(context.Background(), "Paid AWS bill 100 USD using company credit card")
	if err != nil {
		t.Fatalf("expected success after correction, got %v", err)
	}
	if res.Turns != 2 {
		t.Fatalf("expected 2 turns, got %d", res.Turns)
	}
	if calls != 2 {
		t.Fatalf("expected engine called twice, got %d", calls)
	}
	entries, _ := repo.Entries(context.Background())
	if len(entries) != 1 {
		t.Fatalf("expected exactly one entry posted after correction, got %d", len(entries))
	}
}

func TestAgent_RejectsClosedPeriodIntent(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	calls := 0
	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		calls++
		intent := balancedAWSIntent()
		if calls == 1 {
			intent.PeriodID = "2026-04"
		}
		return llm.IntentOutput(postIntent(intent), nil, "best guess"), nil
	})

	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}
	res, err := agent.Book(context.Background(), "Record April AWS bill late")
	if err != nil {
		t.Fatalf("expected success after correcting to open period, got %v", err)
	}
	if res.Entry.PeriodID != "2026-05" {
		t.Fatalf("expected entry posted to open period, got %q", res.Entry.PeriodID)
	}
}

func TestAgent_SequentialIDsAcrossPosts(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, ""), nil
	})
	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}

	a, err := agent.Book(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := agent.Book(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if a.Entry.ID == b.Entry.ID {
		t.Fatalf("expected distinct IDs across posts, got %s and %s", a.Entry.ID, b.Entry.ID)
	}
}

func TestAgent_ReversesAPostedEntry(t *testing.T) {
	ctx := context.Background()
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	postedID := ""
	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		if postedID == "" {
			return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, ""), nil
		}
		return llm.IntentOutput(
			bookkeeping.Intent{
				Kind:    bookkeeping.IntentReverseJournal,
				Reverse: &bookkeeping.ReverseIntent{EntryID: postedID, Reason: "duplicate posting"},
				Final:   true,
			},
			nil,
			"reverse the entry the request names",
		), nil
	})
	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}

	first, err := agent.Book(ctx, "Paid AWS bill on the company credit card")
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	postedID = first.Entry.ID

	second, err := agent.Book(ctx, "Reverse "+postedID+"; it was a duplicate")
	if err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if second.Intent.Kind != bookkeeping.IntentReverseJournal {
		t.Fatalf("expected the reverse_journal intent, got %q", second.Intent.Kind)
	}
	if second.Entry.ID == first.Entry.ID {
		t.Fatal("expected the reversal to be posted as a new entry")
	}
	if !strings.HasPrefix(second.Entry.Description, "Reversal of "+first.Entry.ID) {
		t.Fatalf("expected the reversal description to name the original, got %q", second.Entry.Description)
	}
	entries, _ := repo.Entries(ctx)
	if len(entries) != 2 {
		t.Fatalf("expected the original and the reversal stored, got %d", len(entries))
	}
}

func TestAgent_MultiActionReverseThenRepostInOneRequest(t *testing.T) {
	ctx := context.Background()
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	post := agent.Bookkeeper{
		Engine: fakeEngineFunc(func(context.Context, llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
			return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, ""), nil
		}),
		Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3,
	}
	first, err := post.Book(ctx, "post the AWS bill")
	if err != nil {
		t.Fatalf("seed post: %v", err)
	}

	// One request, two actions: reverse the entry (not final), then re-post (final).
	call := 0
	engine := fakeEngineFunc(func(context.Context, llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		call++
		if call == 1 {
			return llm.IntentOutput(bookkeeping.Intent{
				Kind:    bookkeeping.IntentReverseJournal,
				Reverse: &bookkeeping.ReverseIntent{EntryID: first.Entry.ID, Reason: accounting.ReasonAmountError},
				Final:   false,
			}, nil, "reverse the wrong entry"), nil
		}
		return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, "re-post the correction"), nil
	})
	bk := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 5}

	res, err := bk.Book(ctx, "reverse JE and re-post at the right amount")
	if err != nil {
		t.Fatalf("multi-action request: %v", err)
	}
	if res.Turns != 2 {
		t.Fatalf("turns = %d, want 2 (reverse + final post)", res.Turns)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("res.Entries = %d, want 2 (reversal + correction)", len(res.Entries))
	}
	if res.Entry.ID != res.Entries[1].ID {
		t.Fatalf("res.Entry should be the last committed entry, got %q want %q", res.Entry.ID, res.Entries[1].ID)
	}
	entries, _ := repo.Entries(ctx)
	if len(entries) != 3 {
		t.Fatalf("expected original + reversal + correction = 3 entries, got %d", len(entries))
	}
}

// A multi-action request that never marks a final action runs to MaxTurns and
// aborts: the staged entries are discarded, so the ledger keeps only what earlier
// requests committed and the Result reports nothing committed.
func TestAgent_PartialMultiActionAbortsWithoutCommitting(t *testing.T) {
	ctx := context.Background()
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	// Every turn posts a non-final entry, so the loop never finishes and exhausts MaxTurns.
	engine := fakeEngineFunc(func(context.Context, llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		intent := postIntent(balancedAWSIntent())
		intent.Final = false
		return llm.IntentOutput(intent, nil, "keep posting, never final"), nil
	})
	bk := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 3}

	res, err := bk.Book(ctx, "do several things but never finish")
	if !errors.Is(err, loop.ErrMaxTurnsExceeded) {
		t.Fatalf("err = %v, want ErrMaxTurnsExceeded", err)
	}
	if len(res.Entries) != 0 || res.Entry.ID != "" {
		t.Fatalf("aborted request committed nothing, got Entry=%q Entries=%d", res.Entry.ID, len(res.Entries))
	}
	entries, _ := repo.Entries(ctx)
	if len(entries) != 0 {
		t.Fatalf("expected the ledger untouched after abort, got %d entries", len(entries))
	}
}

func TestAgent_ClosedPeriodMidSessionBlocksFurtherPosts(t *testing.T) {
	ctx := context.Background()
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)

	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		return llm.IntentOutput(postIntent(balancedAWSIntent()), nil, ""), nil
	})
	agent := agent.Bookkeeper{Engine: engine, Repo: repo, Publisher: bus, Clock: fixedClock, MaxTurns: 1}

	if _, err := agent.Book(ctx, "first post should succeed"); err != nil {
		t.Fatalf("first post: %v", err)
	}

	period, _, _ := repo.Period(ctx, "2026-05")
	period.Status = accounting.PeriodClosed
	if err := repo.PutPeriod(ctx, period); err != nil {
		t.Fatalf("close period: %v", err)
	}

	if _, err := agent.Book(ctx, "second post against closed period"); err == nil {
		t.Fatal("expected error after closing the period")
	}
}

func TestAgent_MissingEngine(t *testing.T) {
	repo := awsBillRepo(t)
	bus := wireBus(t, repo)
	agent := agent.Bookkeeper{Repo: repo, Publisher: bus}
	if _, err := agent.Book(context.Background(), "x"); err == nil {
		t.Fatal("expected error for missing engine")
	}
}

func TestAgent_MissingRepo(t *testing.T) {
	bus := inproc.NewAccountingBus()
	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		return llm.ReasoningOutput[bookkeeping.Intent]{}, nil
	})
	agent := agent.Bookkeeper{Engine: engine, Publisher: bus}
	if _, err := agent.Book(context.Background(), "x"); err == nil {
		t.Fatal("expected error for missing repository")
	}
}

func TestAgent_MissingPublisher(t *testing.T) {
	repo := awsBillRepo(t)
	engine := fakeEngineFunc(func(_ context.Context, _ llm.ReasoningInput) (llm.ReasoningOutput[bookkeeping.Intent], error) {
		return llm.ReasoningOutput[bookkeeping.Intent]{}, nil
	})
	agent := agent.Bookkeeper{Engine: engine, Repo: repo}
	if _, err := agent.Book(context.Background(), "x"); err == nil {
		t.Fatal("expected error for missing publisher")
	}
}
