package bookkeeping

import (
	"context"
	"errors"
	"fmt"

	"github.com/flarexio/accounting"
)

// ErrDuplicateCounterparty is returned when a draft's tax_id is already registered.
var ErrDuplicateCounterparty = errors.New("bookkeeping: counterparty already registered")

// AddCounterparty is the operator-driven "register a customer/supplier" use case.
type AddCounterparty struct {
	Repo      accounting.LedgerRepository
	Publisher Publisher
}

// Execute validates the draft, allocates the next dense CP-NNNN id, and publishes
// CounterpartyAdded. The new counterparty is registered active; a tax_id already
// in use is rejected. There is no optimistic-concurrency check (the projection is
// upsert-by-id), so it expects a single writer.
func (uc AddCounterparty) Execute(ctx context.Context, draft accounting.Counterparty) (accounting.Counterparty, error) {
	if uc.Repo == nil {
		return accounting.Counterparty{}, errors.New("bookkeeping: add counterparty has no repository")
	}
	if uc.Publisher == nil {
		return accounting.Counterparty{}, errors.New("bookkeeping: add counterparty has no event publisher")
	}
	if err := draft.Validate(); err != nil {
		return accounting.Counterparty{}, err
	}

	existing, err := uc.Repo.Counterparties(ctx)
	if err != nil {
		return accounting.Counterparty{}, fmt.Errorf("bookkeeping: read counterparties: %w", err)
	}
	if draft.TaxID != "" {
		for _, c := range existing {
			if c.TaxID == draft.TaxID {
				return accounting.Counterparty{}, fmt.Errorf("%w: tax_id %q is %s", ErrDuplicateCounterparty, draft.TaxID, c.ID)
			}
		}
	}

	cp := draft
	cp.ID = accounting.FormatCounterpartyID(uint64(len(existing)) + 1)
	cp.Active = true

	if err := uc.Publisher.Publish(ctx, accounting.CounterpartyAdded{Counterparty: cp}, accounting.ExpectedSequence{}); err != nil {
		return accounting.Counterparty{}, fmt.Errorf("bookkeeping: publish: %w", err)
	}
	return cp, nil
}
