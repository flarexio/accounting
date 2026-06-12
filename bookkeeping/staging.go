package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// Staging buffers the events a multi-step request produces so they reach the
// bus only on Commit — or are dropped on Abort, leaving nothing in the ledger.
//
// It hands use cases a Repository view whose EntryCount and LastSequence include
// the buffered entries, so several entries produced in one request number and
// sequence themselves as a dense, contiguous batch (the offsets ClosePeriod
// otherwise computes by hand). It is request-scoped and used from a single
// goroutine, so it holds no lock.
type Staging struct {
	repo      accounting.LedgerRepository
	publisher Publisher
	events    []stagedEvent
}

type stagedEvent struct {
	event  Event
	expect accounting.ExpectedSequence
}

// NewStaging buffers onto repo/publisher; reads delegate to repo, publishes are held until Commit.
func NewStaging(repo accounting.LedgerRepository, publisher Publisher) *Staging {
	return &Staging{repo: repo, publisher: publisher}
}

// Repo is the repository view to hand to use cases: every read delegates to the
// underlying repo except EntryCount and LastSequence, which add the buffered
// offsets so the next entry's number and sequence follow the staged ones.
func (s *Staging) Repo() accounting.LedgerRepository {
	return stagingRepo{LedgerRepository: s.repo, staging: s}
}

// Publisher is the publisher to hand to use cases: Publish buffers the event instead of reaching the bus.
func (s *Staging) Publisher() Publisher {
	return stagingPublisher{staging: s}
}

// Pending reports how many events are buffered.
func (s *Staging) Pending() int { return len(s.events) }

// Commit publishes every buffered event to the bus in order, then clears the
// buffer. A publish failure stops the flush and returns the error, keeping the
// unpublished tail; events already published stay published (JetStream has no
// atomic multi-message publish).
func (s *Staging) Commit(ctx context.Context) error {
	total := len(s.events)
	for i, e := range s.events {
		if err := s.publisher.Publish(ctx, e.event, e.expect); err != nil {
			s.events = s.events[i:]
			return fmt.Errorf("bookkeeping: commit staged event %d/%d: %w", i+1, total, err)
		}
	}
	s.events = nil
	return nil
}

// Abort discards the buffer; nothing reaches the bus.
func (s *Staging) Abort() { s.events = nil }

// stagedEntryCount counts buffered events that add a journal entry.
func (s *Staging) stagedEntryCount() uint64 {
	var n uint64
	for _, e := range s.events {
		if _, ok := e.event.(accounting.JournalPosted); ok {
			n++
		}
	}
	return n
}

// stagedSubjectCount counts buffered events published on subject.
func (s *Staging) stagedSubjectCount(subject string) uint64 {
	var n uint64
	for _, e := range s.events {
		if e.event.EventSubject() == subject {
			n++
		}
	}
	return n
}

// stagedEntries returns the journal entries buffered so far, in staged order.
func (s *Staging) stagedEntries() []accounting.JournalEntry {
	var out []accounting.JournalEntry
	for _, e := range s.events {
		if jp, ok := e.event.(accounting.JournalPosted); ok {
			out = append(out, jp.Entry)
		}
	}
	return out
}

type stagingPublisher struct{ staging *Staging }

func (p stagingPublisher) Publish(_ context.Context, evt Event, expect accounting.ExpectedSequence) error {
	p.staging.events = append(p.staging.events, stagedEvent{event: evt, expect: expect})
	return nil
}

type stagingRepo struct {
	accounting.LedgerRepository
	staging *Staging
}

func (r stagingRepo) EntryCount(ctx context.Context) (uint64, error) {
	base, err := r.LedgerRepository.EntryCount(ctx)
	if err != nil {
		return 0, err
	}
	return base + r.staging.stagedEntryCount(), nil
}

func (r stagingRepo) LastSequence(ctx context.Context, subject string) (uint64, error) {
	base, err := r.LedgerRepository.LastSequence(ctx, subject)
	if err != nil {
		return 0, err
	}
	return base + r.staging.stagedSubjectCount(subject), nil
}

// Entry, Entries, and EntriesByPeriod overlay the staged-but-uncommitted entries
// on the underlying repo so a later action in the same request — and the recall
// tools (get_entry) — see an entry the request has already posted but not committed.

func (r stagingRepo) Entry(ctx context.Context, id string) (accounting.JournalEntry, bool, error) {
	for _, e := range r.staging.stagedEntries() {
		if e.ID == id {
			return e, true, nil
		}
	}
	return r.LedgerRepository.Entry(ctx, id)
}

func (r stagingRepo) Entries(ctx context.Context) ([]accounting.JournalEntry, error) {
	base, err := r.LedgerRepository.Entries(ctx)
	if err != nil {
		return nil, err
	}
	return append(base, r.staging.stagedEntries()...), nil
}

func (r stagingRepo) EntriesByPeriod(ctx context.Context, periodID string) ([]accounting.JournalEntry, error) {
	base, err := r.LedgerRepository.EntriesByPeriod(ctx, periodID)
	if err != nil {
		return nil, err
	}
	for _, e := range r.staging.stagedEntries() {
		if e.PeriodID == periodID {
			base = append(base, e)
		}
	}
	return base, nil
}
