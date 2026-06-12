package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/flarexio/accounting/bookkeeping"
)

func newPolicyCommand(stdout io.Writer) *cli.Command {
	workDir := &cli.StringFlag{
		Name:  "work-dir",
		Usage: "accounting work directory holding config.yaml; defaults to ~/.flarex/accounting",
	}
	return &cli.Command{
		Name:  "policy",
		Usage: "Read or write the company bookkeeping policy injected into the agent prompt.",
		Description: "The policy is operator-authored free-text (markdown convention) that the\n" +
			"agent reads verbatim when choosing accounts -- sparse, high-consequence\n" +
			"disambiguation rules, not a rule schema. It is stored as an event-sourced\n" +
			"field on the company (PolicySet), separate from `ledger seed`, so re-seeding\n" +
			"never clobbers it.",
		Commands: []*cli.Command{
			{
				Name:  "get",
				Usage: "Print the current company policy.",
				Flags: []cli.Flag{workDir},
				Action: func(ctx context.Context, c *cli.Command) error {
					return runPolicyGet(ctx, c, stdout)
				},
			},
			{
				Name:      "set",
				Usage:     "Replace the policy from a file (--file) or stdin.",
				ArgsUsage: "[--file policy.md]",
				Flags: []cli.Flag{
					workDir,
					&cli.StringFlag{Name: "file", Usage: "read the policy document from this path instead of stdin"},
				},
				Action: func(ctx context.Context, c *cli.Command) error {
					return runPolicySet(ctx, c, stdout)
				},
			},
			{
				Name:  "edit",
				Usage: "Open the current policy in $EDITOR and store it on save.",
				Flags: []cli.Flag{workDir},
				Action: func(ctx context.Context, c *cli.Command) error {
					return runPolicyEdit(ctx, c, stdout)
				},
			},
		},
	}
}

func runPolicyGet(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence, cfg.Embedding)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	company, ok, err := repo.Company(ctx)
	if err != nil {
		return fmt.Errorf("policy: load company: %w", err)
	}
	if !ok {
		return fmt.Errorf("policy: ledger has no company; run `ledger seed` first")
	}
	if policy := strings.TrimSpace(company.Policy); policy != "" {
		fmt.Fprintln(stdout, policy)
	}
	return nil
}

func runPolicySet(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	var (
		policy []byte
		err    error
	)
	if file := c.String("file"); file != "" {
		if policy, err = os.ReadFile(file); err != nil {
			return fmt.Errorf("policy: read %q: %w", file, err)
		}
	} else if policy, err = io.ReadAll(os.Stdin); err != nil {
		return fmt.Errorf("policy: read stdin: %w", err)
	}
	return publishPolicy(ctx, c, stdout, string(policy))
}

func runPolicyEdit(ctx context.Context, c *cli.Command, stdout io.Writer) error {
	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence, cfg.Embedding)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	company, ok, err := repo.Company(ctx)
	if err != nil {
		return fmt.Errorf("policy: load company: %w", err)
	}
	if !ok {
		return fmt.Errorf("policy: ledger has no company; run `ledger seed` first")
	}

	edited, err := editInEditor(company.Policy)
	if err != nil {
		return err
	}
	if strings.TrimSpace(edited) == strings.TrimSpace(company.Policy) {
		fmt.Fprintln(stdout, "policy unchanged")
		return nil
	}

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()
	return runSetPolicy(ctx, bus, stdout, edited)
}

func publishPolicy(ctx context.Context, c *cli.Command, stdout io.Writer, policy string) error {
	cfg, err := loadBookConfig(c.String("work-dir"))
	if err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	repo, repoCloser, err := buildRepository(ctx, cfg.Persistence, cfg.Embedding)
	if err != nil {
		return err
	}
	defer repoCloser.Close()

	bus, err := buildMessaging(ctx, cfg.Messaging, repo)
	if err != nil {
		return err
	}
	defer bus.Close()
	return runSetPolicy(ctx, bus, stdout, policy)
}

func runSetPolicy(ctx context.Context, bus bookkeeping.EventBus, stdout io.Writer, policy string) error {
	uc := bookkeeping.SetPolicy{Publisher: bus}
	if err := uc.Execute(ctx, policy); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if err := bus.CatchUp(ctx); err != nil {
		return fmt.Errorf("policy: %w", err)
	}
	if strings.TrimSpace(policy) == "" {
		fmt.Fprintln(stdout, "policy cleared")
	} else {
		fmt.Fprintln(stdout, "policy updated")
	}
	return nil
}

// editInEditor round-trips initial through $EDITOR (then $VISUAL, then vi).
func editInEditor(initial string) (string, error) {
	tmp, err := os.CreateTemp("", "ledger-policy-*.md")
	if err != nil {
		return "", fmt.Errorf("policy: create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(initial); err != nil {
		tmp.Close()
		return "", fmt.Errorf("policy: write temp file: %w", err)
	}
	tmp.Close()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, tmp.Name()) //nolint:gosec // operator-chosen editor
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("policy: editor %q: %w", filepath.Base(editor), err)
	}

	edited, err := os.ReadFile(tmp.Name())
	if err != nil {
		return "", fmt.Errorf("policy: read edited file: %w", err)
	}
	return string(edited), nil
}
