package accounting_test

import (
	"testing"

	"github.com/flarexio/accounting"
)

func TestFormatCounterpartyID(t *testing.T) {
	if got := accounting.FormatCounterpartyID(1); got != "CP-0001" {
		t.Errorf("FormatCounterpartyID(1) = %q, want CP-0001", got)
	}
	if got := accounting.FormatCounterpartyID(42); got != "CP-0042" {
		t.Errorf("FormatCounterpartyID(42) = %q, want CP-0042", got)
	}
}

func TestCounterpartyMatch(t *testing.T) {
	cp := accounting.Counterparty{
		ID:      "CP-0001",
		Name:    "台灣積體電路製造股份有限公司",
		TaxID:   "22099131",
		Aliases: []string{"台積電", "TSMC"},
	}

	tests := []struct {
		name     string
		query    string
		wantTier int
		wantOK   bool
	}{
		{"exact id", "cp-0001", 0, true},
		{"exact name", "台灣積體電路製造股份有限公司", 1, true},
		{"exact tax id", "22099131", 2, true},
		{"exact alias", "台積電", 3, true},
		{"alias case-insensitive", "tsmc", 3, true},
		{"name substring", "積體電路", 4, true},
		{"no match", "鴻海", 0, false},
		{"empty query", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tier, ok := accounting.CounterpartyMatch(tc.query, cp)
			if ok != tc.wantOK || (ok && tier != tc.wantTier) {
				t.Errorf("CounterpartyMatch(%q) = (%d, %v), want (%d, %v)", tc.query, tier, ok, tc.wantTier, tc.wantOK)
			}
		})
	}
}
