// Package bookkeeping holds the bookkeeping use cases: application-layer
// operations that validate and execute a typed Intent against the accounting
// domain. A use case carries no LLM dependency.
package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/flarexio/accounting"
)

// Clock returns the time a posted entry is stamped with; tests inject a fake.
type Clock func() time.Time

// PostJournal is the "post a journal entry" use case.
type PostJournal struct {
	Repo      accounting.LedgerRepository
	Publisher Publisher
	Clock     Clock
	Subject   string
}

// Validate runs no side effect; it reports whether intent satisfies every accounting invariant.
func (uc PostJournal) Validate(ctx context.Context, intent accounting.JournalIntent) error {
	return accounting.Validator{Repo: uc.Repo}.Validate(ctx, intent)
}

// Execute publishes an already-validated intent. It does not re-validate;
// unvalidated callers must use Handle.
func (uc PostJournal) Execute(ctx context.Context, intent accounting.JournalIntent) (accounting.JournalEntry, error) {
	if uc.Repo == nil {
		return accounting.JournalEntry{}, errors.New("bookkeeping: post journal has no repository")
	}
	if uc.Publisher == nil {
		return accounting.JournalEntry{}, errors.New("bookkeeping: post journal has no event publisher")
	}

	subject := uc.Subject
	if subject == "" {
		subject = accounting.SubjectJournalPosted
	}
	clock := uc.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}

	// lastSeq+1 is both the optimistic-concurrency hint and the new entry's ID:
	// Apply writes the entry and bumps the subject offset in one transaction.
	lastSeq, err := uc.Repo.LastSequence(ctx, subject)
	if err != nil {
		return accounting.JournalEntry{}, fmt.Errorf("bookkeeping: read last sequence: %w", err)
	}

	entry := accounting.JournalEntry{
		ID:          accounting.FormatEntryID(lastSeq + 1),
		Date:        intent.Date,
		PeriodID:    intent.PeriodID,
		Currency:    intent.Currency,
		Description: intent.Description,
		Lines:       intent.Lines,
		PostedAt:    clock(),
	}

	dispatched, err := uc.Publisher.Publish(ctx, accounting.JournalPosted{Entry: entry}, accounting.ExpectedSequence{
		Subject: subject,
		LastSeq: lastSeq,
	})
	if err != nil {
		return accounting.JournalEntry{}, fmt.Errorf("bookkeeping: publish: %w", err)
	}
	posted, ok := dispatched.(accounting.JournalPosted)
	if !ok {
		return accounting.JournalEntry{}, fmt.Errorf("bookkeeping: publisher returned %T, want JournalPosted", dispatched)
	}
	return posted.Entry, nil
}

// Handle validates intent and, if clean, executes it.
func (uc PostJournal) Handle(ctx context.Context, intent accounting.JournalIntent) (accounting.JournalEntry, error) {
	if err := uc.Validate(ctx, intent); err != nil {
		return accounting.JournalEntry{}, err
	}
	return uc.Execute(ctx, intent)
}
