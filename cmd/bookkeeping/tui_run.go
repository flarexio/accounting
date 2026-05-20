package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/cmd/bookkeeping/tui"
	"github.com/flarexio/accounting/config"
	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/world"

	bookkeeper "github.com/flarexio/accounting/agent"
)

func newTUICommand() *cli.Command {
	return &cli.Command{
		Name:      "tui",
		Usage:     "Launch the conversational Bubble Tea terminal UI.",
		ArgsUsage: "<scenario.json> [scenario.json ...]",
		Description: "Launches a conversational terminal UI over the same reason -> validate ->\n" +
			"execute loop the book-run / npc-run commands use. Pass one or more scenario\n" +
			"JSON files: accounting scenarios become bookkeeper sessions, world scenarios\n" +
			"become one npc session per actor. Bookkeeper sessions read config.yaml from\n" +
			"--work-dir (default ~/.flarex/stoa) and connect to a ledger already seeded\n" +
			"by `stoa seed` -- the TUI never seeds on startup; npc sessions need no config.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "engine",
				Usage: "bookkeeper reasoning engine: scripted (offline) or openai (live); overrides config.yaml llm.engine",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "model name for the openai engine; overrides config.yaml llm.model",
			},
			&cli.IntFlag{
				Name:  "amount",
				Usage: "amount in minor currency units for the scripted engine's balanced journal",
				Value: 10000,
			},
			&cli.StringFlag{
				Name:  "currency",
				Usage: "ISO currency code used by the scripted engine",
				Value: "USD",
			},
			&cli.IntFlag{
				Name:  "max-turns",
				Usage: "maximum reasoning turns per request",
				Value: 3,
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "stoa work directory holding config.yaml; defaults to ~/.flarex/stoa",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runTUI(ctx, c)
		},
	}
}

func runTUI(ctx context.Context, c *cli.Command) error {
	paths := c.Args().Slice()
	if len(paths) == 0 {
		return errors.New("tui: provide at least one scenario JSON file (accounting or world)")
	}

	// Classify up front so config.yaml is only required when a bookkeeper scenario is present.
	type classified struct {
		path   string
		acc    accounting.Scenario
		wld    world.Scenario
		isBook bool
	}
	var items []classified
	needConfig := false
	for _, path := range paths {
		acc, wld, isBook, err := classifyScenario(path)
		if err != nil {
			return err
		}
		items = append(items, classified{path: path, acc: acc, wld: wld, isBook: isBook})
		needConfig = needConfig || isBook
	}

	comp := tuiComposer{
		engineKind: c.String("engine"),
		model:      c.String("model"),
		amount:     int64(c.Int("amount")),
		currency:   c.String("currency"),
		maxTurns:   int(c.Int("max-turns")),
	}
	if needConfig {
		cfg, err := loadBookConfig(c.String("work-dir"))
		if err != nil {
			return fmt.Errorf("tui: %w", err)
		}
		comp.cfg = cfg
		if comp.engineKind == "" {
			comp.engineKind = string(cfg.LLM.Engine)
		}
		if comp.model == "" {
			comp.model = cfg.LLM.Model
		}
	}

	var options []tui.Option
	for _, it := range items {
		if it.isBook {
			options = append(options, comp.bookOption(it.path, it.acc))
		}
	}

	return tui.Run(ctx, options)
}

// classifyScenario picks accounting vs world by trial decode (both loaders reject unknown fields).
func classifyScenario(path string) (accounting.Scenario, world.Scenario, bool, error) {
	if acc, err := accounting.LoadScenarioFile(path); err == nil {
		return acc, world.Scenario{}, true, nil
	}
	if wld, err := world.LoadScenarioFile(path); err == nil {
		return accounting.Scenario{}, wld, false, nil
	}
	return accounting.Scenario{}, world.Scenario{}, false,
		fmt.Errorf("tui: %s is not a recognized accounting or world scenario", path)
}

// tuiComposer turns classified scenarios into tui.Options.
type tuiComposer struct {
	cfg        *config.Config
	engineKind string
	model      string
	amount     int64
	currency   string
	maxTurns   int
}

// bookOption is a selectable bookkeeper session; the TUI never seeds, it
// connects to a ledger already seeded by `stoa seed`.
func (comp tuiComposer) bookOption(path string, scenario accounting.Scenario) tui.Option {
	return tui.Option{
		Label: "bookkeeper · " + scenarioLabel(scenario.Name, path),
		Hint:  path,
		Start: func(ctx context.Context) (tui.Session, error) {
			repo, repoCloser, err := buildRepository(ctx, comp.cfg.Persistence)
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
				return nil, fmt.Errorf("tui: ledger has no open period; run `stoa seed` first")
			}
			engine, err := buildBookEngine(ctx, comp.engineKind, scenario, repo, comp.amount, comp.currency, comp.model)
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

func scenarioLabel(name, path string) string {
	if name != "" {
		return name
	}
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
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
