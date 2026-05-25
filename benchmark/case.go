// Package benchmark runs the bookkeeping agent against fixed scenarios with
// known answers, so model choices can be compared on the same task. A Case
// pairs a scenario (chart + periods + branches), a natural-language request,
// and a Gold answer; the Runner executes the agent under one or more model
// configurations, and the Scorer compares each Result against the Gold.
package benchmark

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Case is one benchmark scenario: where the ledger comes from, what the user
// asks for, and what the right answer looks like.
type Case struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description,omitempty"`
	Scenario    string      `yaml:"scenario"`
	Request     string      `yaml:"request"`
	Gold        Gold        `yaml:"gold"`
	Options     CaseOptions `yaml:"options,omitempty"`

	Path string `yaml:"-"`
}

// CaseOptions overrides the runner's defaults for this case.
type CaseOptions struct {
	MaxTurns int `yaml:"max_turns,omitempty"`
}

// LoadCaseFile reads and decodes a case file from disk. YAML and JSON are
// both accepted via the file extension.
func LoadCaseFile(path string) (*Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("benchmark: open case %q: %w", path, err)
	}
	defer f.Close()
	c, err := DecodeCase(f)
	if err != nil {
		return nil, err
	}
	c.Path = path
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("benchmark: case %q: %w", path, err)
	}
	return c, nil
}

// DecodeCase reads a case document from r. Unknown fields are rejected.
func DecodeCase(r io.Reader) (*Case, error) {
	var c Case
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("benchmark: decode case: %w", err)
	}
	return &c, nil
}

// ScenarioPath returns the case's scenario path resolved against the case file's directory.
func (c *Case) ScenarioPath() string {
	if filepath.IsAbs(c.Scenario) {
		return c.Scenario
	}
	if c.Path == "" {
		return c.Scenario
	}
	return filepath.Join(filepath.Dir(c.Path), c.Scenario)
}

func (c *Case) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(c.Scenario) == "" {
		return fmt.Errorf("scenario is required")
	}
	if strings.TrimSpace(c.Request) == "" {
		return fmt.Errorf("request is required")
	}
	return c.Gold.validate()
}
