// UI-agnostic composition helpers wiring outbound adapters from config.Config.

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/config"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
	"github.com/flarexio/stoa/llm"
	"github.com/flarexio/stoa/llm/anthropic"
	"github.com/flarexio/stoa/llm/openai"

	embedopenai "github.com/flarexio/accounting/embedding/openai"
	natsmsg "github.com/flarexio/accounting/messaging/nats"
	pgrepo "github.com/flarexio/accounting/persistence/postgres"
)

// loadBookConfig reads config.yaml from dir, or ~/.flarex/accounting when empty; missing is an error.
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
func buildRepository(ctx context.Context, persist config.Persistence, embed config.Embedding) (accounting.LedgerRepository, io.Closer, error) {
	switch persist.Kind {
	case config.PersistenceMemory:
		return memory.NewAccountingRepository(), noopCloser{}, nil
	case config.PersistencePostgres:
		embedder := embedopenai.NewEmbedder(embed.Model, embed.Dimensions)
		repo, closer, err := pgrepo.NewAccountingRepository(ctx, persist.Postgres.DSN, embedder)
		if err != nil {
			return nil, nil, fmt.Errorf("book-run: postgres: %w", err)
		}
		return repo, closer, nil
	default:
		return nil, nil, fmt.Errorf("book-run: unsupported persistence kind %q", persist.Kind)
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

// firstOpenPeriod returns the lowest-id open period, or a zero Period when none is open.
func firstOpenPeriod(ctx context.Context, repo accounting.LedgerRepository) (accounting.Period, error) {
	periods, err := repo.Periods(ctx)
	if err != nil {
		return accounting.Period{}, err
	}
	sort.Slice(periods, func(i, j int) bool { return periods[i].ID < periods[j].ID })
	for _, p := range periods {
		if p.Status == accounting.PeriodOpen {
			return p, nil
		}
	}
	return accounting.Period{}, nil
}

// buildBookEngine wires the bookkeeper reasoning engine selected by llmCfg.Kind.
func buildBookEngine(ctx context.Context, repo accounting.LedgerRepository, llmCfg config.LLM) (llm.ReasoningEngine[bookkeeping.Intent], error) {
	renderer, err := agent.NewPromptRenderer(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("book-run: %s engine: %w", llmCfg.Kind, err)
	}
	switch llmCfg.Kind {
	case "", config.LLMOpenAI:
		adapter, err := openai.NewAdapter(openai.Config[bookkeeping.Intent]{
			APIKey:                       llmCfg.APIKey,
			BaseURL:                      llmCfg.BaseURL,
			Model:                        llmCfg.Model,
			IntentSchema:                 bookkeeping.IntentSchema(),
			DisableStrictSchemaWithTools: llmCfg.DisableStrictSchemaWithTools,
			Renderer:                     renderer,
		})
		if err != nil {
			return nil, fmt.Errorf("book-run: openai engine: %w", err)
		}
		return adapter, nil
	case config.LLMAnthropic:
		adapter, err := anthropic.NewAdapter(anthropic.Config[bookkeeping.Intent]{
			APIKey:       llmCfg.APIKey,
			BaseURL:      llmCfg.BaseURL,
			Model:        llmCfg.Model,
			MaxTokens:    llmCfg.MaxTokens,
			IntentSchema: bookkeeping.IntentSchema(),
			Renderer:     renderer,
		})
		if err != nil {
			return nil, fmt.Errorf("book-run: anthropic engine: %w", err)
		}
		return adapter, nil
	default:
		return nil, fmt.Errorf("book-run: unsupported llm kind %q", llmCfg.Kind)
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
