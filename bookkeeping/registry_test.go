package bookkeeping_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/flarexio/accounting/bookkeeping"
)

func TestRegistry_RoutesPostJournal(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)
	reg := bookkeeping.NewBookkeepingRegistry(repo, bus, fixedClock, "")

	intent := balancedIntent()
	intentEnvelope := bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal, Post: &intent}

	if err := reg.Validate(ctx, intentEnvelope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	entry, err := reg.Execute(ctx, intentEnvelope)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stored, _ := repo.Entries(ctx); len(stored) != 1 || stored[0].ID != entry.ID {
		t.Fatalf("expected the routed entry posted, got %+v", stored)
	}
}

func TestRegistry_RoutesReverseJournal(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)
	reg := bookkeeping.NewBookkeepingRegistry(repo, bus, fixedClock, "")

	original := postOne(t, repo, bus)

	reverse := bookkeeping.ReverseIntent{EntryID: original.ID, Reason: "wrong amount"}
	intentEnvelope := bookkeeping.Intent{Kind: bookkeeping.IntentReverseJournal, Reverse: &reverse}

	if err := reg.Validate(ctx, intentEnvelope); err != nil {
		t.Fatalf("validate: %v", err)
	}
	reversal, err := reg.Execute(ctx, intentEnvelope)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if reversal.ID == original.ID {
		t.Fatal("expected a new reversing entry")
	}
	if stored, _ := repo.Entries(ctx); len(stored) != 2 {
		t.Fatalf("expected the original and the reversal stored, got %d", len(stored))
	}
}

func TestRegistry_RejectsUnknownKind(t *testing.T) {
	repo, bus := seededLedger(t)
	reg := bookkeeping.NewBookkeepingRegistry(repo, bus, fixedClock, "")

	err := reg.Validate(context.Background(), bookkeeping.Intent{Kind: "frobnicate"})
	if err == nil {
		t.Fatal("expected an unknown intent kind to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown intent kind") {
		t.Fatalf("expected an unknown-kind error, got %v", err)
	}
}

func TestRegistry_RejectsMissingPayload(t *testing.T) {
	repo, bus := seededLedger(t)
	reg := bookkeeping.NewBookkeepingRegistry(repo, bus, fixedClock, "")

	if err := reg.Validate(context.Background(), bookkeeping.Intent{Kind: bookkeeping.IntentPostJournal}); err == nil {
		t.Fatal("expected post_journal with no payload to be rejected")
	}
	if err := reg.Validate(context.Background(), bookkeeping.Intent{Kind: bookkeeping.IntentReverseJournal}); err == nil {
		t.Fatal("expected reverse_journal with no payload to be rejected")
	}
}

// TestRegistry_KindsMatchIntents guards prompt/registry drift.
func TestRegistry_KindsMatchIntents(t *testing.T) {
	reg := bookkeeping.NewBookkeepingRegistry(nil, nil, nil, "")

	want := make([]bookkeeping.IntentKind, 0)
	for _, d := range bookkeeping.Intents() {
		want = append(want, d.Kind)
	}
	slices.Sort(want)

	if got := reg.Kinds(); !slices.Equal(got, want) {
		t.Fatalf("registry routes %v but Intents() describes %v", got, want)
	}
}
