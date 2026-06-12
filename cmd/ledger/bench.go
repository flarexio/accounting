package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/benchmark"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/config"
	embedopenai "github.com/flarexio/accounting/embedding/openai"
	"github.com/flarexio/stoa/llm"
)

func newBenchCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "bench",
		Usage: "Run the bookkeeping agent over a suite of cases against one or more models, scored against gold answers.",
		Description: "For each case in --suite and each model in --model the bench command\n" +
			"builds a fresh in-memory ledger (with a chromem-go account searcher\n" +
			"when an embedding API key is available), runs the agent once per\n" +
			"--repeats, and grades the proposed intent against the case's gold\n" +
			"answer. The reasoning engine reads config.yaml llm.api_key and\n" +
			"llm.base_url; --model overrides the model name per run.",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "suite",
				Usage:    "case files or glob(s); repeat the flag or use commas",
				Required: true,
			},
			&cli.StringSliceFlag{
				Name:     "model",
				Usage:    "openai model name; repeat the flag or use commas to compare several",
				Required: true,
			},
			&cli.IntFlag{
				Name:  "max-turns",
				Usage: "default reasoning turn ceiling for cases that do not set their own",
				Value: 8,
			},
			&cli.IntFlag{
				Name:  "repeats",
				Usage: "iterations per (case, model) pair",
				Value: 1,
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write the JSON report to this path; stdout when empty",
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "accounting work directory holding config.yaml; defaults to ~/.flarex/accounting",
			},
			&cli.BoolFlag{
				Name:  "no-vector-search",
				Usage: "skip the chromem-go searcher even when an OpenAI key is available",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runBench(ctx, c, stdout)
		},
	}
}

func runBench(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	suiteArgs := c.StringSlice("suite")
	modelArgs := c.StringSlice("model")
	repeats := int(c.Int("repeats"))
	maxTurns := int(c.Int("max-turns"))
	outPath := c.String("out")
	workDir := c.String("work-dir")
	skipVector := c.Bool("no-vector-search")

	cases, err := loadBenchCases(suiteArgs)
	if err != nil {
		return err
	}
	if len(cases) == 0 {
		return errors.New("bench: --suite matched no files")
	}

	models, err := parseBenchModels(modelArgs)
	if err != nil {
		return err
	}

	cfg, err := loadBookConfig(workDir)
	if err != nil {
		return fmt.Errorf("bench: %w", err)
	}
	llmCfg := cfg.LLM
	if llmCfg.APIKey == "" && os.Getenv("OPENAI_API_KEY") == "" {
		return errors.New("bench: openai engine requires config.yaml llm.api_key or OPENAI_API_KEY")
	}

	for i := range models {
		if models[i].APIKey == "" {
			models[i].APIKey = llmCfg.APIKey
		}
		if models[i].BaseURL == "" {
			models[i].BaseURL = llmCfg.BaseURL
		}
		models[i].DisableStrictSchemaWithTools = llmCfg.DisableStrictSchemaWithTools
	}

	var embedder accounting.Embedder
	if !skipVector {
		embedder = embedopenai.NewEmbedder(cfg.Embedding.Model, cfg.Embedding.Dimensions)
	}

	runner := benchmark.Runner{
		Cases:           cases,
		Models:          models,
		Engine:          benchEngineFactory(),
		Embedder:        embedder,
		DefaultMaxTurns: maxTurns,
		Repeats:         repeats,
	}
	results, err := runner.Run(ctx)
	if err != nil {
		return fmt.Errorf("bench: %w", err)
	}
	report := benchmark.BuildReport(results, len(cases), len(models), runner.Repeats)

	w, closer, err := openBenchOutput(outPath, stdout)
	if err != nil {
		return err
	}
	defer closer()
	if err := report.WriteJSON(w); err != nil {
		return fmt.Errorf("bench: %w", err)
	}
	return nil
}

func benchEngineFactory() benchmark.EngineFactory {
	return func(ctx context.Context, repo accounting.LedgerRepository, m benchmark.ModelConfig) (llm.ReasoningEngine[bookkeeping.Intent], error) {
		return buildBookEngine(ctx, repo, config.LLM{
			Model:                        m.Model,
			APIKey:                       m.APIKey,
			BaseURL:                      m.BaseURL,
			DisableStrictSchemaWithTools: m.DisableStrictSchemaWithTools,
		}, "", false)
	}
}

// loadBenchCases expands every arg as a glob (a literal path also matches itself), dedupes, sorts, and decodes each.
func loadBenchCases(args []string) ([]*benchmark.Case, error) {
	seen := map[string]struct{}{}
	var paths []string
	for _, arg := range args {
		matches, err := filepath.Glob(arg)
		if err != nil {
			return nil, fmt.Errorf("bench: glob %q: %w", arg, err)
		}
		if matches == nil {
			if _, err := os.Stat(arg); err == nil {
				matches = []string{arg}
			}
		}
		for _, m := range matches {
			if _, ok := seen[m]; ok {
				continue
			}
			seen[m] = struct{}{}
			paths = append(paths, m)
		}
	}
	sort.Strings(paths)

	cases := make([]*benchmark.Case, 0, len(paths))
	for _, p := range paths {
		c, err := benchmark.LoadCaseFile(p)
		if err != nil {
			return nil, fmt.Errorf("bench: %w", err)
		}
		cases = append(cases, c)
	}
	return cases, nil
}

// parseBenchModels turns repeated/comma-separated --model values into ModelConfigs.
func parseBenchModels(args []string) ([]benchmark.ModelConfig, error) {
	seen := map[string]struct{}{}
	var models []benchmark.ModelConfig
	for _, arg := range args {
		for name := range strings.SplitSeq(arg, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			models = append(models, benchmark.ModelConfig{Model: name})
		}
	}
	if len(models) == 0 {
		return nil, errors.New("bench: --model is required")
	}
	return models, nil
}

func openBenchOutput(path string, stdout io.Writer) (io.Writer, func(), error) {
	if path == "" {
		return stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("bench: create %q: %w", path, err)
	}
	return f, func() { _ = f.Close() }, nil
}
