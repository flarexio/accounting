// Package config parses the YAML file that selects the stoa binary's outbound
// adapters at boot. Read by cmd/stoa only; domain and adapter packages must
// not import it. See config.example.yaml for the full shape.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	stoac "github.com/flarexio/stoa/config"
)

// Filename is the fixed config file name inside the accounting work directory.
const Filename = "config.yaml"

// DefaultDir is the per-user work directory: ~/.flarex/accounting.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home directory: %w", err)
	}

	return filepath.Join(home, ".flarex", "accounting"), nil
}

// PersistenceKind names the persistence backend; empty defaults to PersistenceMemory.
type PersistenceKind string

const (
	PersistenceMemory   PersistenceKind = "memory"
	PersistencePostgres PersistenceKind = "postgres"
)

// MessagingKind names the messaging backend; empty defaults to MessagingInproc.
type MessagingKind string

const (
	MessagingInproc MessagingKind = "inproc"
	MessagingNATS   MessagingKind = "nats"
)

// Config is the decoded representation of config.yaml.
type Config struct {
	Persistence Persistence `yaml:"persistence"`
	Messaging   Messaging   `yaml:"messaging"`
	LLM         stoac.LLM   `yaml:"llm"`
}

type Persistence struct {
	Kind     PersistenceKind `yaml:"kind"`
	Postgres Postgres        `yaml:"postgres"`
}

type Postgres struct {
	DSN string `yaml:"dsn"`
}

type Messaging struct {
	Kind MessagingKind `yaml:"kind"`
	NATS NATS          `yaml:"nats"`
}

// NATS connection settings for messaging/nats. Subjects are domain constants,
// not configurable here.
type NATS struct {
	URL      string `yaml:"url"`
	Stream   string `yaml:"stream"`
	Consumer string `yaml:"consumer"`
}

// Load reads path, decodes strictly (unknown fields rejected), and validates.
// Empty kinds are defaulted before return so callers can switch on Kind
// without re-checking for "".
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("config: decode %q: %w", path, err)
	}

	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: %q: %w", path, err)
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Persistence.Kind == "" {
		c.Persistence.Kind = PersistenceMemory
	}

	if c.Messaging.Kind == "" {
		c.Messaging.Kind = MessagingInproc
	}

	if c.LLM.Engine == "" {
		c.LLM.Engine = stoac.EngineScripted
	}
}

// Validate returns a joined error of every misconfiguration found.
func (c *Config) Validate() error {
	var errs []error

	switch c.Persistence.Kind {
	case PersistenceMemory:
	case PersistencePostgres:
		if c.Persistence.Postgres.DSN == "" {
			errs = append(errs, errors.New("persistence.postgres.dsn is required when persistence.kind is postgres"))
		}
	default:
		errs = append(errs, fmt.Errorf("persistence.kind %q is not supported (memory|postgres)", c.Persistence.Kind))
	}

	switch c.Messaging.Kind {
	case MessagingInproc:
	case MessagingNATS:
		if c.Messaging.NATS.URL == "" {
			errs = append(errs, errors.New("messaging.nats.url is required when messaging.kind is nats"))
		}
		if c.Messaging.NATS.Stream == "" {
			errs = append(errs, errors.New("messaging.nats.stream is required when messaging.kind is nats"))
		}
		if c.Messaging.NATS.Consumer == "" {
			errs = append(errs, errors.New("messaging.nats.consumer is required when messaging.kind is nats"))
		}
	default:
		errs = append(errs, fmt.Errorf("messaging.kind %q is not supported (inproc|nats)", c.Messaging.Kind))
	}

	switch c.LLM.Engine {
	case stoac.EngineScripted:
	case stoac.EngineOpenAI:
		// llm.model is optional at config time; --model can supply it.
	default:
		errs = append(errs, fmt.Errorf("llm.engine %q is not supported (scripted|openai)", c.LLM.Engine))
	}

	return errors.Join(errs...)
}
