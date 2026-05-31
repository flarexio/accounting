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
	Query string `json:"query"`
	Type  string `json:"type"`
}

// findAccountsArgsSchema is the JSON Schema OpenAI structured-outputs strict
// mode expects for find_accounts. Both args are required; type may be the
// empty string to skip the type filter.
const findAccountsArgsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["query", "type"],
  "properties": {
    "query": { "type": "string", "description": "Describe the economic event or the kind of account you need in natural language (e.g. \"employee business travel: high-speed rail and hotel\"). Ranked semantically against account names, descriptions, and aliases -- it is not a literal substring, so describe the meaning rather than guessing the exact account name." },
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
				Description: "Search the chart of accounts by describing the transaction or account in natural language. Active accounts are listed best match first; any matching inactive (disabled) accounts are listed separately and must not be used in a posting.",
				ArgsSchema:  json.RawMessage(findAccountsArgsSchema),
			},
			Handler: findAccountsHandler(repo),
		},
	}
}

// findAccountsHandler answers a find_accounts call by searching repo's chart.
// Inactive matches are returned too, flagged, so the model can refuse a
// disabled account the user named rather than silently substituting an active
// one; the posting itself may still only use an active account.
func findAccountsHandler(repo accounting.LedgerRepository) loop.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var p findAccountsArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("invalid find_accounts args: %w", err)
			}
		}
		matches, err := repo.FindAccounts(ctx, accounting.AccountFilter{
			Query: p.Query,
			Type:  accounting.AccountType(strings.TrimSpace(p.Type)),
		})
		if err != nil {
			return "", err
		}
		return formatAccountMatches(matches), nil
	}
}

// formatAccountMatches renders the active matches as the postable candidates
// and lists any inactive matches separately as disabled, so the model can tell
// a disabled account apart from a missing one.
func formatAccountMatches(accounts []accounting.Account) string {
	var active, inactive []accounting.Account
	for _, a := range accounts {
		if a.Active {
			active = append(active, a)
		} else {
			inactive = append(inactive, a)
		}
	}
	if len(active) == 0 && len(inactive) == 0 {
		return "No accounts match. Try a broader search term."
	}
	var b strings.Builder
	if len(active) > 0 {
		fmt.Fprintf(&b, "%d matching active account(s):", len(active))
		for _, a := range active {
			fmt.Fprintf(&b, "\n  - %s %s (%s)", a.Code, a.Name, a.Type)
		}
	} else {
		b.WriteString("No active accounts match.")
	}
	if len(inactive) > 0 {
		fmt.Fprintf(&b, "\n\n%d inactive account(s) matched -- disabled, must not be used in a posting:", len(inactive))
		for _, a := range inactive {
			fmt.Fprintf(&b, "\n  - %s %s (%s)", a.Code, a.Name, a.Type)
		}
	}
	return b.String()
}
