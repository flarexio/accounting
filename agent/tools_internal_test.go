package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

func TestFormatAccountMatches(t *testing.T) {
	active := accounting.Account{Code: "6117", Name: "雜費", Type: accounting.AccountExpense, Active: true}
	inactive := accounting.Account{Code: "6199", Name: "其他費用（舊制，停用）", Type: accounting.AccountExpense, Active: false}

	t.Run("active and inactive split into separate sections", func(t *testing.T) {
		out := formatAccountMatches([]accounting.Account{active, inactive})
		if !strings.Contains(out, "1 matching active account(s):") || !strings.Contains(out, "6117 雜費") {
			t.Errorf("active section missing:\n%s", out)
		}
		disabledHeader := strings.Index(out, "disabled")
		if disabledHeader < 0 {
			t.Fatalf("inactive accounts should be flagged disabled:\n%s", out)
		}
		// The disabled account must appear only under the disabled header, never
		// among the postable candidates.
		if i := strings.Index(out, "6199"); i < disabledHeader {
			t.Errorf("inactive 6199 leaked into the active list:\n%s", out)
		}
	})

	t.Run("all active has no inactive section", func(t *testing.T) {
		out := formatAccountMatches([]accounting.Account{active})
		if strings.Contains(out, "inactive") || strings.Contains(out, "disabled") {
			t.Errorf("no inactive section expected:\n%s", out)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		if out := formatAccountMatches(nil); !strings.Contains(out, "No accounts match") {
			t.Errorf("unexpected empty-match message: %q", out)
		}
	})
}

func TestFindCounterpartiesHandler(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	for _, c := range []accounting.Counterparty{
		{ID: "CP-0001", Name: "台積電", Kind: accounting.CounterpartyCustomer, TaxID: "22099131", Active: true, Aliases: []string{"TSMC"}},
		{ID: "CP-0002", Name: "中華電信", Kind: accounting.CounterpartySupplier, Active: true},
		{ID: "CP-0003", Name: "舊廠商", Kind: accounting.CounterpartySupplier, Active: false},
	} {
		if err := repo.PutCounterparty(ctx, c); err != nil {
			t.Fatalf("seed counterparty: %v", err)
		}
	}
	handle := findCounterpartiesHandler(repo)

	t.Run("resolves an alias and shows the tax id", func(t *testing.T) {
		out, err := handle(ctx, json.RawMessage(`{"query":"TSMC","kind":""}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "CP-0001 台積電") || !strings.Contains(out, "22099131") {
			t.Errorf("expected the TSMC match with its tax id:\n%s", out)
		}
	})

	t.Run("kind filter excludes the other side", func(t *testing.T) {
		out, err := handle(ctx, json.RawMessage(`{"query":"中華電信","kind":"customer"}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "No counterparties match") {
			t.Errorf("a supplier should not match a customer filter:\n%s", out)
		}
	})

	t.Run("inactive match is flagged disabled, not referenceable", func(t *testing.T) {
		out, err := handle(ctx, json.RawMessage(`{"query":"舊廠商","kind":""}`))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "disabled") || !strings.Contains(out, "CP-0003") {
			t.Errorf("inactive counterparty should be flagged disabled:\n%s", out)
		}
	})
}
