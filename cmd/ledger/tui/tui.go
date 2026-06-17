// Package tui is the Bubble Tea conversational front-end for ledger. It is
// pure presentation: cmd/ledger composes the agents and injects them as Options.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/accounting"
	"github.com/flarexio/stoa/harness/loop"
)

// Session is one composed agent the TUI runs user turns against.
type Session interface {
	// Run executes one turn, streaming cycle events to sink as they happen.
	Run(ctx context.Context, request string, sink loop.EventSink) (Outcome, error)
	Close() error
}

// EntryLookup is implemented by Sessions that can fetch a posted entry; the
// TUI uses it to render reverse_journal previews from the original lines.
type EntryLookup interface {
	LookupEntry(ctx context.Context, entryID string) (accounting.JournalEntry, bool, error)
}

// AccountLookup is implemented by Sessions that can resolve an account code to
// its chart-of-accounts row; the TUI uses it to label preview lines by name.
type AccountLookup interface {
	LookupAccount(ctx context.Context, code string) (accounting.Account, bool, error)
}

// CounterpartyAdmin is implemented by Sessions that can list and register counterparties.
type CounterpartyAdmin interface {
	Counterparties(ctx context.Context) ([]accounting.Counterparty, error)
	AddCounterparty(ctx context.Context, draft accounting.Counterparty) (accounting.Counterparty, error)
}

// Outcome is the non-event summary of a completed turn.
type Outcome struct {
	Turns   int
	Summary string
}

// Option is one branch the TUI can run a session for: Label is its name, Hint
// its id (used by /branch). The first option is started on launch; /branch
// switches between them. Start composes the session lazily.
type Option struct {
	Label string
	Hint  string
	Start func(ctx context.Context) (Session, error)
}

// Run starts the Bubble Tea program and blocks until the user quits.
func Run(ctx context.Context, options []Option) error {
	if len(options) == 0 {
		return errNoOptions
	}
	p := tea.NewProgram(newModel(ctx, options), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}
