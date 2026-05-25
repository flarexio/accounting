package benchmark

import (
	"context"
	"errors"
	"fmt"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/agent"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/chromem"
	"github.com/flarexio/accounting/persistence/memory"
	"github.com/flarexio/stoa/llm"
)

// ModelConfig identifies one reasoning engine configuration under test.
// Name labels it in the report; if empty, Model is used.
type ModelConfig struct {
	Name                         string
	Model                        string
	APIKey                       string
	BaseURL                      string
	DisableStrictSchemaWithTools bool
}

// DisplayName is Name when set, otherwise Model.
func (m ModelConfig) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	return m.Model
}

// EngineFactory builds a reasoning engine for one (repo, model) pair. The
// runner calls it per iteration so each run gets a fresh adapter bound to
// the freshly-seeded repository.
type EngineFactory func(ctx context.Context, repo accounting.LedgerRepository, m ModelConfig) (llm.ReasoningEngine[bookkeeping.Intent], error)

// Runner executes Cases against Models for Repeats iterations each.
type Runner struct {
	Cases           []*Case
	Models          []ModelConfig
	Engine          EngineFactory
	Embedder        accounting.Embedder
	DefaultMaxTurns int
	Repeats         int
}

// RunResult is one (case, model, iteration) outcome.
type RunResult struct {
	Case      string                  `json:"case"`
	Model     string                  `json:"model"`
	Iteration int                     `json:"iteration"`
	Intent    bookkeeping.Intent      `json:"intent"`
	Entry     accounting.JournalEntry `json:"entry,omitzero"`
	Observation llm.Observation       `json:"observation"`
	Score     Score                   `json:"score"`
	Error     string                  `json:"error,omitempty"`
}

// Run executes every (case, model, iteration) sequentially and returns one
// RunResult per cell. A failure in one run is captured in RunResult.Error
// and does not abort the rest of the suite.
func (r *Runner) Run(ctx context.Context) ([]RunResult, error) {
	if r.Engine == nil {
		return nil, errors.New("benchmark: Runner.Engine is required")
	}
	if len(r.Cases) == 0 {
		return nil, errors.New("benchmark: Runner.Cases is empty")
	}
	if len(r.Models) == 0 {
		return nil, errors.New("benchmark: Runner.Models is empty")
	}
	repeats := r.Repeats
	if repeats <= 0 {
		repeats = 1
	}

	var out []RunResult
	for _, c := range r.Cases {
		for _, m := range r.Models {
			for i := 0; i < repeats; i++ {
				out = append(out, r.runOne(ctx, c, m, i))
			}
		}
	}
	return out, nil
}

func (r *Runner) runOne(ctx context.Context, c *Case, m ModelConfig, iteration int) RunResult {
	rr := RunResult{Case: c.Name, Model: m.DisplayName(), Iteration: iteration}

	scenario, err := accounting.LoadScenarioFile(c.ScenarioPath())
	if err != nil {
		rr.Error = err.Error()
		return rr
	}

	opts := []memory.Option{}
	if r.Embedder != nil {
		searcher, err := chromem.NewSearcher(r.Embedder)
		if err != nil {
			rr.Error = fmt.Errorf("chromem searcher: %w", err).Error()
			return rr
		}
		opts = append(opts, memory.WithSearcher(searcher))
	}
	repo := memory.NewAccountingRepository(opts...)
	if err := scenario.Seed(ctx, repo); err != nil {
		rr.Error = fmt.Errorf("seed: %w", err).Error()
		return rr
	}

	bus := inproc.NewAccountingBus()
	defer bus.Close()
	apply := bookkeeping.EventHandlerFunc(func(ctx context.Context, evt accounting.JournalPosted) error {
		return repo.Apply(ctx, evt)
	})
	if err := bus.Subscribe(apply); err != nil {
		rr.Error = fmt.Errorf("subscribe: %w", err).Error()
		return rr
	}

	engine, err := r.Engine(ctx, repo, m)
	if err != nil {
		rr.Error = fmt.Errorf("engine: %w", err).Error()
		return rr
	}

	maxTurns := c.Options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = r.DefaultMaxTurns
	}
	a := agent.Bookkeeper{
		Engine:    engine,
		Repo:      repo,
		Publisher: bus,
		MaxTurns:  maxTurns,
	}
	res, runErr := a.Book(ctx, c.Request)
	rr.Intent = res.Intent
	rr.Entry = res.Entry
	rr.Observation = res.Observation
	rr.Score = Compare(res, c.Gold)
	if runErr != nil {
		rr.Error = runErr.Error()
	}
	return rr
}
