package accounting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scenario is the on-disk shape of an accounting fixture: company, chart of
// accounts, branches, and periods that seed a LedgerRepository. It carries no
// journal entries -- those arrive only as JournalPosted events.
type Scenario struct {
	Name        string    `json:"name,omitempty" yaml:"name,omitempty"`
	Description string    `json:"description,omitempty" yaml:"description,omitempty"`
	Company     Company   `json:"company" yaml:"company"`
	Accounts    []Account `json:"accounts" yaml:"accounts"`
	Branches    []Branch  `json:"branches,omitempty" yaml:"branches,omitempty"`
	Periods     []Period  `json:"periods" yaml:"periods"`
}

// LoadScenarioFile reads and decodes a scenario file from disk. The format is
// chosen by extension: .yaml/.yml use the YAML decoder, anything else (or no
// extension) uses the JSON decoder.
func LoadScenarioFile(path string) (Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("accounting: open scenario %q: %w", path, err)
	}
	defer f.Close()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return DecodeScenarioYAML(f)
	default:
		return DecodeScenario(f)
	}
}

// DecodeScenario reads a scenario JSON document from r.
func DecodeScenario(r io.Reader) (Scenario, error) {
	var s Scenario
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("accounting: decode scenario: %w", err)
	}
	return s, nil
}

// LoadScenarioYAML reads and decodes a YAML seed file (used by `ledger seed`).
func LoadScenarioYAML(path string) (Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return Scenario{}, fmt.Errorf("accounting: open seed %q: %w", path, err)
	}
	defer f.Close()
	return DecodeScenarioYAML(f)
}

// DecodeScenarioYAML reads a YAML seed document from r. Unknown fields are rejected.
func DecodeScenarioYAML(r io.Reader) (Scenario, error) {
	var s Scenario
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("accounting: decode seed: %w", err)
	}
	return s, nil
}

// Seed upserts the scenario's chart, branches, and periods into repo through
// its Put* methods; it does not check for or merge with existing state.
func (s Scenario) Seed(ctx context.Context, repo LedgerRepository) error {
	for _, a := range s.Accounts {
		if err := repo.PutAccount(ctx, a); err != nil {
			return fmt.Errorf("accounting: seed account %q: %w", a.Code, err)
		}
	}
	for _, b := range s.Branches {
		if err := repo.PutBranch(ctx, b); err != nil {
			return fmt.Errorf("accounting: seed branch %q: %w", b.ID, err)
		}
	}
	for _, p := range s.Periods {
		if err := repo.PutPeriod(ctx, p); err != nil {
			return fmt.Errorf("accounting: seed period %q: %w", p.ID, err)
		}
	}
	return nil
}
