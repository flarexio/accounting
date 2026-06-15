package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
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
			"execute loop the book-run command uses. Drives a real LLM through the OpenAI-\n" +
			"compatible engine; set llm.api_key in config or export OPENAI_API_KEY. The TUI\n" +
			"connects to the ledger seeded by `ledger seed` and reads the single company from\n" +
			"the repository; it never seeds on startup and takes no arguments.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "model",
				Usage: "model name for the openai engine; overrides config.yaml llm.model",
			},
			&cli.IntFlag{
				Name:  "max-turns",
				Usage: "maximum reasoning turns per request",
				Value: 8,
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
		llmCfg:   cfg.LLM,
		maxTurns: int(c.Int("max-turns")),
	}
	if model := c.String("model"); model != "" {
		comp.llmCfg.Model = model
	}

	repo, repoCloser, err := buildRepository(ctx, comp.cfg.Persistence, comp.cfg.Embedding)
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	defer repoCloser.Close()

	bus, err := buildMessaging(ctx, comp.cfg.Messaging, repo)
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	defer bus.Close()

	period, err := firstOpenPeriod(ctx, repo)
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	if period.ID == "" {
		return fmt.Errorf("tui: ledger has no open period; run `ledger seed` first")
	}

	branches, err := repo.Branches(ctx)
	if err != nil {
		return fmt.Errorf("tui: load branches: %w", err)
	}
	if len(branches) == 0 {
		return fmt.Errorf("tui: ledger has no branches; run `ledger seed` first")
	}

	options := make([]tui.Option, len(branches))
	for i, br := range branches {
		options[i] = comp.bookOption(repo, bus, br)
	}

	return tui.Run(ctx, options)
}

// tuiComposer builds the bookkeeper session from config and CLI flags.
type tuiComposer struct {
	cfg      *config.Config
	llmCfg   config.LLM
	maxTurns int
}

// bookOption is one branch-scoped bookkeeper session. The repo and bus live
// for the lifetime of the TUI process (closed in runTUI), so Start only
// builds the per-session engine.
func (comp tuiComposer) bookOption(repo accounting.LedgerRepository, bus bookkeeping.EventBus, branch accounting.Branch) tui.Option {
	return tui.Option{
		Label: branch.Name,
		Hint:  branch.ID,
		Start: func(ctx context.Context) (tui.Session, error) {
			engine, err := buildBookEngine(ctx, repo, comp.llmCfg, branch.ID)
			if err != nil {
				return nil, err
			}
			return &bookSession{
				agent: bookkeeper.Bookkeeper{
					Engine:    engine,
					Repo:      repo,
					Publisher: bus,
					MaxTurns:  comp.maxTurns,
					Recent:    bookkeeper.NewRecentEntries(5),
				},
				repo: repo,
			}, nil
		},
	}
}

type bookSession struct {
	agent bookkeeper.Bookkeeper
	repo  accounting.LedgerRepository
}

func (s *bookSession) LookupEntry(ctx context.Context, entryID string) (accounting.JournalEntry, bool, error) {
	return s.repo.Entry(ctx, entryID)
}

func (s *bookSession) LookupAccount(ctx context.Context, code string) (accounting.Account, bool, error) {
	return s.repo.Account(ctx, code)
}

func (s *bookSession) Run(ctx context.Context, request string, sink loop.EventSink) (tui.Outcome, error) {
	agent := s.agent
	agent.Sink = sink
	res, err := agent.Book(ctx, request)
	out := tui.Outcome{Turns: res.Turns}
	switch len(res.Entries) {
	case 0:
	case 1:
		out.Summary = fmt.Sprintf("posted entry %s", res.Entries[0].ID)
	default:
		ids := make([]string, len(res.Entries))
		for i, e := range res.Entries {
			ids[i] = e.ID
		}
		out.Summary = fmt.Sprintf("posted %d entries: %s", len(ids), strings.Join(ids, ", "))
	}
	return out, err
}

func (s *bookSession) Close() error { return nil }
