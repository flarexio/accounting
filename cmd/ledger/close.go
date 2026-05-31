package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting/bookkeeping"
)

func newCloseCommand(stdout io.Writer) *cli.Command {
	return &cli.Command{
		Name:  "close",
		Usage: "Close an accounting period by posting closing entries against the configured ledger.",
		Description: "Runs the bookkeeping.ClosePeriod use case against the configured repository.\n" +
			"For each branch with revenue or expense activity in the period it posts one\n" +
			"balanced closing entry that drains every contributing account into Retained\n" +
			"Earnings, links the closing entry to each source entry via `closes`\n" +
			"JournalRelation rows, then flips the period to closed. A second invocation\n" +
			"against an already-closed period is a no-op.\n" +
			"\n" +
			"Intended to be invoked by an external scheduler (a crontab entry or\n" +
			"equivalent). Prints a JSON report of the posted entries; exits non-zero\n" +
			"on validation or publish failure.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "period",
				Usage:    "id of the period to close (matches Period.ID, e.g. 2026-05)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "work-dir",
				Usage: "accounting work directory holding config.yaml; defaults to ~/.flarex/accounting",
			},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runClose(ctx, c, stdout)
		},
	}
}

func runClose(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	periodID := c.String("period")
	if periodID == "" {
		return errors.New("close: --period is required")
	}

	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("close: %w", err)
	}

	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence, cfg.Embedding, cfg.Rerank)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()

	uc := bookkeeping.ClosePeriod{Repo: repo, Publisher: bus}
	result, runErr := uc.Handle(ctx, bookkeeping.ClosePeriodIntent{PeriodID: periodID})

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		return fmt.Errorf("close: encode output: %w", err)
	}
	return runErr
}
