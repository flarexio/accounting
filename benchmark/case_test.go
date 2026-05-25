package benchmark_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flarexio/accounting/benchmark"
	"github.com/flarexio/accounting/bookkeeping"
)

func TestLoadCaseFile_AWSBill(t *testing.T) {
	c, err := benchmark.LoadCaseFile("../seed/bench/aws_bill_basic_payment.case.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Name != "aws_bill_basic_payment" {
		t.Fatalf("name: %q", c.Name)
	}
	if c.Gold.Kind != bookkeeping.IntentPostJournal {
		t.Fatalf("gold kind: %q", c.Gold.Kind)
	}
	if c.Gold.Post == nil || len(c.Gold.Post.Lines) != 2 {
		t.Fatalf("gold post payload: %+v", c.Gold.Post)
	}
	want := mustAbs(t, "../seed/aws_bill.json")
	if got := mustAbs(t, c.ScenarioPath()); got != want {
		t.Fatalf("scenario path: want %q, got %q", want, got)
	}
}

func TestLoadCaseFile_RejectsMissingFields(t *testing.T) {
	tmp := writeTempCase(t, `name: bad
scenario: ../missing.json
request: ""
gold:
  kind: post_journal
`)
	if _, err := benchmark.LoadCaseFile(tmp); err == nil || !strings.Contains(err.Error(), "request") {
		t.Fatalf("expected request validation error, got %v", err)
	}
}

func TestLoadCaseFile_RejectsUnbalancedGoldKind(t *testing.T) {
	tmp := writeTempCase(t, `name: bad
scenario: x.json
request: hi
gold:
  kind: post_journal
`)
	if _, err := benchmark.LoadCaseFile(tmp); err == nil || !strings.Contains(err.Error(), "post_journal is required") {
		t.Fatalf("expected missing payload error, got %v", err)
	}
}

func writeTempCase(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "case.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %q: %v", path, err)
	}
	return abs
}
