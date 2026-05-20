package bookkeeping

import (
	"context"
	"io"

	"github.com/flarexio/accounting"
)

// EventPublisher publishes a JournalPosted through a transport, which assigns
// Subject and Sequence. Callers use the returned event when they need the
// broker-assigned identifiers.
type EventPublisher interface {
	Publish(ctx context.Context, evt accounting.JournalPosted, expect accounting.ExpectedSequence) (accounting.JournalPosted, error)
}

// EventHandler consumes a JournalPosted, typically projecting it into a
// LedgerRepository.
type EventHandler interface {
	Handle(ctx context.Context, evt accounting.JournalPosted) error
}

// EventHandlerFunc adapts an ordinary function to EventHandler.
type EventHandlerFunc func(ctx context.Context, evt accounting.JournalPosted) error

func (f EventHandlerFunc) Handle(ctx context.Context, evt accounting.JournalPosted) error {
	return f(ctx, evt)
}

// EventSubscriber registers a handler with a transport, which owns per-message
// context, ack/nak, and concurrency.
type EventSubscriber interface {
	Subscribe(handler EventHandler) error
}

// EventBus is the bidirectional transport contract for the bookkeeping flow.
type EventBus interface {
	EventPublisher
	EventSubscriber
	io.Closer
}
