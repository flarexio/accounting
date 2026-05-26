package accounting_test

import (
	"strings"
	"testing"

	"github.com/flarexio/accounting"
)

const sampleSeedYAML = `name: sample
company:
  id: acme
  name: Acme Co.
  timezone: Asia/Taipei
accounts:
  - { code: "1000", name: Cash, type: asset, active: true }
  - { code: "4000", name: Sales, type: revenue, active: false }
branches:
  - { id: hq, name: Headquarters }
periods:
  - { id: "2026-05", start: "2026-05-01", end: "2026-05-31", status: open }
`

func TestDecodeScenarioYAML(t *testing.T) {
	s, err := accounting.DecodeScenarioYAML(strings.NewReader(sampleSeedYAML))
	if err != nil {
		t.Fatalf("DecodeScenarioYAML: %v", err)
	}
	if s.Company.ID != "acme" {
		t.Errorf("company id: got %q, want acme", s.Company.ID)
	}
	if len(s.Accounts) != 2 {
		t.Fatalf("accounts: got %d, want 2", len(s.Accounts))
	}
	if s.Accounts[0].Code != "1000" || s.Accounts[0].Type != accounting.AccountAsset {
		t.Errorf("account[0]: got %+v", s.Accounts[0])
	}
	if s.Accounts[1].Active {
		t.Errorf("account[1] should decode as inactive")
	}
	if len(s.Periods) != 1 {
		t.Fatalf("periods: got %d, want 1", len(s.Periods))
	}
	wantStart := accounting.NewDate(2026, 5, 1)
	if !s.Periods[0].Start.Equal(wantStart) {
		t.Errorf("period start: got %v, want %v", s.Periods[0].Start, wantStart)
	}
	if s.Company.TimeZone != "Asia/Taipei" {
		t.Errorf("company timezone: got %q, want Asia/Taipei", s.Company.TimeZone)
	}
	if s.Periods[0].Status != accounting.PeriodOpen {
		t.Errorf("period status: got %q, want open", s.Periods[0].Status)
	}
}

func TestDecodeScenarioYAML_RejectsUnknownField(t *testing.T) {
	const badYAML = `company:
  id: acme
  name: Acme Co.
accounts: []
periods: []
currency: TWD
`
	if _, err := accounting.DecodeScenarioYAML(strings.NewReader(badYAML)); err == nil {
		t.Fatal("expected an error for the unknown top-level field 'currency'")
	}
}
