package accounting

import (
	"context"
	"errors"
	"fmt"
)

// SubjectJournalPosted is the bus subject JournalPosted events are published on.
const SubjectJournalPosted = "accounting.journal"

// SubjectPeriodClosure is the bus subject PeriodClosure events are published on.
const SubjectPeriodClosure = "accounting.period.closure"

// Subjects for the reference-data events `ledger seed` emits, one per entity.
const (
	SubjectCompanyConfigured = "accounting.company.configured"
	SubjectAccountAdded      = "accounting.account.added"
	SubjectBranchAdded       = "accounting.branch.added"
	SubjectPeriodAdded       = "accounting.period.added"
)

// SubjectPolicySet is the bus subject PolicySet events are published on.
const SubjectPolicySet = "accounting.company.policy"

// JournalPosted is the domain event emitted after a JournalIntent has been
// validated and the broker has accepted it. Entry.ID is producer-assigned and
// carried through the body. Relations carries any JournalRelation rows built
// alongside the entry (e.g. a reversal's link to its original); AppendEntry
// writes the entry and all relations in one transaction.
type JournalPosted struct {
	Entry     JournalEntry      `json:"entry"`
	Relations []JournalRelation `json:"relations,omitempty"`
}

// EventSubject reports the bus subject JournalPosted lives on.
func (JournalPosted) EventSubject() string { return SubjectJournalPosted }

// CompanyConfigured, AccountAdded, BranchAdded, and PeriodAdded are the
// reference-data events `ledger seed` emits, one per entity, for the projection
// handlers to upsert.
type CompanyConfigured struct {
	Company Company `json:"company"`
}

// EventSubject reports the bus subject CompanyConfigured lives on.
func (CompanyConfigured) EventSubject() string { return SubjectCompanyConfigured }

// AccountAdded carries one chart account for the projection to upsert.
type AccountAdded struct {
	Account Account `json:"account"`
}

// EventSubject reports the bus subject AccountAdded lives on.
func (AccountAdded) EventSubject() string { return SubjectAccountAdded }

// BranchAdded carries one reporting branch for the projection to upsert.
type BranchAdded struct {
	Branch Branch `json:"branch"`
}

// EventSubject reports the bus subject BranchAdded lives on.
func (BranchAdded) EventSubject() string { return SubjectBranchAdded }

// PeriodAdded carries one accounting period for the projection to upsert.
type PeriodAdded struct {
	Period Period `json:"period"`
}

// EventSubject reports the bus subject PeriodAdded lives on.
func (PeriodAdded) EventSubject() string { return SubjectPeriodAdded }

// PolicySet carries the company's bookkeeping policy for the projection to
// store; the empty string clears it.
type PolicySet struct {
	Policy string `json:"policy"`
}

// EventSubject reports the bus subject PolicySet lives on.
func (PolicySet) EventSubject() string { return SubjectPolicySet }

// PeriodClosure is the domain event emitted by ClosePeriod after every
// closing entry for the period has been published. The subscribed handler is
// the only writer that flips Period.Status to closed in the projection, so
// this is the event-sourced counterpart of JournalPosted for period state
// transitions.
type PeriodClosure struct {
	Period Period `json:"period"`
}

// EventSubject reports the bus subject PeriodClosure lives on.
func (PeriodClosure) EventSubject() string { return SubjectPeriodClosure }

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

// EventMeta is the bus subject and broker sequence a projection records for a
// delivered event; it travels in the context, not on domain-typed signatures.
type EventMeta struct {
	Subject  string
	Sequence uint64
}

type eventMetaKey struct{}

// WithEventMeta returns ctx carrying meta for a projection write to read.
func WithEventMeta(ctx context.Context, meta EventMeta) context.Context {
	return context.WithValue(ctx, eventMetaKey{}, meta)
}

// EventMetaFrom returns the EventMeta carried by ctx; ok is false when none is set.
func EventMetaFrom(ctx context.Context) (EventMeta, bool) {
	meta, ok := ctx.Value(eventMetaKey{}).(EventMeta)
	return meta, ok
}
