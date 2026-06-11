package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// ApplyJournal projects accounting.JournalPosted: it unwraps the event to its
// domain models and writes them through LedgerRepository.AppendEntry.
type ApplyJournal struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyJournal) Handle(ctx context.Context, evt Event) error {
	je, ok := evt.(accounting.JournalPosted)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyJournal received %T on subject %q, want JournalPosted", evt, evt.EventSubject())
	}
	return h.Repo.AppendEntry(ctx, je.Entry, je.Relations)
}
