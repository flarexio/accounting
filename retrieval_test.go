package accounting_test

import (
	"testing"

	"github.com/flarexio/accounting"
)

func codes(accs []accounting.Account) []string {
	out := make([]string, len(accs))
	for i, a := range accs {
		out[i] = a.Code
	}
	return out
}

func acc(code string) accounting.Account { return accounting.Account{Code: code, Name: code} }

func TestFuseAccountsRRF(t *testing.T) {
	t.Run("single channel preserves order", func(t *testing.T) {
		got := accounting.FuseAccountsRRF([][]accounting.Account{
			{acc("a"), acc("b"), acc("c")},
		}, 0)
		if want := []string{"a", "b", "c"}; !equal(codes(got), want) {
			t.Errorf("got %v, want %v", codes(got), want)
		}
	})

	t.Run("agreement across channels outranks a single-channel top hit", func(t *testing.T) {
		// "b" is rank 2 in both channels; "a" and "x" each top one channel.
		got := accounting.FuseAccountsRRF([][]accounting.Account{
			{acc("a"), acc("b")},
			{acc("x"), acc("b")},
		}, 0)
		if got[0].Code != "b" {
			t.Errorf("expected the doubly-matched code first, got %v", codes(got))
		}
	})

	t.Run("limit caps the fused result", func(t *testing.T) {
		got := accounting.FuseAccountsRRF([][]accounting.Account{
			{acc("a"), acc("b"), acc("c"), acc("d")},
		}, 2)
		if len(got) != 2 {
			t.Fatalf("expected 2 results, got %d (%v)", len(got), codes(got))
		}
	})

	t.Run("account value comes from the first channel carrying the code", func(t *testing.T) {
		got := accounting.FuseAccountsRRF([][]accounting.Account{
			{{Code: "a", Name: "From Dense"}},
			{{Code: "a", Name: "From Lexical"}},
		}, 0)
		if len(got) != 1 || got[0].Name != "From Dense" {
			t.Fatalf("expected the first channel's Account value, got %+v", got)
		}
	})
}

func TestLexicalAccountTier(t *testing.T) {
	cases := []struct {
		name       string
		query      string
		code, acct string
		wantTier   int
		wantOK     bool
	}{
		{"exact code", "6104", "6104", "旅費", 0, true},
		{"exact name", "旅費", "6104", "旅費", 1, true},
		{"name inside query", "員工旅費出差", "6104", "旅費", 2, true},
		{"query inside name", "旅", "6104", "旅費", 2, true},
		{"code inside query", "支付 6104 這筆", "6104", "旅費", 3, true},
		{"case-insensitive name", "CASH", "1010", "Cash", 1, true},
		{"no relation", "員工出差搭高鐵", "6104", "旅費", 0, false},
		{"empty query", "   ", "6104", "旅費", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tier, ok := accounting.LexicalAccountTier(tc.query, tc.code, tc.acct)
			if ok != tc.wantOK || (ok && tier != tc.wantTier) {
				t.Errorf("LexicalAccountTier(%q,%q,%q) = (%d,%v), want (%d,%v)",
					tc.query, tc.code, tc.acct, tier, ok, tc.wantTier, tc.wantOK)
			}
		})
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
