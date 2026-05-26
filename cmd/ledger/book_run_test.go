package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flarexio/accounting/config"
)

const inProcessConfig = "persistence:\n  kind: memory\nmessaging:\n  kind: inproc\n"

// seedConfigBody points $HOME/$USERPROFILE at a tempdir and writes config.yaml at ~/.flarex/accounting/.
func seedConfigBody(t *testing.T, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".flarex", "accounting")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir default config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, config.Filename), []byte(body), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
}

// seedInProcessConfig writes a memory+inproc config so no-flag tests don't
// inherit the developer's local config.
func seedInProcessConfig(t *testing.T) {
	t.Helper()
	seedConfigBody(t, inProcessConfig)
}

// isolateHome points $HOME at an empty tempdir; no config.yaml is written.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func runBookCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	app := newApp(stdout, stderr)
	full := append([]string{"ledger", "book-run"}, args...)
	return app.Run(ctx, full)
}

// These tests cover the CLI surface only -- flag and config validation, error
// messages, work-dir resolution. End-to-end behavior (scenario-driven posting)
// lives in agent/agent_test.go where it does not need to thread through CLI
// argument parsing.

func TestRunBook_RequiresRequest(t *testing.T) {
	seedInProcessConfig(t)
	var stdout, stderr bytes.Buffer
	err := runBookCLI(context.Background(), nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --request is missing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "request") {
		t.Errorf("error should mention request, got %v", err)
	}
}

func TestRunBook_RequiresAPIKey(t *testing.T) {
	seedInProcessConfig(t)
	t.Setenv("OPENAI_API_KEY", "")
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x", "--model", "gpt-5.5"}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error without OPENAI_API_KEY")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "openai_api_key") {
		t.Errorf("error should mention OPENAI_API_KEY, got %v", err)
	}
}

func TestRunBook_RequiresModel(t *testing.T) {
	seedInProcessConfig(t)
	t.Setenv("OPENAI_API_KEY", "fake-key-for-test")
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x"}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error without --model or config.yaml llm.model")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "model") {
		t.Errorf("error should mention model, got %v", err)
	}
}

const llmModelConfig = "persistence:\n  kind: memory\nmessaging:\n  kind: inproc\n" +
	"llm:\n  model: gpt-5.5\n"

func TestRunBook_ConfigLLMModelUsed(t *testing.T) {
	// With config llm.model set and no --model flag, validation should pass
	// the model check and instead fail on the absent API key -- proving the
	// config value reached validateOpenAIConfig.
	seedConfigBody(t, llmModelConfig)
	t.Setenv("OPENAI_API_KEY", "")
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x"}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error without OPENAI_API_KEY")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "openai_api_key") {
		t.Errorf("error should mention OPENAI_API_KEY (config llm.model used), got %v", err)
	}
}

func TestRunBook_WorkDirRejectsBadKind(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, config.Filename), []byte("persistence:\n  kind: mongodb\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x", "--work-dir", dir}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error from unsupported persistence kind")
	}
	if !strings.Contains(err.Error(), "mongodb") {
		t.Errorf("error should name the bad kind, got %v", err)
	}
}

func TestRunBook_WorkDirMissingConfigYAML(t *testing.T) {
	isolateHome(t)
	emptyDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x", "--work-dir", emptyDir}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --work-dir has no config.yaml inside")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "config") {
		t.Errorf("error should mention config, got %v", err)
	}
}

func TestRunBook_DefaultDirLoaded(t *testing.T) {
	// A bad config at ~/.flarex/accounting/ -- the bad-kind error proves the fallback fired.
	home := isolateHome(t)
	cfgDir := filepath.Join(home, ".flarex", "accounting")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir default config dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, config.Filename), []byte("persistence:\n  kind: mongodb\n"), 0o600); err != nil {
		t.Fatalf("write default config: %v", err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x"}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error sourced from default config file")
	}
	if !strings.Contains(err.Error(), "mongodb") {
		t.Errorf("error should originate from default config (mention 'mongodb'), got %v", err)
	}
}

func TestRunBook_DefaultDirMissingErrors(t *testing.T) {
	// Missing default config must error, not silently fall back to in-process.
	isolateHome(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--request", "x"}
	err := runBookCLI(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when default ~/.flarex/accounting/config.yaml is missing")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "config") {
		t.Errorf("error should mention config, got %v", err)
	}
}
