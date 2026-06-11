package accounting

import "context"

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
