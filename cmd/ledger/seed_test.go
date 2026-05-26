package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

func runSeedCLI(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	app := newApp(stdout, stderr)
	full := append([]string{"ledger", "seed"}, args...)
	return app.Run(ctx, full)
}

const sampleSeed = `name: t
company:
  id: acme
  name: Acme Co.
  timezone: UTC
accounts:
  - { code: "1000", name: Cash, type: asset, active: true }
branches:
  - { id: main, name: Main }
periods:
  - { id: "2026-05", start: "2026-05-01", end: "2026-05-31", status: open }
`

func writeSeedFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}
	return p
}

func TestRunSeed_SingleFile(t *testing.T) {
	seedInProcessConfig(t)
	path := writeSeedFile(t, t.TempDir(), "acme.yaml", sampleSeed)
	var stdout, stderr bytes.Buffer
	if err := runSeedCLI(context.Background(), []string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("runSeedCLI: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "acme") {
		t.Errorf("summary should name the seeded company, got %q", stdout.String())
	}
}

func TestRunSeed_Directory(t *testing.T) {
	seedInProcessConfig(t)
	dir := t.TempDir()
	writeSeedFile(t, dir, "acme.yaml", sampleSeed)
	writeSeedFile(t, dir, "notes.txt", "not a seed file")
	var stdout, stderr bytes.Buffer
	if err := runSeedCLI(context.Background(), []string{dir}, &stdout, &stderr); err != nil {
		t.Fatalf("runSeedCLI (dir): %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "acme") {
		t.Errorf("summary should name the seeded company, got %q", stdout.String())
	}
}

func TestRunSeed_RequiresPath(t *testing.T) {
	seedInProcessConfig(t)
	var stdout, stderr bytes.Buffer
	if err := runSeedCLI(context.Background(), nil, &stdout, &stderr); err == nil {
		t.Fatal("expected an error when the seed path is missing")
	}
}

// TestRunSeed_BundledSeedFile applies the real seed/taiwan_ledger.yaml as a smoke test.
func TestRunSeed_BundledSeedFile(t *testing.T) {
	seedInProcessConfig(t)
	path := filepath.Join("..", "..", "seed", "taiwan_ledger.yaml")
	var stdout, stderr bytes.Buffer
	if err := runSeedCLI(context.Background(), []string{path}, &stdout, &stderr); err != nil {
		t.Fatalf("seeding the bundled taiwan_ledger.yaml failed: %v\nstderr: %s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "dongsheng") {
		t.Errorf("summary should name the dongsheng company, got %q", stdout.String())
	}
}

func TestSeed_IdempotentUpsert(t *testing.T) {
	scn, err := accounting.DecodeScenarioYAML(strings.NewReader(sampleSeed))
	if err != nil {
		t.Fatalf("decode sample seed: %v", err)
	}
	repo := memory.NewAccountingRepository()
	ctx := context.Background()
	for i := range 2 {
		if err := scn.Seed(ctx, repo); err != nil {
			t.Fatalf("Seed pass %d: %v", i, err)
		}
	}
	accounts, err := repo.Accounts(ctx)
	if err != nil {
		t.Fatalf("Accounts: %v", err)
	}
	if len(accounts) != 1 {
		t.Errorf("re-seeding should converge to 1 account, got %d", len(accounts))
	}
}
