package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// ApplyJournal is the projection-side handler for accounting.JournalPosted
// events: it type-asserts the polymorphic Event delivered by the Router and
// hands the payload to LedgerRepository.Apply, which writes the entry, its
// lines, every JournalRelation, and the broker sequence in one transaction.
type ApplyJournal struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyJournal) Handle(ctx context.Context, evt Event) error {
	je, ok := evt.(accounting.JournalPosted)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyJournal received %T on subject %q, want JournalPosted", evt, evt.EventSubject())
	}
	return h.Repo.Apply(ctx, je)
}
