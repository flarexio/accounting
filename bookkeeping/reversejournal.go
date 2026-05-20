package bookkeeping

import (
	"context"
	"errors"
	"fmt"

	"github.com/flarexio/accounting"
)

// ReverseJournal is the "reverse a posted entry" use case. It builds the
// mirror-image entry -- every line's side swapped -- and posts it through
// PostJournal into the original's period and date. The original entry is
// never touched.
type ReverseJournal struct {
	Repo      accounting.LedgerRepository
	Publisher EventPublisher
	Clock     Clock
	Subject   string
}

// Validate runs no side effect; it reports whether intent names an existing
// entry and the resulting reversal satisfies every accounting invariant.
func (uc ReverseJournal) Validate(ctx context.Context, intent ReverseIntent) error {
	reversal, err := uc.reversalIntent(ctx, intent)
	if err != nil {
		return err
	}
	return uc.post().Validate(ctx, reversal)
}

// Execute posts the reversing entry for an already-validated intent. It does
// not re-validate; unvalidated callers must use Handle.
func (uc ReverseJournal) Execute(ctx context.Context, intent ReverseIntent) (accounting.JournalEntry, error) {
	reversal, err := uc.reversalIntent(ctx, intent)
	if err != nil {
		return accounting.JournalEntry{}, err
	}
	return uc.post().Execute(ctx, reversal)
}

// Handle validates intent and, if clean, executes it.
func (uc ReverseJournal) Handle(ctx context.Context, intent ReverseIntent) (accounting.JournalEntry, error) {
	if err := uc.Validate(ctx, intent); err != nil {
		return accounting.JournalEntry{}, err
	}
	return uc.Execute(ctx, intent)
}

func (uc ReverseJournal) post() PostJournal {
	return PostJournal{
		Repo:      uc.Repo,
		Publisher: uc.Publisher,
		Clock:     uc.Clock,
		Subject:   uc.Subject,
	}
}

func (uc ReverseJournal) reversalIntent(ctx context.Context, intent ReverseIntent) (accounting.JournalIntent, error) {
	if uc.Repo == nil {
		return accounting.JournalIntent{}, errors.New("bookkeeping: reverse journal has no repository")
	}
	if intent.EntryID == "" {
		return accounting.JournalIntent{}, errors.New("bookkeeping: reverse journal needs an entry_id")
	}

	entry, ok, err := uc.Repo.Entry(ctx, intent.EntryID)
	if err != nil {
		return accounting.JournalIntent{}, fmt.Errorf("bookkeeping: load entry %q: %w", intent.EntryID, err)
	}
	if !ok {
		return accounting.JournalIntent{}, fmt.Errorf("bookkeeping: entry %q is not in the ledger", intent.EntryID)
	}

	lines := make([]accounting.JournalLine, len(entry.Lines))
	for i, line := range entry.Lines {
		line.Side = flipSide(line.Side)
		lines[i] = line
	}

	description := fmt.Sprintf("Reversal of %s", entry.ID)
	if intent.Reason != "" {
		description += ": " + intent.Reason
	}

	return accounting.JournalIntent{
		Date:        entry.Date,
		PeriodID:    entry.PeriodID,
		Currency:    entry.Currency,
		Description: description,
		Lines:       lines,
	}, nil
}

func flipSide(side accounting.LineSide) accounting.LineSide {
	switch side {
	case accounting.SideDebit:
		return accounting.SideCredit
	case accounting.SideCredit:
		return accounting.SideDebit
	default:
		return side
	}
}
