// UI-agnostic composition helpers wiring outbound adapters from config.Config.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/config"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
	"github.com/flarexio/stoa/llm"
	"github.com/flarexio/stoa/llm/openai"

	natsmsg "github.com/flarexio/accounting/messaging/nats"
	pgrepo "github.com/flarexio/accounting/persistence/postgres"
)

// loadBookConfig reads config.yaml from dir, or ~/.flarex/stoa when empty; missing is an error.
func loadBookConfig(dir string) (*config.Config, error) {
	if dir == "" {
		def, err := config.DefaultDir()
		if err != nil {
			return nil, fmt.Errorf("book-run: %w", err)
		}
		dir = def
	}
	return config.Load(filepath.Join(dir, config.Filename))
}

// buildRepository materialises the LedgerRepository; the returned Closer is always safe to call.
func buildRepository(ctx context.Context, cfg config.Persistence) (accounting.LedgerRepository, io.Closer, error) {
	switch cfg.Kind {
	case config.PersistenceMemory:
		return memory.NewAccountingRepository(), noopCloser{}, nil
	case config.PersistencePostgres:
		repo, closer, err := pgrepo.NewAccountingRepository(ctx, cfg.Postgres.DSN)
		if err != nil {
			return nil, nil, fmt.Errorf("book-run: postgres: %w", err)
		}
		return repo, closer, nil
	default:
		return nil, nil, fmt.Errorf("book-run: unsupported persistence kind %q", cfg.Kind)
	}
}

// buildMessaging opens the EventBus and subscribes a handler that applies events to repo.
func buildMessaging(ctx context.Context, cfg config.Messaging, repo accounting.LedgerRepository) (bookkeeping.EventBus, error) {
	bus, err := openBus(ctx, cfg)
	if err != nil {
		return nil, err
	}
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		_ = bus.Close()
		return nil, fmt.Errorf("book-run: subscribe: %w", err)
	}
	return bus, nil
}

// openBus opens the EventBus chosen by cfg without subscribing yet.
func openBus(ctx context.Context, cfg config.Messaging) (bookkeeping.EventBus, error) {
	switch cfg.Kind {
	case config.MessagingInproc:
		return inproc.NewAccountingBus(), nil
	case config.MessagingNATS:
		bus, err := natsmsg.NewAccountingBus(ctx, natsmsg.Config{
			URL:      cfg.NATS.URL,
			Stream:   cfg.NATS.Stream,
			Consumer: cfg.NATS.Consumer,
		})
		if err != nil {
			return nil, fmt.Errorf("book-run: nats: %w", err)
		}
		return bus, nil
	default:
		return nil, fmt.Errorf("book-run: unsupported messaging kind %q", cfg.Kind)
	}
}

type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// buildBookEngine selects scripted (offline) or openai for the bookkeeper agent.
func buildBookEngine(ctx context.Context, kind string, scenario accounting.Scenario, repo accounting.LedgerRepository, amount int64, currency, model string) (llm.ReasoningEngine[bookkeeping.Intent], error) {
	switch kind {
	case "", "scripted":
		expense, err := firstActiveAccount(ctx, repo, accounting.AccountExpense)
		if err != nil {
			return nil, err
		}
		if expense == "" {
			return nil, errors.New("book-run: scripted engine requires an active expense account")
		}
		liability, err := firstActiveAccount(ctx, repo, accounting.AccountLiability)
		if err != nil {
			return nil, err
		}
		if liability == "" {
			return nil, errors.New("book-run: scripted engine requires an active liability account")
		}
		return newScriptedBookEngine(repo, amount, currency), nil
	case "openai":
		renderer, err := agent.NewPromptRenderer(ctx, scenario.Company, repo)
		if err != nil {
			return nil, fmt.Errorf("book-run: openai engine: %w", err)
		}
		adapter, err := openai.NewAdapter(openai.Config[bookkeeping.Intent]{
			Model:        model,
			OutputFormat: openai.OutputFormatJSONObject,
			Renderer:     renderer,
		})
		if err != nil {
			return nil, fmt.Errorf("book-run: openai engine: %w", err)
		}
		return adapter, nil
	default:
		return nil, fmt.Errorf("book-run: unknown --engine %q (want scripted|openai)", kind)
	}
}

// extractFeedback collects validation/execution-error content for the CLI report.
func extractFeedback(events []llm.CycleEvent) []string {
	var feedback []string
	for _, e := range events {
		if e.Kind == llm.EventValidationError || e.Kind == llm.EventExecutionError {
			feedback = append(feedback, e.Content)
		}
	}
	return feedback
}
