package inproc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
)

func sampleEvent() accounting.JournalPosted {
	return accounting.JournalPosted{
		Entry: accounting.JournalEntry{
			Date:        accounting.NewDate(2026, 5, 12),
			PeriodID:    "2026-05",
			Currency:    "USD",
			Description: "Demo",
			Lines: []accounting.JournalLine{
				{AccountCode: "5200", Side: accounting.SideDebit, Amount: 10000},
				{AccountCode: "2100", Side: accounting.SideCredit, Amount: 10000},
			},
		},
	}
}

func captureJournal(observed *[]accounting.JournalPosted) bookkeeping.EventHandler {
	return bookkeeping.EventHandlerFunc(func(_ context.Context, evt bookkeeping.Event) error {
		*observed = append(*observed, evt.(accounting.JournalPosted))
		return nil
	})
}

func TestBus_PublishCarriesEntryIDAndSequenceInContext(t *testing.T) {
	ctx := context.Background()
	bus := inproc.NewAccountingBus()

	var observed []accounting.JournalPosted
	var meta accounting.EventMeta
	handler := bookkeeping.EventHandlerFunc(func(c context.Context, evt bookkeeping.Event) error {
		observed = append(observed, evt.(accounting.JournalPosted))
		meta, _ = accounting.EventMetaFrom(c)
		return nil
	})
	if err := bus.Subscribe(bookkeeping.NewRouter().On(accounting.SubjectJournalPosted, handler)); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	in := sampleEvent()
	in.Entry.ID = accounting.FormatEntryID(1) // Entry.ID is producer-assigned.

	if err := bus.Publish(ctx, in, accounting.ExpectedSequence{Subject: accounting.SubjectJournalPosted, LastSeq: 0}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	if len(observed) != 1 {
		t.Fatalf("expected one handler call, got %d", len(observed))
	}
	if observed[0].Entry.ID != in.Entry.ID {
		t.Fatalf("handler saw different ID than producer set: %q vs %q", observed[0].Entry.ID, in.Entry.ID)
	}
	if meta.Subject != accounting.SubjectJournalPosted || meta.Sequence != 1 {
		t.Fatalf("expected EventMeta {subject, seq=1} in ctx, got %+v", meta)
	}
}

func TestBus_RejectsStaleExpectedSequence(t *testing.T) {
	ctx := context.Background()
	bus := inproc.NewAccountingBus()

	if err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{Subject: accounting.SubjectJournalPosted, LastSeq: 0}); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{Subject: accounting.SubjectJournalPosted, LastSeq: 0})
	if !errors.Is(err, accounting.ErrConcurrentUpdate) {
		t.Fatalf("expected ErrConcurrentUpdate, got %v", err)
	}
}

func TestBus_SkipsConcurrencyCheckWhenSubjectEmpty(t *testing.T) {
	ctx := context.Background()
	bus := inproc.NewAccountingBus()

	if err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{}); err != nil {
		t.Fatalf("second publish: %v", err)
	}
}

func TestBus_RouterDispatchesBySubject(t *testing.T) {
	ctx := context.Background()
	bus := inproc.NewAccountingBus()

	var (
		journal []accounting.JournalPosted
		closure []accounting.PeriodClosure
	)
	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, captureJournal(&journal)).
		On(accounting.SubjectPeriodClosure, bookkeeping.EventHandlerFunc(func(_ context.Context, evt bookkeeping.Event) error {
			closure = append(closure, evt.(accounting.PeriodClosure))
			return nil
		}))
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	if err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{}); err != nil {
		t.Fatal(err)
	}
	pc := accounting.PeriodClosure{Period: accounting.Period{ID: "2026-05", Status: accounting.PeriodClosed}}
	if err := bus.Publish(ctx, pc, accounting.ExpectedSequence{}); err != nil {
		t.Fatal(err)
	}

	if len(journal) != 1 {
		t.Fatalf("expected one journal handler call, got %d", len(journal))
	}
	if len(closure) != 1 {
		t.Fatalf("expected one closure handler call, got %d", len(closure))
	}
	if closure[0].Period.ID != "2026-05" {
		t.Fatalf("closure dispatched with wrong period: %+v", closure[0].Period)
	}
}

func TestBus_HandlerErrorPropagates(t *testing.T) {
	ctx := context.Background()
	bus := inproc.NewAccountingBus()

	want := errors.New("boom")
	if err := bus.Subscribe(bookkeeping.NewRouter().On(accounting.SubjectJournalPosted, bookkeeping.EventHandlerFunc(func(_ context.Context, _ bookkeeping.Event) error {
		return want
	}))); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	err := bus.Publish(ctx, sampleEvent(), accounting.ExpectedSequence{})
	if !errors.Is(err, want) {
		t.Fatalf("expected handler error to propagate, got %v", err)
	}
}
