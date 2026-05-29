// Package agent wires the bookkeeping use cases through the harness loop: the
// LLM proposes a typed Intent and the bookkeeping Registry validates, routes,
// and executes it.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/llm"
)

// Bookkeeper runs one bookkeeping decision: reason -> validate -> execute.
type Bookkeeper struct {
	Engine    llm.ReasoningEngine[bookkeeping.Intent]
	Repo      accounting.LedgerRepository
	Publisher bookkeeping.Publisher
	Subject   string
	Clock     bookkeeping.Clock
	MaxTurns  int
	Sink      loop.EventSink
}

// Result is the outcome of one bookkeeping cycle.
type Result struct {
	Intent      bookkeeping.Intent
	Entry       accounting.JournalEntry
	Observation llm.Observation
	Turns       int
	Events      []llm.CycleEvent
}

// Book runs the loop for request, routing whichever Intent the model proposes through the Registry.
func (a Bookkeeper) Book(ctx context.Context, request string) (Result, error) {
	if a.Engine == nil {
		return Result{}, errors.New("bookkeeper: agent has no reasoning engine")
	}
	if a.Repo == nil {
		return Result{}, errors.New("bookkeeper: agent has no repository")
	}
	if a.Publisher == nil {
		return Result{}, errors.New("bookkeeper: agent has no event publisher")
	}

	registry := bookkeeping.NewBookkeepingRegistry(a.Repo, a.Publisher, a.Clock, a.Subject)

	var posted accounting.JournalEntry
	executor := loop.ExecutorFunc[bookkeeping.Intent](func(ctx context.Context, intent bookkeeping.Intent) (llm.Observation, error) {
		if intent.Kind == bookkeeping.IntentReject {
			reason := "request cannot be fulfilled"
			if intent.Reject != nil && intent.Reject.Reason != "" {
				reason = intent.Reject.Reason
			}
			return llm.Observation{Summary: reason}, nil
		}
		entry, err := registry.Execute(ctx, intent)
		if err != nil {
			return llm.Observation{}, err
		}
		posted = entry
		return llm.Observation{
			Summary: fmt.Sprintf("Posted journal entry %s for %s with %d line(s).",
				entry.ID, entry.Description, len(entry.Lines)),
			Fields: map[string]string{
				"entry_id":  entry.ID,
				"period_id": entry.PeriodID,
				"currency":  entry.Currency,
			},
		}, nil
	})

	runner := loop.Runner[bookkeeping.Intent]{
		Engine:    a.Engine,
		Validator: registry,
		Executor:  executor,
		Tools:     accountTools(a.Repo),
		MaxTurns:  a.MaxTurns,
		Sink:      a.Sink,
	}

	out, err := runner.Run(ctx, llm.ReasoningInput{
		Task: request,
	})
	return Result{
		Intent:      out.Reasoning.Intent,
		Entry:       posted,
		Observation: out.Observation,
		Turns:       out.Turns,
		Events:      out.Events,
	}, err
}
