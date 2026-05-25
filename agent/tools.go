package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/flarexio/accounting"
	"github.com/flarexio/stoa/harness/loop"
	"github.com/flarexio/stoa/llm"
)

const toolFindAccounts = "find_accounts"

type findAccountsArgs struct {
	NameContains string `json:"name_contains"`
	Type         string `json:"type"`
}

// findAccountsArgsSchema is the JSON Schema OpenAI structured-outputs strict
// mode expects for find_accounts. Both args are required; type may be the
// empty string to skip the type filter.
const findAccountsArgsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["name_contains", "type"],
  "properties": {
    "name_contains": { "type": "string", "description": "Substring matched case-insensitively against account names." },
    "type": {
      "type": "string",
      "description": "Restrict to one account type; empty string means all types.",
      "enum": ["", "asset", "liability", "equity", "revenue", "expense"]
    }
  }
}`

// accountTools returns the tool registry the bookkeeping agent exposes. Each
// tool carries the spec the harness forwards to the provider's native
// tools/tool_calls channel and the handler that runs when the model invokes it.
func accountTools(repo accounting.LedgerRepository) map[string]loop.Tool {
	return map[string]loop.Tool{
		toolFindAccounts: {
			Spec: llm.ToolSpec{
				Name:        toolFindAccounts,
				Description: "Search the chart of accounts by case-insensitive name substring; returns only active accounts.",
				ArgsSchema:  json.RawMessage(findAccountsArgsSchema),
			},
			Handler: findAccountsHandler(repo),
		},
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
