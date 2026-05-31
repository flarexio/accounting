package openai

import (
	"strings"
	"testing"

	"github.com/flarexio/accounting"
)

func acc(code string) accounting.Account {
	return accounting.Account{Code: code, Name: "name-" + code, Type: accounting.AccountExpense}
}

func codes(accs []accounting.Account) []string {
	out := make([]string, len(accs))
	for i, a := range accs {
		out[i] = a.Code
	}
	return out
}

func TestApplyRanking(t *testing.T) {
	candidates := []accounting.Account{acc("6104"), acc("6115"), acc("6117")}

	cases := []struct {
		name    string
		content string
		want    []string
		wantErr bool
	}{
		{"reorders to model order", `{"codes":["6117","6104","6115"]}`, []string{"6117", "6104", "6115"}, false},
		{"omitted codes appended in original order", `{"codes":["6115"]}`, []string{"6115", "6104", "6117"}, false},
		{"unknown codes ignored", `{"codes":["9999","6104"]}`, []string{"6104", "6115", "6117"}, false},
		{"duplicate codes collapse", `{"codes":["6104","6104","6115"]}`, []string{"6104", "6115", "6117"}, false},
		{"missing key keeps original order", `{"other":[]}`, []string{"6104", "6115", "6117"}, false},
		{"malformed json errors", `not json`, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := applyRanking(tc.content, candidates)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !equal(codes(got), tc.want) {
				t.Errorf("got %v, want %v", codes(got), tc.want)
			}
		})
	}
}

func TestRerankUserPrompt(t *testing.T) {
	prompt := rerankUserPrompt("paid the travel bill", []accounting.Account{
		{Code: "6104", Name: "旅費", Type: accounting.AccountExpense},
	})
	if !strings.Contains(prompt, "paid the travel bill") {
		t.Error("prompt should carry the query")
	}
	if !strings.Contains(prompt, "6104\t旅費 (expense)") {
		t.Errorf("prompt should list code, name and type; got:\n%s", prompt)
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
