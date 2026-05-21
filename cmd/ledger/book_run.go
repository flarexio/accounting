package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

type bookRunOutput struct {
	Request     string                  `json:"request"`
	Turns       int                     `json:"turns"`
	Intent      bookkeeping.Intent      `json:"intent"`
	Entry       accounting.JournalEntry `json:"entry"`
	Observation llm.Observation         `json:"observation"`
	Events      []llm.CycleEvent        `json:"events"`
	Feedback    []string                `json:"feedback"`
}

func newBookRunCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "book-run",
		Usage: "Run a single bookkeeping reasoning cycle against an already-seeded ledger.",
		Description: "Connects to the ledger seeded by `ledger seed`, runs the agent.Bookkeeper\n" +
			"loop against --request, and prints a JSON report to stdout. The binary\n" +
			"reads config.yaml from --work-dir, defaulting to ~/.flarex/accounting;\n" +
			"the file must exist. The reasoning engine and model come from the\n" +
			"config.yaml llm block (engine defaults to scripted, the deterministic\n" +
			"offline engine); --engine and --model override that block when set.\n" +
			"The openai engine drives a real LLM through the same harness and\n" +
			"needs OPENAI_API_KEY.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "request",
				Usage:    "natural-language bookkeeping request",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "engine",
				Usage: "reasoning engine: scripted (offline) or openai (live); overrides config.yaml llm.engine",
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
				Usage: "maximum reasoning turns",
				Value: 3,
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "accounting work directory (holds config.yaml today, more state later); defaults to ~/.flarex/accounting",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runBook(ctx, c, stdout)
		},
	}
}

func runBook(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	request := c.String("request")
	engineKind := c.String("engine")
	amount := int64(c.Int("amount"))
	currency := c.String("currency")
	maxTurns := int(c.Int("max-turns"))
	model := c.String("model")
	workDir := c.String("work-dir")

	cfg, err := loadBookConfig(workDir)
	if err != nil {
		return err
	}

	// --engine / --model override the config.yaml llm block.
	if engineKind == "" {
		engineKind = string(cfg.LLM.Engine)
	}
	if model == "" {
		model = cfg.LLM.Model
	}

	// Validate engine config before touching the repo so misconfigured
	// invocations (unknown engine, missing OPENAI_API_KEY) fail fast.
	if err := validateBookEngineConfig(engineKind, model); err != nil {
		return err
	}

	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence, cfg.Embedding)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	period, err := firstOpenPeriod(ctx, repo)
	if err != nil {
		return err
	}
	if period.ID == "" {
		return errors.New("book-run: ledger has no open period; run `ledger seed` first")
	}

	company, ok, err := repo.Company(ctx)
	if err != nil {
		return fmt.Errorf("book-run: read company: %w", err)
	}
	if !ok {
		return errors.New("book-run: ledger has no company; run `ledger seed` first")
	}

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()

	engine, err := buildBookEngine(ctx, engineKind, company, repo, amount, currency, model)
	if err != nil {
		return err
	}
	agent := agent.Bookkeeper{
		Engine:    engine,
		Repo:      repo,
		Publisher: bus,
		MaxTurns:  maxTurns,
	}

	res, runErr := agent.Book(ctx, request)

	out := bookRunOutput{
		Request:     request,
		Turns:       res.Turns,
		Intent:      res.Intent,
		Entry:       res.Entry,
		Observation: res.Observation,
		Events:      res.Events,
		Feedback:    extractFeedback(res.Events),
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("book-run: encode output: %w", err)
	}
	return runErr
}

// validateBookEngineConfig checks engine selection without touching the repo
// so flag/config errors surface before any repository work.
func validateBookEngineConfig(engineKind, model string) error {
	switch engineKind {
	case "", "scripted":
		return nil
	case "openai":
		if model == "" {
			return errors.New("book-run: --engine openai requires --model or config.yaml llm.model")
		}
		if os.Getenv("OPENAI_API_KEY") == "" {
			return errors.New("book-run: --engine openai requires OPENAI_API_KEY")
		}
		return nil
	default:
		return fmt.Errorf("book-run: unknown --engine %q (want scripted|openai)", engineKind)
	}
}
