package agent

import (
	"strings"
	"testing"

	"github.com/flarexio/accounting"
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
