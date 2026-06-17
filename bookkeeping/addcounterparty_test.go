package bookkeeping_test

import (
	"context"
	"errors"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
)

// newCounterpartyEnv wires a memory repo to an inproc bus projecting
// CounterpartyAdded; inproc dispatches synchronously, so a published add is
// queryable on return.
func newCounterpartyEnv(t *testing.T) (accounting.LedgerRepository, bookkeeping.EventBus) {
	t.Helper()
	repo := memory.NewAccountingRepository()
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectCounterpartyAdded, &bookkeeping.ApplyCounterparty{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return repo, bus
}

func TestAddCounterparty_AllocatesSequentialIDs(t *testing.T) {
	ctx := context.Background()
	repo, bus := newCounterpartyEnv(t)
	uc := bookkeeping.AddCounterparty{Repo: repo, Publisher: bus}

	first, err := uc.Execute(ctx, accounting.Counterparty{Name: "Acme", Kind: accounting.CounterpartyCustomer})
	if err != nil {
		t.Fatalf("first add: %v", err)
	}
	if first.ID != "CP-0001" {
		t.Fatalf("first id = %q, want CP-0001", first.ID)
	}
	if !first.Active {
		t.Fatalf("a freshly added counterparty should be active")
	}

	second, err := uc.Execute(ctx, accounting.Counterparty{Name: "Globex", Kind: accounting.CounterpartySupplier})
	if err != nil {
		t.Fatalf("second add: %v", err)
	}
	if second.ID != "CP-0002" {
		t.Fatalf("second id = %q, want CP-0002 (dense, no gap)", second.ID)
	}
}

func TestAddCounterparty_RejectsDuplicateTaxID(t *testing.T) {
	ctx := context.Background()
	repo, bus := newCounterpartyEnv(t)
	uc := bookkeeping.AddCounterparty{Repo: repo, Publisher: bus}

	if _, err := uc.Execute(ctx, accounting.Counterparty{Name: "Acme", Kind: accounting.CounterpartyCustomer, TaxID: "12345678"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	_, err := uc.Execute(ctx, accounting.Counterparty{Name: "Acme Holdings", Kind: accounting.CounterpartyCustomer, TaxID: "12345678"})
	if !errors.Is(err, bookkeeping.ErrDuplicateCounterparty) {
		t.Fatalf("duplicate tax_id err = %v, want ErrDuplicateCounterparty", err)
	}
	if cps, _ := repo.Counterparties(ctx); len(cps) != 1 {
		t.Fatalf("duplicate should not have been registered: %d counterparties", len(cps))
	}
}

func TestAddCounterparty_RejectsInvalidDraft(t *testing.T) {
	ctx := context.Background()
	repo, bus := newCounterpartyEnv(t)
	uc := bookkeeping.AddCounterparty{Repo: repo, Publisher: bus}

	if _, err := uc.Execute(ctx, accounting.Counterparty{Kind: accounting.CounterpartyCustomer}); err == nil {
		t.Fatal("expected a missing-name draft to be rejected")
	}
	if _, err := uc.Execute(ctx, accounting.Counterparty{Name: "Acme", Kind: "vendor"}); err == nil {
		t.Fatal("expected an unknown kind to be rejected")
	}
}
