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

// CatchUp is a no-op: the in-process bus dispatches synchronously on Publish,
// so the projection is always current.
func (b *accountingBus) CatchUp(context.Context) error {
	return nil
}

// Close is a no-op: the in-process bus owns nothing.
func (b *accountingBus) Close() error {
	return nil
}

// Publish assigns the next per-subject sequence and dispatches evt to the
// router under the EventMeta context. A stale ExpectedSequence is rejected
// before any handler runs.
func (b *accountingBus) Publish(ctx context.Context, evt bookkeeping.Event, expect accounting.ExpectedSequence) error {
	subject := evt.EventSubject()

	b.mu.Lock()
	if expect.Subject != "" {
		if b.streamSeq[expect.Subject] != expect.LastSeq {
			b.mu.Unlock()
			return accounting.ErrConcurrentUpdate
		}
	}
	b.streamSeq[subject]++
	seq := b.streamSeq[subject]
	router := b.router
	b.mu.Unlock()

	if router == nil {
		return nil
	}
	handler := router.Handler(subject)
	if handler == nil {
		return nil
	}
	ctx = accounting.WithEventMeta(ctx, accounting.EventMeta{Subject: subject, Sequence: seq})
	return handler.Handle(ctx, evt)
}
