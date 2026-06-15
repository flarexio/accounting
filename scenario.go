package accounting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Scenario is the on-disk shape of an accounting fixture: company, chart of
// accounts, branches, periods, and counterparties that seed a LedgerRepository.
// It carries no journal entries -- those arrive only as JournalPosted events.
type Scenario struct {
	Name           string         `json:"name,omitempty" yaml:"name,omitempty"`
	Description    string         `json:"description,omitempty" yaml:"description,omitempty"`
	Company        Company        `json:"company" yaml:"company"`
	Accounts       []Account      `json:"accounts" yaml:"accounts"`
	Branches       []Branch       `json:"branches,omitempty" yaml:"branches,omitempty"`
	Periods        []Period       `json:"periods" yaml:"periods"`
	Counterparties []Counterparty `json:"counterparties,omitempty" yaml:"counterparties,omitempty"`
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

// Validate returns the first invariant the scenario breaks, or nil if it can seed.
func (s Scenario) Validate() error {
	if len(s.Branches) == 0 {
		return errors.New("accounting: scenario needs at least one branch; single-location companies use {id: main, name: ...}")
	}
	return nil
}

// Seed upserts the scenario's company, chart, branches, and periods into repo. An empty Company is skipped.
func (s Scenario) Seed(ctx context.Context, repo LedgerRepository) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Company.ID != "" {
		if err := repo.SetCompany(ctx, s.Company); err != nil {
			return fmt.Errorf("accounting: seed company %q: %w", s.Company.ID, err)
		}
	}
	for _, a := range s.Accounts {
		if err := repo.PutAccount(ctx, a); err != nil {
			return fmt.Errorf("accounting: seed account %q: %w", a.Code, err)
		}
	}
	for i, b := range s.Branches {
		if b.Position == 0 {
			b.Position = i + 1
		}
		if err := repo.PutBranch(ctx, b); err != nil {
			return fmt.Errorf("accounting: seed branch %q: %w", b.ID, err)
		}
	}
	for _, p := range s.Periods {
		if err := repo.PutPeriod(ctx, p); err != nil {
			return fmt.Errorf("accounting: seed period %q: %w", p.ID, err)
		}
	}
	for _, c := range s.Counterparties {
		if err := repo.PutCounterparty(ctx, c); err != nil {
			return fmt.Errorf("accounting: seed counterparty %q: %w", c.ID, err)
		}
	}
	return nil
}
