package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting"
)

func newSeedCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:      "seed",
		Usage:     "Apply a declarative YAML ledger seed to the configured repository.",
		ArgsUsage: "<seed.yaml | seed-directory>",
		Description: "Reads one YAML seed file, or every *.yaml / *.yml file in a directory,\n" +
			"and applies each to the repository chosen by config.yaml. A seed file is\n" +
			"a declarative accounting scenario -- company, chart of accounts, branches,\n" +
			"and periods. Applying it upserts that metadata, so running seed again\n" +
			"converges to the same state rather than accumulating. The binary reads\n" +
			"config.yaml from --work-dir, defaulting to ~/.flarex/stoa.\n" +
			"\n" +
			"seed owns data; schema is owned by the out-of-band golang-migrate step.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "stoa work directory holding config.yaml; defaults to ~/.flarex/stoa",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runSeed(ctx, c, stdout)
		},
	}
}

func runSeed(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	if c.NArg() == 0 {
		return errors.New("seed: seed path is required")
	}
	path := c.Args().First()

	scenarios, err := loadSeedScenarios(path)
	if err != nil {
		return err
	}
	if len(scenarios) == 0 {
		return fmt.Errorf("seed: no YAML seed files found at %q", path)
	}

	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("seed: %w", err)
	}

	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	for _, s := range scenarios {
		if err := s.Seed(ctx, repo); err != nil {
			return fmt.Errorf("seed: %w", err)
		}
		fmt.Fprintf(stdout, "seeded %s (%s): %d account(s), %d branch(es), %d period(s)\n",
			s.Company.ID, s.Company.Name, len(s.Accounts), len(s.Branches), len(s.Periods))
	}
	return nil
}

// loadSeedScenarios reads path as one file or every *.yaml / *.yml in a directory (sorted).
func loadSeedScenarios(path string) ([]accounting.Scenario, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}

	if !info.IsDir() {
		s, err := accounting.LoadScenarioYAML(path)
		if err != nil {
			return nil, err
		}
		return []accounting.Scenario{s}, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("seed: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext == ".yaml" || ext == ".yml" {
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	sort.Strings(files)

	scenarios := make([]accounting.Scenario, 0, len(files))
	for _, f := range files {
		s, err := accounting.LoadScenarioYAML(f)
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}
