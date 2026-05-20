package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"

	"github.com/flarexio/stoa/llm"
)

var errNoOptions = errors.New("tui: no agent/scenario options to choose from")

type eventMsg llm.CycleEvent

type turnDoneMsg struct {
	outcome Outcome
	err     error
}

// chanSink forwards events onto a channel the Bubble Tea command drains;
// a blocked send is released by ctx cancellation.
type chanSink struct {
	events chan<- llm.CycleEvent
}

func (s chanSink) Emit(ctx context.Context, event llm.CycleEvent) error {
	select {
	case s.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitForTurn returns the next event, or the turn result once events is closed.
func waitForTurn(events <-chan llm.CycleEvent, done <-chan turnDoneMsg) tea.Cmd {
	return func() tea.Msg {
		if ev, ok := <-events; ok {
			return eventMsg(ev)
		}
		return <-done
	}
}
