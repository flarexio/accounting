package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flarexio/accounting/config"

	"github.com/flarexio/stoa/llm"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

func TestLoad_EmptyFileDefaultsToInProcess(t *testing.T) {
	path := writeConfig(t, "")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Persistence.Kind != config.PersistenceMemory {
		t.Errorf("persistence default: want memory, got %q", cfg.Persistence.Kind)
	}

	if cfg.Messaging.Kind != config.MessagingInproc {
		t.Errorf("messaging default: want inproc, got %q", cfg.Messaging.Kind)
	}

	if cfg.LLM.Engine != llm.EngineScripted {
		t.Errorf("llm engine default: want scripted, got %q", cfg.LLM.Engine)
	}
}

func TestLoad_LLMBlockParsed(t *testing.T) {
	path := writeConfig(t, "llm:\n  engine: openai\n  model: gpt-5.4-mini\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.Engine != llm.EngineOpenAI {
		t.Errorf("llm engine: want openai, got %q", cfg.LLM.Engine)
	}

	if cfg.LLM.Model != "gpt-5.4-mini" {
		t.Errorf("llm model: want gpt-5.4-mini, got %q", cfg.LLM.Model)
	}
}

func TestLoad_UnknownEngineRejected(t *testing.T) {
	path := writeConfig(t, "llm:\n  engine: anthropic\n")
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported llm engine")
	}

	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("error should name the bad engine, got %v", err)
	}
}

func TestLoad_OpenAIEngineDoesNotRequireModel(t *testing.T) {
	// --model may supply it later; config validation must not reject openai without one.
	path := writeConfig(t, "llm:\n  engine: openai\n")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.Engine != llm.EngineOpenAI || cfg.LLM.Model != "" {
		t.Errorf("unexpected llm block: %+v", cfg.LLM)
	}
}

func TestLoad_PostgresRequiresDSN(t *testing.T) {
	path := writeConfig(t, "persistence:\n  kind: postgres\n")
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error when postgres.dsn is missing")
	}

	if !strings.Contains(err.Error(), "persistence.postgres.dsn") {
		t.Errorf("error should name the missing field, got %v", err)
	}
}

func TestLoad_NATSRequiresURLStreamConsumer(t *testing.T) {
	path := writeConfig(t, "messaging:\n  kind: nats\n")

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error when nats block is empty")
	}

	for _, want := range []string{"messaging.nats.url", "messaging.nats.stream", "messaging.nats.consumer"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got %v", want, err)
		}
	}
}

func TestLoad_NATSRejectsSubjectField(t *testing.T) {
	// Subject and stream_subject are domain constants, not config; the
	// strict decoder must reject them so a stale config fails loudly.
	for _, body := range []string{
		"messaging:\n  kind: nats\n  nats:\n    subject: accounting.journal\n",
		"messaging:\n  kind: nats\n  nats:\n    stream_subject: accounting.>\n",
	} {
		path := writeConfig(t, body)
		if _, err := config.Load(path); err == nil {
			t.Errorf("expected error for legacy subject field in %q", body)
		}
	}
}

func TestLoad_UnknownKindRejected(t *testing.T) {
	path := writeConfig(t, "persistence:\n  kind: mongodb\n")

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported persistence kind")
	}

	if !strings.Contains(err.Error(), "mongodb") {
		t.Errorf("error should name the bad kind, got %v", err)
	}
}

func TestLoad_UnknownFieldRejected(t *testing.T) {
	path := writeConfig(t, "persistence:\n  kind: memory\n  redis:\n    addr: localhost:6379\n")

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for unknown top-level field under persistence")
	}

	if !strings.Contains(err.Error(), "redis") {
		t.Errorf("error should reject unknown field 'redis', got %v", err)
	}
}

func TestLoad_FullPostgresAndNATS(t *testing.T) {
	body := `persistence:
  kind: postgres
  postgres:
    dsn: postgres://stoa:stoa@localhost:5432/accounting?sslmode=disable
messaging:
  kind: nats
  nats:
    url: nats://localhost:4222
    stream: ACCOUNTING
    consumer: accounting-book-run
`
	path := writeConfig(t, body)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Persistence.Postgres.DSN == "" {
		t.Error("DSN should be parsed")
	}

	if cfg.Messaging.NATS.Stream != "ACCOUNTING" {
		t.Errorf("stream: got %q", cfg.Messaging.NATS.Stream)
	}

	if cfg.Messaging.NATS.Consumer != "accounting-book-run" {
		t.Errorf("consumer: got %q", cfg.Messaging.NATS.Consumer)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestDefaultDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got, err := config.DefaultDir()
	if err != nil {
		t.Fatalf("DefaultDir: %v", err)
	}

	want := filepath.Join(home, ".flarex", "accounting")
	if got != want {
		t.Errorf("DefaultDir: got %q, want %q", got, want)
	}

	if config.Filename != "config.yaml" {
		t.Errorf("Filename: got %q, want %q", config.Filename, "config.yaml")
	}
}
