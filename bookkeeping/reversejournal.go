package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flarexio/accounting"
)

// ReverseJournal is the "reverse a posted entry" use case. It loads the named
// entry, builds the mirror-image reversal, and posts the new entry together
// with a JournalRelation linking it back to the original. The original entry
// is never touched.
type ReverseJournal struct {
	Repo      accounting.LedgerRepository
	Publisher Publisher
	Clock     Clock
	Subject   string
}

// Validate reports whether intent names an existing entry, the mirror reversal
// satisfies every JournalIntent invariant, and the resulting JournalRelation
// satisfies every relation invariant. It runs no side effect.
func (uc ReverseJournal) Validate(ctx context.Context, intent ReverseIntent) error {
	_, _, _, _, err := uc.prepare(ctx, intent)
	return err
}

// Execute prepares the reversal entry and its linking relation and publishes
// them as a single JournalPosted; the projection writes both in one
// transaction. It does not re-validate; unvalidated callers must use Handle.
func (uc ReverseJournal) Execute(ctx context.Context, intent ReverseIntent) (accounting.JournalEntry, error) {
	entry, rel, lastSeq, subject, err := uc.prepare(ctx, intent)
	if err != nil {
		return accounting.JournalEntry{}, err
	}
	if err := uc.Publisher.Publish(ctx, accounting.JournalPosted{
		Entry:     entry,
		Relations: []accounting.JournalRelation{rel},
	}, accounting.ExpectedSequence{
		Subject: subject,
		LastSeq: lastSeq,
	}); err != nil {
		return accounting.JournalEntry{}, fmt.Errorf("bookkeeping: publish: %w", err)
	}
	return entry, nil
}

// Handle validates intent and, if clean, executes it.
func (uc ReverseJournal) Handle(ctx context.Context, intent ReverseIntent) (accounting.JournalEntry, error) {
	if err := uc.Validate(ctx, intent); err != nil {
		return accounting.JournalEntry{}, err
	}
	return uc.Execute(ctx, intent)
}

// prepare loads the original entry, builds the mirror reversal entry and its
// linking relation, and runs both the JournalIntent and JournalRelation
// validators. It does not publish.
func (uc ReverseJournal) prepare(ctx context.Context, intent ReverseIntent) (accounting.JournalEntry, accounting.JournalRelation, uint64, string, error) {
	if uc.Repo == nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: reverse journal has no repository")
	}
	if uc.Publisher == nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: reverse journal has no event publisher")
	}
	if intent.EntryID == "" {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: reverse journal needs an entry_id")
	}

	original, ok, err := uc.Repo.Entry(ctx, intent.EntryID)
	if err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", fmt.Errorf("bookkeeping: load entry %q: %w", intent.EntryID, err)
	}
	if !ok {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", fmt.Errorf("bookkeeping: entry %q is not in the ledger", intent.EntryID)
	}

	lines := make([]accounting.JournalLine, len(original.Lines))
	for i, line := range original.Lines {
		line.Side = flipSide(line.Side)
		lines[i] = line
	}

	description := fmt.Sprintf("Reversal of %s", original.ID)
	if intent.Note != "" {
		description += ": " + intent.Note
	}

	revIntent := accounting.JournalIntent{
		Date:        original.Date,
		PeriodID:    original.PeriodID,
		Currency:    original.Currency,
		Description: description,
		Lines:       lines,
	}

	validator := accounting.Validator{Repo: uc.Repo}
	if err := validator.Validate(ctx, revIntent); err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", err
	}

	subject := uc.Subject
	if subject == "" {
		subject = accounting.SubjectJournalPosted
	}
	clock := uc.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	lastSeq, err := uc.Repo.LastSequence(ctx, subject)
	if err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", fmt.Errorf("bookkeeping: read last sequence: %w", err)
	}
	count, err := uc.Repo.EntryCount(ctx)
	if err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", fmt.Errorf("bookkeeping: read entry count: %w", err)
	}

	revEntry := accounting.JournalEntry{
		ID:          accounting.FormatEntryID(count + 1),
		Date:        revIntent.Date,
		PeriodID:    revIntent.PeriodID,
		Currency:    revIntent.Currency,
		Description: revIntent.Description,
		Lines:       revIntent.Lines,
		PostedAt:    clock(),
	}

	rel := accounting.JournalRelation{
		FromEntry: revEntry.ID,
		ToEntry:   original.ID,
		Type:      accounting.RelationReverses,
		Reason:    intent.Reason,
		Note:      intent.Note,
	}

	if err := validator.ValidateRelation(ctx, rel, revEntry); err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", err
	}

	return revEntry, rel, lastSeq, subject, nil
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
