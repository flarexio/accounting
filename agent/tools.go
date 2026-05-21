package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flarexio/accounting"
	"github.com/flarexio/stoa/harness/loop"
)

const toolFindAccounts = "find_accounts"

type findAccountsArgs struct {
	NameContains string `json:"name_contains"`
	Type         string `json:"type"`
}

// accountTools returns the tool handlers the bookkeeping agent exposes.
func accountTools(repo accounting.LedgerRepository) map[string]loop.ToolHandler {
	return map[string]loop.ToolHandler{
		toolFindAccounts: findAccountsHandler(repo),
	}
}

// findAccountsHandler answers a find_accounts call by searching repo's chart.
// Only active accounts are returned -- a posting may not use an inactive one.
func findAccountsHandler(repo accounting.LedgerRepository) loop.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var p findAccountsArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("invalid find_accounts args: %w", err)
			}
		}
		matches, err := repo.FindAccounts(ctx, accounting.AccountFilter{
			NameContains: p.NameContains,
			Type:         accounting.AccountType(strings.TrimSpace(p.Type)),
			ActiveOnly:   true,
		})
		if err != nil {
			return "", err
		}
		return formatAccountMatches(matches), nil
	}
}

func formatAccountMatches(accounts []accounting.Account) string {
	if len(accounts) == 0 {
		return "No active accounts match. Try a broader search term."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d matching active account(s):", len(accounts))
	for _, a := range accounts {
		fmt.Fprintf(&b, "\n  - %s %s (%s)", a.Code, a.Name, a.Type)
	}
	return b.String()
}
