package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting/cmd/ledger/tui"
	"github.com/flarexio/accounting/config"
	"github.com/flarexio/stoa/harness/loop"

	bookkeeper "github.com/flarexio/accounting/agent"
)

func newTUICommand() *cli.Command {
	return &cli.Command{
		Name:  "tui",
		Usage: "Launch the conversational Bubble Tea terminal UI.",
		Description: "Launches a conversational terminal UI over the same reason -> validate ->\n" +
			"execute loop the book-run command uses. Drives a real LLM through the OpenAI\n" +
			"engine, so OPENAI_API_KEY must be set. The TUI connects to the ledger seeded by\n" +
			"`ledger seed` and reads the single company from the repository; it never seeds\n" +
			"on startup and takes no arguments.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "model name for the openai engine; overrides config.yaml llm.model",
			},
			&cli.IntFlag{
				Name:  "max-turns",
				Usage: "maximum reasoning turns per request",
				Value: 3,
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "accounting work directory holding config.yaml; defaults to ~/.flarex/accounting",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runTUI(ctx, c)
		},
	}
}

func runTUI(ctx context.Context, c *cli.Command) error {
	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	comp := tuiComposer{
		cfg:      cfg,
		model:    c.String("model"),
		maxTurns: int(c.Int("max-turns")),
	}
	if comp.model == "" {
		comp.model = cfg.LLM.Model
	}

	return tui.Run(ctx, []tui.Option{comp.bookOption()})
}

// tuiComposer builds the bookkeeper session from config and CLI flags.
type tuiComposer struct {
	cfg      *config.Config
	model    string
	maxTurns int
}

// bookOption is the bookkeeper session; the TUI never seeds, it connects to a
// ledger already seeded by `ledger seed` and reads the company from the repo.
func (comp tuiComposer) bookOption() tui.Option {
	return tui.Option{
		Label: "bookkeeper",
		Start: func(ctx context.Context) (tui.Session, error) {
			repo, repoCloser, err := buildRepository(ctx, comp.cfg.Persistence, comp.cfg.Embedding)
			if err != nil {
				return nil, err
			}
			bus, err := buildMessaging(ctx, comp.cfg.Messaging, repo)
			if err != nil {
				repoCloser.Close()
				return nil, err
			}
			period, err := firstOpenPeriod(ctx, repo)
			if err != nil {
				bus.Close()
				repoCloser.Close()
				return nil, err
			}
			if period.ID == "" {
				bus.Close()
				repoCloser.Close()
				return nil, fmt.Errorf("tui: ledger has no open period; run `ledger seed` first")
			}
			engine, err := buildBookEngine(ctx, repo, comp.model)
			if err != nil {
				bus.Close()
				repoCloser.Close()
				return nil, err
			}
			return &bookSession{
				agent: bookkeeper.Bookkeeper{
					Engine:    engine,
					Repo:      repo,
					Publisher: bus,
					MaxTurns:  comp.maxTurns,
				},
				closers: []io.Closer{bus, repoCloser},
			}, nil
		},
	}
}

type bookSession struct {
	agent   bookkeeper.Bookkeeper
	closers []io.Closer
}

func (s *bookSession) Run(ctx context.Context, request string, sink loop.EventSink) (tui.Outcome, error) {
	agent := s.agent
	agent.Sink = sink
	res, err := agent.Book(ctx, request)
	out := tui.Outcome{Turns: res.Turns}
	if res.Entry.ID != "" {
		out.Summary = fmt.Sprintf("posted entry %s", res.Entry.ID)
	}
	return out, err
}

func (s *bookSession) Close() error {
	var errs []error
	for _, c := range s.closers {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
