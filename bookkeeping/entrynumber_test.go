package bookkeeping_test

import (
	"context"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// divergedRepo simulates the NATS case: the journal subject's stream sequence
// is inflated by non-journal events (LastSequence) while few journal entries
// exist (EntryCount). The embedded nil interface panics if any other method is
// called, so the test pins exactly what PostJournal.Execute reads.
type divergedRepo struct {
	accounting.LedgerRepository
	lastSeq uint64
	count   uint64
}

func (r divergedRepo) LastSequence(context.Context, string) (uint64, error) { return r.lastSeq, nil }
func (r divergedRepo) EntryCount(context.Context) (uint64, error)           { return r.count, nil }

type capturePublisher struct {
	evt    bookkeeping.Event
	expect accounting.ExpectedSequence
}

func (p *capturePublisher) Publish(_ context.Context, evt bookkeeping.Event, expect accounting.ExpectedSequence) error {
	p.evt, p.expect = evt, expect
	return nil
}

func TestPostJournal_EntryNumberIsDenseNotStreamSequence(t *testing.T) {
	// 62 prior stream events (e.g. a seeded chart), but no journal entries yet.
	repo := divergedRepo{lastSeq: 62, count: 0}
	pub := &capturePublisher{}
	uc := bookkeeping.PostJournal{Repo: repo, Publisher: pub, Clock: func() time.Time { return time.Unix(0, 0).UTC() }}

	entry, err := uc.Execute(context.Background(), balancedIntent())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The number is dense (first journal entry), not the stream position.
	if entry.ID != "JE-0001" {
		t.Fatalf("entry number should be dense JE-0001, got %q", entry.ID)
	}
	if posted := pub.evt.(accounting.JournalPosted); posted.Entry.ID != "JE-0001" {
		t.Fatalf("published entry id: %q", posted.Entry.ID)
	}
	// The optimistic-concurrency hint stays the per-subject stream sequence.
	if pub.expect.LastSeq != 62 {
		t.Fatalf("concurrency hint should be the stream seq 62, got %d", pub.expect.LastSeq)
	}
}
