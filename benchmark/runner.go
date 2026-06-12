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

// EngineFactory builds a reasoning engine bound to one repo and model. The
// runner calls it once per (scenario, model), so the adapter snapshots the
// chart once and is reused across every case and iteration in that group.
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
// and does not abort the rest of the suite. Cases that share a scenario file
// share one seeded repository and one engine per model, so chart-of-accounts
// embeddings and adapter setup are paid once per scenario rather than per
// case; only the posted journals are cleared between iterations.
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
	for _, group := range groupByScenario(r.Cases) {
		out = append(out, r.runScenarioGroup(ctx, group, repeats)...)
	}
	return out, nil
}

// groupByScenario returns cases bucketed by ScenarioPath, preserving the
// first-seen order of both groups and members so result ordering stays
// deterministic and close to the caller's input order.
func groupByScenario(cases []*Case) [][]*Case {
	var groups [][]*Case
	seen := map[string]int{}
	for _, c := range cases {
		path := c.ScenarioPath()
		if idx, ok := seen[path]; ok {
			groups[idx] = append(groups[idx], c)
			continue
		}
		seen[path] = len(groups)
		groups = append(groups, []*Case{c})
	}
	return groups
}

// runScenarioGroup seeds the scenario and builds the per-model engine once
// for every case in the group, then runs each (case, model, iteration)
// against that shared state. Only journals are cleared between iterations,
// so chart-of-accounts embeddings and engine setup are paid once per group.
func (r *Runner) runScenarioGroup(ctx context.Context, group []*Case, repeats int) []RunResult {
	results := make([]RunResult, 0, len(group)*len(r.Models)*repeats)
	fanout := func(err error) {
		for _, c := range group {
			for _, m := range r.Models {
				for i := range repeats {
					results = append(results, RunResult{
						Case: c.Name, Model: m.DisplayName(), Iteration: i, Error: err.Error(),
					})
				}
			}
		}
	}

	scenario, err := accounting.LoadScenarioFile(group[0].ScenarioPath())
	if err != nil {
		fanout(err)
		return results
	}

	opts := []memory.Option{}
	if r.Embedder != nil {
		searcher, err := chromem.NewSearcher(r.Embedder)
		if err != nil {
			fanout(fmt.Errorf("chromem searcher: %w", err))
			return results
		}
		opts = append(opts, memory.WithSearcher(searcher))
	}
	repo := memory.NewAccountingRepository(opts...)
	if err := scenario.Seed(ctx, repo); err != nil {
		fanout(fmt.Errorf("seed: %w", err))
		return results
	}

	engines := make([]llm.ReasoningEngine[bookkeeping.Intent], len(r.Models))
	engineErrs := make([]error, len(r.Models))
	for i, m := range r.Models {
		engine, err := r.Engine(ctx, repo, m)
		if err != nil {
			engineErrs[i] = fmt.Errorf("engine: %w", err)
			continue
		}
		engines[i] = engine
	}

	for _, c := range group {
		maxTurns := c.Options.MaxTurns
		if maxTurns <= 0 {
			maxTurns = r.DefaultMaxTurns
		}
		for mi, m := range r.Models {
			if engineErrs[mi] != nil {
				for i := range repeats {
					results = append(results, RunResult{
						Case: c.Name, Model: m.DisplayName(), Iteration: i, Error: engineErrs[mi].Error(),
					})
				}
				continue
			}
			for i := range repeats {
				results = append(results, r.runIteration(ctx, c, m, i, repo, engines[mi], maxTurns))
			}
		}
	}
	return results
}

// runIteration clears the ledger's journal state, wires a fresh bus, and
// runs the agent once. The bus is per-iteration so its optimistic-concurrency
// sequence never lags behind a freshly-cleared repo.
func (r *Runner) runIteration(ctx context.Context, c *Case, m ModelConfig, iteration int, repo *memory.Repository, engine llm.ReasoningEngine[bookkeeping.Intent], maxTurns int) RunResult {
	rr := RunResult{Case: c.Name, Model: m.DisplayName(), Iteration: iteration}

	if err := repo.ClearJournals(ctx); err != nil {
		rr.Error = fmt.Errorf("clear journals: %w", err).Error()
		return rr
	}

	bus := inproc.NewAccountingBus()
	defer bus.Close()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectJournalPosted, &bookkeeping.ApplyJournal{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		rr.Error = fmt.Errorf("subscribe: %w", err).Error()
		return rr
	}

	a := agent.Bookkeeper{
		Engine:    engine,
		Repo:      repo,
		Publisher: bus,
		MaxTurns:  maxTurns,
	}
	res, runErr := a.Book(ctx, c.Request)
	rr.Intent = res.Intent
	if n := len(res.Entries); n > 0 {
		rr.Entry = res.Entries[n-1]
	}
	rr.Observation = res.Observation
	rr.Score = Compare(res, c.Gold)
	if runErr != nil {
		rr.Error = runErr.Error()
	}
	return rr
}
