package accounting

import (
	"errors"
	"fmt"
)

// JournalPosted is the domain event emitted after a JournalIntent has been
// validated and the broker has accepted it. Subject and Sequence are
// transport-assigned and excluded from JSON; Entry.ID is producer-assigned and
// carried through the body. Relations carries any JournalRelation rows built
// alongside the entry (e.g. a reversal's link to its original); Apply writes
// the entry and all relations in one transaction.
type JournalPosted struct {
	Subject   string            `json:"-"`
	Sequence  uint64            `json:"-"`
	Entry     JournalEntry      `json:"entry"`
	Relations []JournalRelation `json:"relations,omitempty"`
}

// FormatEntryID formats a per-subject counter into the canonical JournalEntry.ID.
func FormatEntryID(seq uint64) string {
	return fmt.Sprintf("JE-%04d", seq)
}

// ExpectedSequence is the optimistic-concurrency hint passed to
// EventPublisher.Publish. A zero value (empty Subject) skips the check.
type ExpectedSequence struct {
	Subject string
	LastSeq uint64
}

// ErrConcurrentUpdate is returned when a publish is rejected because the
// producer's ExpectedSequence is stale; the producer should re-read and retry.
var ErrConcurrentUpdate = errors.New("accounting: concurrent update on subject")
