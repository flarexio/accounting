package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

const toolFindCounterparties = "find_counterparties"

type findCounterpartiesArgs struct {
	Query string `json:"query"`
	Kind  string `json:"kind"`
}

// findCounterpartiesArgsSchema is the strict-mode schema for find_counterparties.
// Both args are required; kind may be the empty string to skip the filter.
const findCounterpartiesArgsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["query", "kind"],
  "properties": {
    "query": { "type": "string", "description": "Name, alias, or tax id of the customer or supplier, e.g. \"TSMC\" or \"22099131\"." },
    "kind": {
      "type": "string",
      "description": "Restrict to customers or suppliers; empty string means either.",
      "enum": ["", "customer", "supplier"]
    }
  }
}`

// counterpartyTools returns the find_counterparties tool: it resolves a
// customer/supplier the user named to its CP-id so a posting can reference it.
func counterpartyTools(repo accounting.LedgerRepository) map[string]loop.Tool {
	return map[string]loop.Tool{
		toolFindCounterparties: {
			Spec: llm.ToolSpec{
				Name:        toolFindCounterparties,
				Description: "Look up a customer or supplier by name, alias, or tax id. Active matches are listed best match first; inactive ones are listed separately and must not be referenced by a new posting.",
				ArgsSchema:  json.RawMessage(findCounterpartiesArgsSchema),
			},
			Handler: findCounterpartiesHandler(repo),
		},
	}
}

const (
	toolRecentEntries = "recent_entries"
	toolGetEntry      = "get_entry"
)

const recentEntriesArgsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["limit"],
  "properties": {
    "limit": { "type": "integer", "description": "How many of the most recent entries to return; 0 means all that are remembered." }
  }
}`

const getEntryArgsSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["entry_id"],
  "properties": {
    "entry_id": { "type": "string", "description": "The entry id to fetch, e.g. JE-0001." }
  }
}`

type recentEntriesArgs struct {
	Limit int `json:"limit"`
}

type getEntryArgs struct {
	EntryID string `json:"entry_id"`
}

// recallTools lets a later turn recover what an earlier turn did without the
// transcript: recent_entries lists this session's recent postings, get_entry
// fetches one by id from the ledger. They are only wired when the bookkeeper
// carries a RecentEntries.
func recallTools(repo accounting.LedgerRepository, recent *RecentEntries) map[string]loop.Tool {
	return map[string]loop.Tool{
		toolRecentEntries: {
			Spec: llm.ToolSpec{
				Name:        toolRecentEntries,
				Description: "List the journal entries you have posted earlier in this session, most recent first, with their lines. Use it to resolve a request that refers to prior work (\"that entry\", \"redo it\", \"change it to ...\") instead of guessing.",
				ArgsSchema:  json.RawMessage(recentEntriesArgsSchema),
			},
			Handler: recentEntriesHandler(recent),
		},
		toolGetEntry: {
			Spec: llm.ToolSpec{
				Name:        toolGetEntry,
				Description: "Fetch one posted journal entry by id (e.g. JE-0001), with its lines, from the ledger.",
				ArgsSchema:  json.RawMessage(getEntryArgsSchema),
			},
			Handler: getEntryHandler(repo),
		},
	}
}

func recentEntriesHandler(recent *RecentEntries) loop.ToolHandler {
	return func(_ context.Context, args json.RawMessage) (string, error) {
		var p recentEntriesArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("invalid recent_entries args: %w", err)
			}
		}
		entries := recent.Recent(p.Limit)
		if len(entries) == 0 {
			return "No entries posted yet in this session.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d recent entry(ies) this session, most recent first:", len(entries))
		for _, e := range entries {
			b.WriteString("\n")
			b.WriteString(formatEntry(e))
		}
		return b.String(), nil
	}
}

func getEntryHandler(repo accounting.LedgerRepository) loop.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var p getEntryArgs
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid get_entry args: %w", err)
		}
		entry, found, err := repo.Entry(ctx, strings.TrimSpace(p.EntryID))
		if err != nil {
			return "", err
		}
		if !found {
			return fmt.Sprintf("No entry %q exists.", p.EntryID), nil
		}
		return formatEntry(entry), nil
	}
}

// formatEntry renders one entry compactly: header line plus one line per posting line.
func formatEntry(e accounting.JournalEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s · %s · %s · %s", e.ID, e.Date, e.PeriodID, e.Description)
	for _, l := range e.Lines {
		fmt.Fprintf(&b, "\n    %s %s %d (%s)", l.AccountCode, l.Side, l.Amount, l.Dimensions.BranchID)
	}
	return b.String()
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

// findCounterpartiesHandler answers a find_counterparties call by lexically
// ranking the chart of counterparties. The list is small, so it loads all and
// ranks in memory rather than pushing the search to the adapter.
func findCounterpartiesHandler(repo accounting.LedgerRepository) loop.ToolHandler {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var p findCounterpartiesArgs
		if len(args) > 0 {
			if err := json.Unmarshal(args, &p); err != nil {
				return "", fmt.Errorf("invalid find_counterparties args: %w", err)
			}
		}
		all, err := repo.Counterparties(ctx)
		if err != nil {
			return "", err
		}
		kind := accounting.CounterpartyKind(strings.TrimSpace(p.Kind))
		type scored struct {
			cp   accounting.Counterparty
			tier int
		}
		var matched []scored
		for _, c := range all {
			if !counterpartyKindMatches(kind, c.Kind) {
				continue
			}
			if tier, ok := accounting.CounterpartyMatch(p.Query, c); ok {
				matched = append(matched, scored{c, tier})
			}
		}
		sort.SliceStable(matched, func(i, j int) bool {
			if matched[i].tier != matched[j].tier {
				return matched[i].tier < matched[j].tier
			}
			return matched[i].cp.ID < matched[j].cp.ID
		})
		out := make([]accounting.Counterparty, len(matched))
		for i, m := range matched {
			out[i] = m.cp
		}
		return formatCounterpartyMatches(out), nil
	}
}

// counterpartyKindMatches reports whether a counterparty of kind cp satisfies a
// query filter; an empty filter matches anything and "both" matches either side.
func counterpartyKindMatches(filter, cp accounting.CounterpartyKind) bool {
	if filter == "" || cp == accounting.CounterpartyBoth {
		return true
	}
	return filter == cp
}

// formatCounterpartyMatches renders active matches as the referenceable
// candidates and lists any inactive ones separately as disabled.
func formatCounterpartyMatches(cps []accounting.Counterparty) string {
	var active, inactive []accounting.Counterparty
	for _, c := range cps {
		if c.Active {
			active = append(active, c)
		} else {
			inactive = append(inactive, c)
		}
	}
	if len(active) == 0 && len(inactive) == 0 {
		return "No counterparties match. Try the name, an alias, or the tax id."
	}
	var b strings.Builder
	if len(active) > 0 {
		fmt.Fprintf(&b, "%d matching counterparty(ies):", len(active))
		for _, c := range active {
			writeCounterparty(&b, c)
		}
	} else {
		b.WriteString("No active counterparties match.")
	}
	if len(inactive) > 0 {
		fmt.Fprintf(&b, "\n\n%d inactive counterparty(ies) matched -- disabled, must not be referenced in a posting:", len(inactive))
		for _, c := range inactive {
			writeCounterparty(&b, c)
		}
	}
	return b.String()
}

func writeCounterparty(b *strings.Builder, c accounting.Counterparty) {
	fmt.Fprintf(b, "\n  - %s %s (%s)", c.ID, c.Name, c.Kind)
	if c.TaxID != "" {
		fmt.Fprintf(b, " TaxID %s", c.TaxID)
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
