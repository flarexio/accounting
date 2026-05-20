package inproc

import (
	"context"
	"sync"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// accountingBus is the in-process bookkeeping.EventBus. It dispatches every
// published event synchronously to all subscribed handlers under one mutex.
// Optimistic concurrency mirrors NATS JetStream's
// Nats-Expected-Last-Subject-Sequence: a stale ExpectedSequence.LastSeq is
// rejected with accounting.ErrConcurrentUpdate before any handler runs.
type accountingBus struct {
	mu        sync.Mutex
	streamSeq uint64
	lastSubj  map[string]uint64
	handlers  []bookkeeping.EventHandler
}

// NewAccountingBus returns an empty in-process bookkeeping.EventBus.
func NewAccountingBus() bookkeeping.EventBus {
	return &accountingBus{lastSubj: make(map[string]uint64)}
}

// Subscribe registers handler to receive every subsequent JournalPosted.
// Handlers run in registration order on the publishing goroutine.
func (b *accountingBus) Subscribe(handler bookkeeping.EventHandler) error {
	b.mu.Lock()
	b.handlers = append(b.handlers, handler)
	b.mu.Unlock()
	return nil
}

// Close is a no-op: the in-process bus owns nothing.
func (b *accountingBus) Close() error {
	return nil
}

func (b *accountingBus) Publish(ctx context.Context, evt accounting.JournalPosted, expect accounting.ExpectedSequence) (accounting.JournalPosted, error) {
	b.mu.Lock()
	if expect.Subject != "" {
		if b.lastSubj[expect.Subject] != expect.LastSeq {
			b.mu.Unlock()
			return accounting.JournalPosted{}, accounting.ErrConcurrentUpdate
		}
	}
	b.streamSeq++
	seq := b.streamSeq
	if expect.Subject != "" {
		b.lastSubj[expect.Subject] = seq
	}
	handlers := append([]bookkeeping.EventHandler(nil), b.handlers...)
	b.mu.Unlock()

	evt.Subject = expect.Subject
	evt.Sequence = seq

	for _, h := range handlers {
		if err := h.Handle(ctx, evt); err != nil {
			return evt, err
		}
	}
	return evt, nil
}
