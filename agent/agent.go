// Package agent wires the bookkeeping use cases through the harness loop: the
// LLM proposes a typed Intent and the bookkeeping Registry validates, routes,
// and executes it.
package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/llm"
)

// maxPostsPerRequest caps how many entries one request may post, bounding a
// runaway multi-action loop independently of MaxTurns (which also counts tool
// calls). Reached, the executor refuses further postings until the model
// finishes or rejects.
const maxPostsPerRequest = 12

// Bookkeeper runs one bookkeeping decision: reason -> validate -> execute.
type Bookkeeper struct {
	Engine    llm.ReasoningEngine[bookkeeping.Intent]
	Repo      accounting.LedgerRepository
	Publisher bookkeeping.Publisher
	Subject   string
	Clock     bookkeeping.Clock
	MaxTurns  int
	Sink      loop.EventSink
	// Recent, when set, is the session's bounded recent-entries buffer: posted
	// entries are recorded into it and the recall tools (recent_entries,
	// get_entry) read it. Nil disables recall (e.g. one-shot book-run).
	Recent *RecentEntries
}

// Result is the outcome of one bookkeeping cycle.
type Result struct {
	Intent      bookkeeping.Intent
	Entries     []accounting.JournalEntry // every committed entry, in order; empty on reject/abort
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

	// Staging makes the request commit all-or-nothing.
	staging := bookkeeping.NewStaging(a.Repo, a.Publisher)
	registry := bookkeeping.NewBookkeepingRegistry(staging.Repo(), staging.Publisher(), a.Clock, a.Subject)

	var posted []accounting.JournalEntry
	executor := loop.ExecutorFunc[bookkeeping.Intent](func(ctx context.Context, intent bookkeeping.Intent) (llm.Observation, error) {
		if intent.Kind == bookkeeping.IntentReject {
			reason := "request cannot be fulfilled"
			if intent.Reject != nil && intent.Reject.Reason != "" {
				reason = intent.Reject.Reason
			}
			return llm.Observation{Summary: reason}, nil
		}
		if len(posted) >= maxPostsPerRequest {
			return llm.Observation{}, fmt.Errorf("bookkeeping: request already posted %d entries; finish it (mark a final action or reject)", maxPostsPerRequest)
		}
		entry, err := registry.Execute(ctx, intent)
		if err != nil {
			return llm.Observation{}, err
		}
		posted = append(posted, entry)
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
		Tools:     a.toolsFor(staging.Repo()),
		MaxTurns:  a.MaxTurns,
		Sink:      a.Sink,
	}

	out, err := runner.Run(ctx, llm.ReasoningInput{
		Task: request,
	})

	// Commit on a clean run, abort on any failure.
	committed := posted
	if err != nil {
		staging.Abort()
		committed = nil
	} else if cErr := staging.Commit(ctx); cErr != nil {
		err = cErr
		committed = nil
	} else {
		for _, e := range committed {
			a.Recent.Add(e)
		}
	}

	return Result{
		Intent:      out.Reasoning.Intent,
		Entries:     committed,
		Observation: out.Observation,
		Turns:       out.Turns,
		Events:      out.Events,
	}, err
}

// toolsFor exposes find_accounts and find_counterparties plus, when a
// recent-entries buffer is present, the recall tools (recent_entries,
// get_entry). repo is the staging view so get_entry also resolves
// staged-but-uncommitted entries.
func (a Bookkeeper) toolsFor(repo accounting.LedgerRepository) map[string]loop.Tool {
	tools := accountTools(repo)
	maps.Copy(tools, counterpartyTools(repo))
	if a.Recent != nil {
		maps.Copy(tools, recallTools(repo, a.Recent))
	}
	return tools
}
