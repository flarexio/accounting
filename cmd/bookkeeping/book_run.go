package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

type bookRunOutput struct {
	Scenario    string                  `json:"scenario,omitempty"`
	Description string                  `json:"description,omitempty"`
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
		Name:      "book-run",
		Usage:     "Run a bookkeeping reasoning loop against an accounting scenario JSON file.",
		ArgsUsage: "<scenario.json>",
		Description: "Loads an accounting scenario JSON file, seeds the configured repository,\n" +
			"runs the agent.Bookkeeper loop, and prints a JSON report to stdout. The\n" +
			"binary reads config.yaml from --work-dir, defaulting to ~/.flarex/stoa;\n" +
			"the file must exist (no implicit in-process fallback). The reasoning\n" +
			"engine and model come from the config.yaml llm block (engine defaults\n" +
			"to scripted, the deterministic offline engine); --engine and --model\n" +
			"override that block when set. The openai engine drives a real LLM\n" +
			"through the same harness and needs OPENAI_API_KEY.",
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
				Usage: "stoa work directory (holds config.yaml today, more state later); defaults to ~/.flarex/stoa",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runBook(ctx, c, stdout)
		},
	}
}

func runBook(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	if c.NArg() == 0 {
		return errors.New("book-run: scenario path is required")
	}
	path := c.Args().First()
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

	scenario, err := accounting.LoadScenarioFile(path)
	if err != nil {
		return err
	}

	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	if err := scenario.Seed(ctx, repo); err != nil {
		return err
	}

	period, err := firstOpenPeriod(ctx, repo)
	if err != nil {
		return err
	}
	if period.ID == "" {
		return errors.New("book-run: scenario has no open period")
	}

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()

	engine, err := buildBookEngine(ctx, engineKind, scenario, repo, amount, currency, model)
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
		Scenario:    scenario.Name,
		Description: scenario.Description,
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
