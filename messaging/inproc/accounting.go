package inproc

import (
	"context"
	"sync"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// accountingBus is the in-process bookkeeping.EventBus. It dispatches every
// published event synchronously through the registered Router under one
// mutex. Optimistic concurrency mirrors NATS JetStream's
// Nats-Expected-Last-Subject-Sequence: a stale ExpectedSequence.LastSeq is
// rejected with accounting.ErrConcurrentUpdate before any handler runs.
//
// Each subject keeps its own stream-sequence counter so JournalPosted and
// PeriodClosure advance independently, matching NATS where each subject's
// last-sequence is tracked separately.
type accountingBus struct {
	mu        sync.Mutex
	streamSeq map[string]uint64
	router    *bookkeeping.Router
}

// NewAccountingBus returns an empty in-process bookkeeping.EventBus.
func NewAccountingBus() bookkeeping.EventBus {
	return &accountingBus{streamSeq: make(map[string]uint64)}
}

// Subscribe installs router as the bus's dispatch table. Subsequent calls
// replace the router; tests can swap it without reopening the bus.
func (b *accountingBus) Subscribe(router *bookkeeping.Router) error {
	b.mu.Lock()
	b.router = router
	b.mu.Unlock()
	return nil
}

// Close is a no-op: the in-process bus owns nothing.
func (b *accountingBus) Close() error {
	return nil
}

// Publish stamps the event with the subject from EventSubject and the next
// per-subject sequence, hands the stamped event to the router, and returns
// it. A stale ExpectedSequence is rejected before any handler runs.
func (b *accountingBus) Publish(ctx context.Context, evt bookkeeping.Event, expect accounting.ExpectedSequence) (bookkeeping.Event, error) {
	subject := evt.EventSubject()

	b.mu.Lock()
	if expect.Subject != "" {
		if b.streamSeq[expect.Subject] != expect.LastSeq {
			b.mu.Unlock()
			return nil, accounting.ErrConcurrentUpdate
		}
	}
	b.streamSeq[subject]++
	seq := b.streamSeq[subject]
	router := b.router
	b.mu.Unlock()

	stamped := stamp(evt, subject, seq)

	if router == nil {
		return stamped, nil
	}
	handler := router.Handler(subject)
	if handler == nil {
		return stamped, nil
	}
	if err := handler.Handle(ctx, stamped); err != nil {
		return stamped, err
	}
	return stamped, nil
}

// stamp returns evt with the transport-assigned Subject and Sequence written
// onto whichever concrete type backs it. Returning the same Event interface
// keeps the bus contract polymorphic without leaking the union types.
func stamp(evt bookkeeping.Event, subject string, sequence uint64) bookkeeping.Event {
	switch e := evt.(type) {
	case accounting.JournalPosted:
		e.Subject = subject
		e.Sequence = sequence
		return e
	case accounting.PeriodClosure:
		e.Subject = subject
		e.Sequence = sequence
		return e
	default:
		return evt
	}
}
