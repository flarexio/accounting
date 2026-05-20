// Package tui is the Bubble Tea conversational front-end for accounting. It is
// pure presentation: cmd/bookkeeping composes the agents and injects them as Options.
package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/stoa/harness/loop"
)

// Session is one composed agent the TUI runs user turns against.
type Session interface {
	// Run executes one turn, streaming cycle events to sink as they happen.
	Run(ctx context.Context, request string, sink loop.EventSink) (Outcome, error)
	Close() error
}

// Outcome is the non-event summary of a completed turn.
type Outcome struct {
	Turns   int
	Summary string
}

// Option is one selectable agent + scenario pairing. Start composes lazily.
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
