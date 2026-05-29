package bookkeeping

import (
	"context"
	"io"

	"github.com/flarexio/accounting"
)

// Event is the polymorphic envelope every transport publishes and delivers.
// Concrete events (accounting.JournalPosted, accounting.PeriodClosure) carry
// their own payloads; EventSubject names the bus subject the event lives on
// so a publisher can route by it without case-switching on the concrete type.
type Event interface {
	EventSubject() string
}

// EventHandler consumes a delivered Event of the type bound to its registered
// subject. The Router guarantees that handler only receives events whose
// subject matches its registration, so the implementation may type-assert
// without a fallback switch.
type EventHandler interface {
	Handle(ctx context.Context, evt Event) error
}

// EventHandlerFunc adapts an ordinary function to EventHandler.
type EventHandlerFunc func(ctx context.Context, evt Event) error

func (f EventHandlerFunc) Handle(ctx context.Context, evt Event) error {
	return f(ctx, evt)
}

// Publisher publishes an Event through a transport, which assigns the
// broker-side identifiers carried on the returned value.
type Publisher interface {
	Publish(ctx context.Context, evt Event, expect accounting.ExpectedSequence) (Event, error)
}

// Subscriber installs a Router as the sole subscription point for the bus.
// A transport calls back into the router on every delivered message to look
// up the registered handler.
type Subscriber interface {
	Subscribe(router *Router) error
}

// EventBus is the bidirectional transport contract for the bookkeeping flow.
type EventBus interface {
	Publisher
	Subscriber
	io.Closer
}

// Router is the per-subject dispatch table the bus consults on every message.
// Construct one in composition (cmd/ledger/compose.go), register each event
// type's handler with On, and hand the router to bus.Subscribe; the bus owns
// the consume loop, the router owns the routing.
type Router struct {
	routes map[string]EventHandler
}

// NewRouter returns an empty Router.
func NewRouter() *Router {
	return &Router{routes: map[string]EventHandler{}}
}

// On registers handler for events whose subject is subject. The last
// registration wins; the receiver is returned so registrations chain.
func (r *Router) On(subject string, handler EventHandler) *Router {
	r.routes[subject] = handler
	return r
}

// Handler returns the registered handler for subject, or nil if none is
// registered. Transports use it to decide whether to ack or nak.
func (r *Router) Handler(subject string) EventHandler {
	return r.routes[subject]
}

// Subjects returns every registered subject; transports use it to drive their
// broker-side subject filter list.
func (r *Router) Subjects() []string {
	out := make([]string, 0, len(r.routes))
	for s := range r.routes {
		out = append(out, s)
	}
	return out
}
