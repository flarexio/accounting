package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flarexio/accounting"
)

// SettleJournal is the "record a payment that settles an invoice" use case. It
// posts the payment entry and, in the same JournalPosted, a settles
// JournalRelation linking it to the invoice/bill it clears. The invoice entry
// is never touched; whether it is fully or partly settled is a derived question
// answered from the entries and their settles relations.
type SettleJournal struct {
	Repo      accounting.LedgerRepository
	Publisher Publisher
	Clock     Clock
	Subject   string
}

// Validate reports whether the payment entry satisfies every JournalIntent
// invariant and the resulting settles relation satisfies every relation
// invariant. It runs no side effect.
func (uc SettleJournal) Validate(ctx context.Context, intent SettleIntent) error {
	_, _, _, _, err := uc.prepare(ctx, intent)
	return err
}

// Execute prepares the payment entry and its settles relation and publishes
// them as a single JournalPosted; the projection writes both in one
// transaction. It does not re-validate; unvalidated callers must use Handle.
func (uc SettleJournal) Execute(ctx context.Context, intent SettleIntent) (accounting.JournalEntry, error) {
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
func (uc SettleJournal) Handle(ctx context.Context, intent SettleIntent) (accounting.JournalEntry, error) {
	if err := uc.Validate(ctx, intent); err != nil {
		return accounting.JournalEntry{}, err
	}
	return uc.Execute(ctx, intent)
}

// prepare builds the payment entry and its settles relation and runs both the
// JournalIntent and JournalRelation validators. It does not publish.
func (uc SettleJournal) prepare(ctx context.Context, intent SettleIntent) (accounting.JournalEntry, accounting.JournalRelation, uint64, string, error) {
	if uc.Repo == nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: settle journal has no repository")
	}
	if uc.Publisher == nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: settle journal has no event publisher")
	}
	if intent.InvoiceEntryID == "" {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", errors.New("bookkeeping: settle journal needs an invoice_entry_id")
	}

	validator := accounting.Validator{Repo: uc.Repo}
	if err := validator.Validate(ctx, intent.Entry); err != nil {
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

	entry := accounting.JournalEntry{
		ID:          accounting.FormatEntryID(count + 1),
		Date:        intent.Entry.Date,
		PeriodID:    intent.Entry.PeriodID,
		Currency:    intent.Entry.Currency,
		Description: intent.Entry.Description,
		Lines:       intent.Entry.Lines,
		PostedAt:    clock(),
		Source:      intent.Entry.Source,
	}

	rel := accounting.JournalRelation{
		FromEntry: entry.ID,
		ToEntry:   intent.InvoiceEntryID,
		Type:      accounting.RelationSettles,
		Note:      intent.Note,
	}

	if err := validator.ValidateRelation(ctx, rel, entry); err != nil {
		return accounting.JournalEntry{}, accounting.JournalRelation{}, 0, "", err
	}

	return entry, rel, lastSeq, subject, nil
}
