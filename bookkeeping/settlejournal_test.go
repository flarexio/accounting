package bookkeeping_test

import (
	"context"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

func TestSettleJournal_PostsPaymentAndLinksInvoice(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)
	invoice := postOne(t, repo, bus)

	uc := bookkeeping.SettleJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	payment, err := uc.Handle(ctx, bookkeeping.SettleIntent{
		Entry:          balancedIntent(),
		InvoiceEntryID: invoice.ID,
		Note:           "paid in full",
	})
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if payment.ID == invoice.ID {
		t.Fatal("the payment should be a new entry, not the invoice")
	}

	stored, err := repo.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected the invoice and the payment stored, got %d entries", len(stored))
	}

	rels, err := repo.RelationsTo(ctx, invoice.ID)
	if err != nil {
		t.Fatalf("RelationsTo: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected one relation pointing at the invoice, got %d", len(rels))
	}
	rel := rels[0]
	if rel.FromEntry != payment.ID || rel.Type != accounting.RelationSettles || rel.Note != "paid in full" {
		t.Fatalf("unexpected settles relation: %+v", rel)
	}
}

func TestSettleJournal_RejectsUnknownInvoice(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)

	uc := bookkeeping.SettleJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	_, err := uc.Handle(ctx, bookkeeping.SettleIntent{
		Entry:          balancedIntent(),
		InvoiceEntryID: "JE-9999",
	})
	if err == nil || !strings.Contains(err.Error(), "not in the ledger") {
		t.Fatalf("expected unknown-invoice error, got %v", err)
	}
}

func TestSettleJournal_RejectsMissingInvoiceID(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)

	uc := bookkeeping.SettleJournal{Repo: repo, Publisher: bus, Clock: fixedClock}
	_, err := uc.Handle(ctx, bookkeeping.SettleIntent{Entry: balancedIntent()})
	if err == nil || !strings.Contains(err.Error(), "invoice_entry_id") {
		t.Fatalf("expected missing-invoice-id error, got %v", err)
	}
}

// Routed through the Registry the way the agent loop runs it.
func TestSettleJournal_ViaRegistry(t *testing.T) {
	ctx := context.Background()
	repo, bus := seededLedger(t)
	invoice := postOne(t, repo, bus)

	reg := bookkeeping.NewBookkeepingRegistry(repo, bus, fixedClock, "")
	intent := bookkeeping.Intent{
		Kind:   bookkeeping.IntentSettle,
		Settle: &bookkeeping.SettleIntent{Entry: balancedIntent(), InvoiceEntryID: invoice.ID},
		Final:  true,
	}
	if err := reg.Validate(ctx, intent); err != nil {
		t.Fatalf("validate: %v", err)
	}
	payment, err := reg.Execute(ctx, intent)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	rels, _ := repo.RelationsTo(ctx, invoice.ID)
	if len(rels) != 1 || rels[0].FromEntry != payment.ID {
		t.Fatalf("expected settles relation from the payment, got %+v", rels)
	}
}
