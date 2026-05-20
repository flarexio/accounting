package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

// PromptRenderer builds provider-neutral messages for a bookkeeping turn. It
// holds chart snapshots so Render is free of repository I/O on the hot path.
type PromptRenderer struct {
	Company  accounting.Company
	Accounts []accounting.Account
	Periods  []accounting.Period
	Branches []accounting.Branch
}

// NewPromptRenderer reads chart, periods, and branches from repo once.
func NewPromptRenderer(ctx context.Context, company accounting.Company, repo accounting.LedgerRepository) (PromptRenderer, error) {
	if repo == nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: NewPromptRenderer needs a repository")
	}
	accounts, err := repo.Accounts(ctx)
	if err != nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: load accounts: %w", err)
	}
	periods, err := repo.Periods(ctx)
	if err != nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: load periods: %w", err)
	}
	branches, err := repo.Branches(ctx)
	if err != nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: load branches: %w", err)
	}
	return PromptRenderer{
		Company:  company,
		Accounts: accounts,
		Periods:  periods,
		Branches: branches,
	}, nil
}

func (r PromptRenderer) Render(input llm.ReasoningInput) ([]llm.Message, error) {
	messages := []llm.Message{
		{Role: llm.MessageRoleSystem, Content: bookkeeperSystemPrompt},
		{Role: llm.MessageRoleUser, Content: r.buildUserPrompt(input)},
	}

	for _, event := range input.Events {
		content := fmt.Sprintf("[%s:%s]\n%s", event.Role, event.Kind, strings.TrimSpace(event.Content))
		role := llm.MessageRoleUser
		if event.Role == llm.EventRoleAssistant {
			role = llm.MessageRoleAssistant
		}
		messages = append(messages, llm.Message{Role: role, Content: content})
	}

	return messages, nil
}

// Above this active-account count the chart is summarized and the model uses find_accounts.
const accountDumpThreshold = 12

func (r PromptRenderer) buildUserPrompt(input llm.ReasoningInput) string {
	toolMode := r.activeAccountCount() > accountDumpThreshold

	var b strings.Builder
	b.WriteString("Bookkeeping request:\n")
	b.WriteString(strings.TrimSpace(input.Task))
	if instr := strings.TrimSpace(input.Instructions); instr != "" {
		b.WriteString("\n\nFeature instructions:\n")
		b.WriteString(instr)
	}

	fmt.Fprintf(&b, "\n\nCompany: %s\n", r.Company.Name)

	if toolMode {
		b.WriteString("\n")
		b.WriteString(r.chartSummary())
	} else {
		b.WriteString("\nActive chart of accounts:\n")
		b.WriteString(r.activeAccounts())
	}

	b.WriteString("\nOpen accounting periods:\n")
	b.WriteString(r.openPeriods())

	if branches := r.branchesText(); branches != "" {
		b.WriteString("\nReporting branches (optional dimension on each line):\n")
		b.WriteString(branches)
	}

	b.WriteString("\nNotes for post_journal:\n")
	b.WriteString("  - amount is an integer in minor currency units. $100 USD = 10000.\n")
	b.WriteString("  - include at least two lines with one or more debits and one or more credits; total debit must equal total credit.\n")
	b.WriteString("  - date must be RFC3339 (e.g. 2026-05-12T00:00:00Z) and fall inside the chosen period.\n")
	if toolMode {
		b.WriteString("  - the chart of accounts is not listed; call find_accounts to look up the account_code values you need.\n")
	} else {
		b.WriteString("  - pick account_code only from the active chart of accounts above.\n")
	}
	b.WriteString("  - pick period_id only from the open periods above.\n")

	b.WriteString("\nAvailable intents -- choose exactly one:\n")
	b.WriteString(intentsText())

	if toolMode {
		b.WriteString("\nTool -- find_accounts: search the chart of accounts by name.\n")
		b.WriteString("  args: " + findAccountsArgsShape + "\n")
		b.WriteString("\nEach turn, return JSON in ONE of these two shapes.\n")
		b.WriteString("To look up accounts:\n")
		b.WriteString(toolCallJSONShape + "\n")
		b.WriteString("To run a command:\n")
		b.WriteString(intentEnvelopeShape)
	} else {
		b.WriteString("\nReturn JSON with this exact shape:\n")
		b.WriteString(intentEnvelopeShape)
	}

	return b.String()
}

func (r PromptRenderer) chartSummary() string {
	byType := map[accounting.AccountType]int{}
	total := 0
	for _, a := range r.Accounts {
		if !a.Active {
			continue
		}
		total++
		byType[a.Type]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Chart of accounts: %d active accounts (not listed -- use find_accounts).\n", total)
	for _, t := range []accounting.AccountType{
		accounting.AccountAsset, accounting.AccountLiability, accounting.AccountEquity,
		accounting.AccountRevenue, accounting.AccountExpense,
	} {
		if n := byType[t]; n > 0 {
			fmt.Fprintf(&b, "  - %s: %d\n", t, n)
		}
	}
	return b.String()
}

func (r PromptRenderer) activeAccountCount() int {
	n := 0
	for _, a := range r.Accounts {
		if a.Active {
			n++
		}
	}
	return n
}

const (
	findAccountsArgsShape = `{"name_contains":"<text>","type":"<asset|liability|equity|revenue|expense; optional>"}`

	toolCallJSONShape = `{"evidence":[{"source":"request","fact":"..."}],"rationale":"...","tool_calls":[{"name":"find_accounts","args":{"name_contains":"rent"}}]}`

	intentEnvelopeShape = `{"evidence":[{"source":"...","fact":"..."}],"rationale":"...","intent":<one command intent object from the list above>}`
)

func intentsText() string {
	var b strings.Builder
	for _, c := range bookkeeping.Intents() {
		fmt.Fprintf(&b, "  - %s -- %s\n", c.Kind, c.Summary)
		fmt.Fprintf(&b, "      intent: {\"kind\":%q,%q:%s}\n", c.Kind, c.Kind, c.ArgsShape)
	}
	return b.String()
}

func (r PromptRenderer) activeAccounts() string {
	sorted := append([]accounting.Account(nil), r.Accounts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })

	var b strings.Builder
	for _, a := range sorted {
		if !a.Active {
			continue
		}
		fmt.Fprintf(&b, "  - %s %s (%s)\n", a.Code, a.Name, a.Type)
	}
	return b.String()
}

func (r PromptRenderer) openPeriods() string {
	sorted := append([]accounting.Period(nil), r.Periods...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var b strings.Builder
	for _, p := range sorted {
		if p.Status != accounting.PeriodOpen {
			continue
		}
		fmt.Fprintf(&b, "  - %s [%s .. %s]\n", p.ID, p.Start.Format("2006-01-02"), p.End.Format("2006-01-02"))
	}
	return b.String()
}

func (r PromptRenderer) branchesText() string {
	sorted := append([]accounting.Branch(nil), r.Branches...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	var b strings.Builder
	for _, br := range sorted {
		fmt.Fprintf(&b, "  - %s (%s)\n", br.ID, br.Name)
	}
	return b.String()
}

const bookkeeperSystemPrompt = `You are a bookkeeping reasoning engine in a validated agent harness.
Each turn you choose ONE intent and return it as a typed intent:
- post_journal: post a new journal entry. Include at least two lines; total debit must equal total credit; use only active account codes; reference an open period_id and a date inside it; use one currency throughout.
- reverse_journal: reverse an existing posted entry. Supply the entry's JE-id and a short reason; the mirror-image entry is built and validated for you.
Rules you must follow:
- If validation feedback is present, fix only the named problems and resubmit.
- Output JSON only. No prose outside the JSON object.`
