package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// ApplyPeriodClosure projects accounting.PeriodClosure: it transitions the
// named period to closed through LedgerRepository.SetPeriodStatus.
type ApplyPeriodClosure struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyPeriodClosure) Handle(ctx context.Context, evt Event) error {
	pc, ok := evt.(accounting.PeriodClosure)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyPeriodClosure received %T on subject %q, want PeriodClosure", evt, evt.EventSubject())
	}
	return h.Repo.SetPeriodStatus(ctx, pc.Period.ID, accounting.PeriodClosed)
}
