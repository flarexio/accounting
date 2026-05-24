package bookkeeping

import (
	"context"
	"fmt"
	"slices"

	"github.com/flarexio/accounting"
)

type intentRoute struct {
	validate func(ctx context.Context, intent Intent) error
	execute  func(ctx context.Context, intent Intent) (accounting.JournalEntry, error)
}

// Registry routes an Intent to the use case registered for its Kind.
// Registry.Validate and Registry.Execute match harness/loop's Validator and
// Executor shapes, so the agent hands the whole Registry to the loop.
type Registry struct {
	routes map[IntentKind]intentRoute
}

// NewBookkeepingRegistry wires every accounting use case against the shared
// ledger repository and event publisher. The kinds it routes match Intents().
func NewBookkeepingRegistry(repo accounting.LedgerRepository, pub EventPublisher, clock Clock, subject string) Registry {
	post := PostJournal{Repo: repo, Publisher: pub, Clock: clock, Subject: subject}
	reverse := ReverseJournal{Repo: repo, Publisher: pub, Clock: clock, Subject: subject}

	return Registry{routes: map[IntentKind]intentRoute{
		IntentPostJournal: {
			validate: func(ctx context.Context, intent Intent) error {
				if intent.Post == nil {
					return missingPayloadErr(IntentPostJournal)
				}
				return post.Validate(ctx, *intent.Post)
			},
			execute: func(ctx context.Context, intent Intent) (accounting.JournalEntry, error) {
				if intent.Post == nil {
					return accounting.JournalEntry{}, missingPayloadErr(IntentPostJournal)
				}
				return post.Execute(ctx, *intent.Post)
			},
		},
		IntentReverseJournal: {
			validate: func(ctx context.Context, intent Intent) error {
				if intent.Reverse == nil {
					return missingPayloadErr(IntentReverseJournal)
				}
				return reverse.Validate(ctx, *intent.Reverse)
			},
			execute: func(ctx context.Context, intent Intent) (accounting.JournalEntry, error) {
				if intent.Reverse == nil {
					return accounting.JournalEntry{}, missingPayloadErr(IntentReverseJournal)
				}
				return reverse.Execute(ctx, *intent.Reverse)
			},
		},
		IntentReject: {
			validate: func(_ context.Context, _ Intent) error { return nil },
			execute:  func(_ context.Context, _ Intent) (accounting.JournalEntry, error) { return accounting.JournalEntry{}, nil },
		},
	}}
}

// Validate routes intent to its use case's validation step. An unrecognised
// Kind is a validation failure, not a panic.
func (r Registry) Validate(ctx context.Context, intent Intent) error {
	route, ok := r.routes[intent.Kind]
	if !ok {
		return r.unknownKindErr(intent.Kind)
	}
	return route.validate(ctx, intent)
}

// Execute routes an already-validated intent to its use case's execution step.
func (r Registry) Execute(ctx context.Context, intent Intent) (accounting.JournalEntry, error) {
	route, ok := r.routes[intent.Kind]
	if !ok {
		return accounting.JournalEntry{}, r.unknownKindErr(intent.Kind)
	}
	return route.execute(ctx, intent)
}

// Kinds returns the IntentKinds the Registry routes, sorted.
func (r Registry) Kinds() []IntentKind {
	out := make([]IntentKind, 0, len(r.routes))
	for kind := range r.routes {
		out = append(out, kind)
	}
	slices.Sort(out)
	return out
}

func (r Registry) unknownKindErr(kind IntentKind) error {
	return fmt.Errorf("bookkeeping: unknown intent kind %q; expected one of %v", kind, r.Kinds())
}

func missingPayloadErr(kind IntentKind) error {
	return fmt.Errorf("bookkeeping: intent kind %q is missing its %q payload object", kind, kind)
}
