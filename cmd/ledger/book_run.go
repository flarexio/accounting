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
	"github.com/flarexio/accounting/config"
	"github.com/flarexio/stoa/llm"
)

type bookRunOutput struct {
	Request     string                    `json:"request"`
	Turns       int                       `json:"turns"`
	Intent      bookkeeping.Intent        `json:"intent"`
	Entries     []accounting.JournalEntry `json:"entries"`
	Observation llm.Observation           `json:"observation"`
	Events      []llm.CycleEvent          `json:"events"`
	Feedback    []string                  `json:"feedback"`
}

func newBookRunCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "book-run",
		Usage: "Run a single bookkeeping reasoning cycle against an already-seeded ledger.",
		Description: "Connects to the ledger seeded by `ledger seed`, runs the agent.Bookkeeper\n" +
			"loop against --request, and prints a JSON report to stdout. The binary\n" +
			"reads config.yaml from --work-dir, defaulting to ~/.flarex/accounting;\n" +
			"the file must exist. The reasoning engine is OpenAI-compatible; set\n" +
			"llm.api_key in config or export OPENAI_API_KEY. --model overrides\n" +
			"config.yaml llm.model.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "request",
				Usage:    "natural-language bookkeeping request",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "openai model name; overrides config.yaml llm.model",
			},
			&cli.IntFlag{
				Name:  "max-turns",
				Usage: "maximum reasoning turns",
				Value: 8,
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
	maxTurns := int(c.Int("max-turns"))
	model := c.String("model")
	workDir := c.String("work-dir")

	cfg, err := loadBookConfig(workDir)
	if err != nil {
		return err
	}

	llmCfg := cfg.LLM
	if model != "" {
		llmCfg.Model = model
	}

	if err := validateOpenAIConfig(llmCfg); err != nil {
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

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()

	engine, _, err := buildBookEngine(ctx, repo, llmCfg, "")
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
		Entries:     res.Entries,
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

// validateOpenAIConfig ensures model and an API key source are present.
func validateOpenAIConfig(llmCfg config.LLM) error {
	if llmCfg.Model == "" {
		return errors.New("book-run: openai engine requires --model or config.yaml llm.model")
	}
	if llmCfg.APIKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		return errors.New("book-run: openai engine requires config.yaml llm.api_key or OPENAI_API_KEY")
	}
	return nil
}
