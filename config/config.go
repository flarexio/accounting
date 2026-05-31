// Package config parses the YAML file that selects the ledger CLI's
// outbound adapters at boot. Domain and adapter packages must not import it.
// See config.example.yaml for the full shape.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
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

// LLM holds the reasoning engine configuration.
type LLM struct {
	Model   string `yaml:"model"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	// DisableStrictSchemaWithTools drops strict json_schema response_format on
	// turns that pass tools. Set true for llama.cpp-style servers whose
	// grammar engine enforces response_format at the sampler.
	DisableStrictSchemaWithTools bool `yaml:"disable_strict_schema_with_tools"`
}

// Embedding holds the embedding model used by the postgres adapter to populate
// the accounts.embedding vector column. Dimensions must match the schema
// (migration 0002 sets it to 1536); changing this requires a new migration.
type Embedding struct {
	Model      string `yaml:"model"`
	Dimensions int    `yaml:"dimensions"`
}

// Rerank holds the optional account-search reranker. An empty Model disables
// reranking, leaving FindAccounts on the hybrid (dense + lexical) ordering.
type Rerank struct {
	Model string `yaml:"model"`
}

// Config is the decoded representation of config.yaml.
type Config struct {
	Persistence Persistence `yaml:"persistence"`
	Messaging   Messaging   `yaml:"messaging"`
	LLM         LLM         `yaml:"llm"`
	Embedding   Embedding   `yaml:"embedding"`
	Rerank      Rerank      `yaml:"rerank"`
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

	if c.Embedding.Model == "" {
		c.Embedding.Model = "text-embedding-3-small"
	}
	if c.Embedding.Dimensions == 0 {
		c.Embedding.Dimensions = 1536
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

	if c.Embedding.Dimensions <= 0 {
		errs = append(errs, fmt.Errorf("embedding.dimensions must be positive (got %d)", c.Embedding.Dimensions))
	}

	return errors.Join(errs...)
}
