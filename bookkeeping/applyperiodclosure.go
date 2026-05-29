package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// ApplyPeriodClosure is the projection-side handler for accounting.PeriodClosure
// events: it type-asserts the polymorphic Event delivered by the Router and
// hands the payload to LedgerRepository.ApplyPeriodClosure, which flips
// Period.Status to closed and bumps the closure subject's sequence in one
// transaction.
type ApplyPeriodClosure struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyPeriodClosure) Handle(ctx context.Context, evt Event) error {
	pc, ok := evt.(accounting.PeriodClosure)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyPeriodClosure received %T on subject %q, want PeriodClosure", evt, evt.EventSubject())
	}
	return h.Repo.ApplyPeriodClosure(ctx, pc)
}
