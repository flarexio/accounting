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

// buildMessaging opens the EventBus and registers the projection handlers via
// a single Router. The router is the only subscription point: every supported
// subject has its handler registered before Subscribe is called.
func buildMessaging(ctx context.Context, cfg config.Messaging, repo accounting.LedgerRepository) (bookkeeping.EventBus, error) {
	bus, err := openBus(ctx, cfg)
	if err != nil {
		return nil, err
	}
	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, &bookkeeping.ApplyJournal{Repo: repo}).
		On(accounting.SubjectPeriodClosure, &bookkeeping.ApplyPeriodClosure{Repo: repo}).
		On(accounting.SubjectCompanyConfigured, &bookkeeping.ApplyCompany{Repo: repo}).
		On(accounting.SubjectPolicySet, &bookkeeping.ApplyPolicy{Repo: repo}).
		On(accounting.SubjectAccountAdded, &bookkeeping.ApplyAccount{Repo: repo}).
		On(accounting.SubjectBranchAdded, &bookkeeping.ApplyBranch{Repo: repo}).
		On(accounting.SubjectPeriodAdded, &bookkeeping.ApplyPeriod{Repo: repo}).
		On(accounting.SubjectCounterpartyAdded, &bookkeeping.ApplyCounterparty{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		_ = bus.Close()
		return nil, fmt.Errorf("book-run: subscribe: %w", err)
	}
	if err := bus.CatchUp(ctx); err != nil {
		_ = bus.Close()
		return nil, fmt.Errorf("book-run: catch up: %w", err)
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

// buildBookEngine wires the OpenAI bookkeeper reasoning engine. operatorBranchID
// is injected into the prompt; pass "" to omit the operator-branch hint. The
// recall guidance is derived from the tool set at render time, so it needs no
// flag here -- wiring the recent_entries tool (via the agent's RecentEntries
// buffer) is what turns it on.
func buildBookEngine(ctx context.Context, repo accounting.LedgerRepository, llmCfg config.LLM, operatorBranchID string) (llm.ReasoningEngine[bookkeeping.Intent], error) {
	renderer, err := agent.NewPromptRenderer(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("book-run: openai engine: %w", err)
	}
	renderer.OperatorBranchID = operatorBranchID
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
