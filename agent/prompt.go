package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/stoa/llm"
)

// PromptVersion identifies the prompt-and-Intent-schema contract a trajectory
// was produced under; bump it whenever the prompt or Intent schema changes so
// distillation records from different contracts stay distinguishable.
const PromptVersion = "v1"

// PromptRenderer builds provider-neutral messages for a bookkeeping turn. It
// holds chart snapshots so Render is free of repository I/O on the hot path.
// OperatorBranchID names the branch the operator is currently working from;
// the prompt asks the model to default to it when the user doesn't specify.
// Clock supplies the current instant the prompt reports as "Now"; nil falls
// back to time.Now().
type PromptRenderer struct {
	Company          accounting.Company
	Accounts         []accounting.Account
	Periods          []accounting.Period
	Branches         []accounting.Branch
	OperatorBranchID string
	Clock            bookkeeping.Clock
}

// NewPromptRenderer snapshots the company, chart, periods, and branches from repo.
func NewPromptRenderer(ctx context.Context, repo accounting.LedgerRepository) (PromptRenderer, error) {
	if repo == nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: NewPromptRenderer needs a repository")
	}
	company, ok, err := repo.Company(ctx)
	if err != nil {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: load company: %w", err)
	}
	if !ok {
		return PromptRenderer{}, fmt.Errorf("bookkeeper: ledger has no company; run `ledger seed` first")
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
		{Role: llm.MessageRoleSystem, Content: r.systemPrompt(hasTool(input.Tools, toolRecentEntries))},
		{Role: llm.MessageRoleUser, Content: taskMessage(input)},
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

func (r PromptRenderer) tenantContext() string {
	toolMode := r.activeAccountCount() > accountDumpThreshold

	var b strings.Builder
	fmt.Fprintf(&b, "Company: %s\n", r.Company.Name)
	if tz := r.Company.TimeZone; tz != "" {
		fmt.Fprintf(&b, "Timezone: %s (all dates are business dates in this zone)\n", tz)
	}
	now := r.now()
	loc := r.Company.Location()
	fmt.Fprintf(&b, "Now: %s; today's date is %s -- use it when the request states no date.\n",
		now.In(loc).Format("2006-01-02 15:04:05 -07:00"), accounting.DateOf(now, loc))

	if toolMode {
		b.WriteString("\n")
		b.WriteString(r.chartSummary())
	} else {
		b.WriteString("\nActive chart of accounts:\n")
		b.WriteString(r.activeAccounts())
		if inactive := r.inactiveAccounts(); inactive != "" {
			b.WriteString("\nInactive accounts (disabled, must not be used in a posting):\n")
			b.WriteString(inactive)
		}
	}

	b.WriteString("\nOpen accounting periods:\n")
	b.WriteString(r.openPeriods())

	if branches := r.branchesText(); branches != "" {
		b.WriteString("\nReporting branches (every line must carry one as branch_id):\n")
		b.WriteString(branches)
	}

	if name := r.operatorBranchName(); name != "" {
		fmt.Fprintf(&b, "\nOperator is working from branch %q (id: %s); default branch_id to this on new lines unless the user clearly specifies another.\n", name, r.OperatorBranchID)
	}

	if policy := strings.TrimSpace(r.Company.Policy); policy != "" {
		b.WriteString("\nCompany bookkeeping policy (authoritative; apply when choosing accounts):\n")
		b.WriteString(policy)
		b.WriteString("\n")
	}

	b.WriteString("\nWhen choosing values for post_journal:\n")
	if !toolMode {
		b.WriteString("  - pick account_code only from the active chart of accounts above.\n")
	}
	b.WriteString("  - pick period_id only from the open periods above.\n")

	return b.String()
}

func taskMessage(input llm.ReasoningInput) string {
	var b strings.Builder
	b.WriteString("Bookkeeping request:\n")
	b.WriteString(strings.TrimSpace(input.Task))
	if instr := strings.TrimSpace(input.Instructions); instr != "" {
		b.WriteString("\n\nFeature instructions:\n")
		b.WriteString(instr)
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

func intentsText() string {
	var b strings.Builder
	for _, c := range bookkeeping.Intents() {
		fmt.Fprintf(&b, "  - %s -- %s\n", c.Kind, c.Summary)
		fmt.Fprintf(&b, "      payload: {\"kind\":%q,%q:%s}\n", c.Kind, c.Kind, c.ArgsShape)
	}
	return b.String()
}

func (r PromptRenderer) activeAccounts() string {
	return r.accountsList(true)
}

func (r PromptRenderer) inactiveAccounts() string {
	return r.accountsList(false)
}

// accountsList renders chart accounts whose Active matches active, code-sorted.
func (r PromptRenderer) accountsList(active bool) string {
	sorted := append([]accounting.Account(nil), r.Accounts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Code < sorted[j].Code })

	var b strings.Builder
	for _, a := range sorted {
		if a.Active != active {
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
		fmt.Fprintf(&b, "  - %s [%s .. %s]\n", p.ID, p.Start, p.End)
	}
	return b.String()
}

// now resolves the clock to the current instant; nil Clock falls back to time.Now().
func (r PromptRenderer) now() time.Time {
	if r.Clock != nil {
		return r.Clock()
	}
	return time.Now()
}

func (r PromptRenderer) operatorBranchName() string {
	if r.OperatorBranchID == "" {
		return ""
	}
	for _, b := range r.Branches {
		if b.ID == r.OperatorBranchID {
			return b.Name
		}
	}
	return ""
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

// hasTool reports whether specs advertise a tool with the given name. The
// recall guidance is keyed off the recent_entries tool's presence so the prompt
// can never advertise a recall ability the tool set does not back.
func hasTool(specs []llm.ToolSpec, name string) bool {
	for _, s := range specs {
		if s.Name == name {
			return true
		}
	}
	return false
}

// systemPrompt assembles everything that is stable for this renderer's
// lifetime: agent role, intent catalog from the registry, payload format
// rules, behavior rules, and the tenant snapshot. recall adds the recall
// guidance; it is derived from the available tools, not stored. The user
// message only carries the per-call task and any optional Instructions.
func (r PromptRenderer) systemPrompt(recall bool) string {
	var b strings.Builder
	b.WriteString(systemPromptHeader)
	b.WriteString("\n\nAvailable intents:\n")
	b.WriteString(intentsText())
	b.WriteString(systemPromptFormatRules)
	b.WriteString(systemPromptBehaviorRules)
	b.WriteString(systemPromptMultiActionRules)
	if recall {
		b.WriteString(systemPromptRecallRules)
	}
	b.WriteString("\n\n")
	b.WriteString(r.tenantContext())
	return b.String()
}

const (
	systemPromptHeader = `You are a bookkeeping reasoning engine in a validated agent harness.
Each turn, choose exactly one intent from the catalog below and return it as a typed intent.`

	systemPromptFormatRules = `
Format rules for post_journal payloads:
  - amount is an integer in minor currency units per the ISO 4217 exponent.
      no-fraction currencies (TWD, JPY, KRW, ...): whole units, e.g. NT$100 = 100.
      two-decimal currencies (USD, EUR, GBP, ...): cents, e.g. $100 = 10000.
      three-decimal currencies (BHD, KWD, ...): mils, e.g. BHD 1 = 1000.
  - include at least two lines with one or more debits and one or more credits; total debit must equal total credit.
  - date is the business date in the company's timezone, formatted YYYY-MM-DD (e.g. 2026-05-12), and must fall inside the chosen period; when the request states no date, use today's date from the company context.
  - every line must carry a branch_id from the reporting branches list; all lines on one entry must share the same branch_id.
  - use one currency throughout.
`

	systemPromptBehaviorRules = `
Behavior rules:
  - If validation feedback is present, fix only the named problems and resubmit.
  - If the user specifies a period that is not in the open periods list, use reject and state that the period is closed. Do not substitute a different period.
  - If the user explicitly asks to use an account shown as inactive (in the chart listing or by find_accounts), use reject and state that the account is disabled. Do not substitute a different account.
  - When the chart of accounts is summarized rather than listed, call the find_accounts tool to look up account_code values; do not invent codes.
  - For a credit sale or purchase (accounts receivable/payable), look the customer or supplier up with find_counterparties and, when you find an active match, set its counterparty_id on the receivable/payable line. counterparty_id is OPTIONAL: if there is no matching counterparty (an unregistered, generic, or one-off party), still post the entry with counterparty_id empty -- do NOT reject a valid sale or purchase just because the party is not in the master list. Leave it empty on cash, tax, and internal lines, and record the invoice/receipt number in source when the request gives one. Only reject over a counterparty if the user insists on a specific party that find_counterparties shows as disabled.
  - To record a payment that clears a specific invoice or bill, use settle: it posts the receipt entry (Dr cash/bank, Cr the receivable/payable) and links it to that invoice's JE-id. Use a plain post_journal for a receipt that is not against a known invoice. Whether an invoice is fully or partly paid is not stored; do not guess it -- recover it from the entries if asked.`

	systemPromptMultiActionRules = `
Completing a request:
  - You may take several actions in one request -- e.g. reverse a wrong entry, then re-post the correction. Emit one action per turn.
  - Set "final": true on the action that finishes the request, "final": false on an action you will follow with another. A lone post, reverse, or reject is itself final.
  - The loop stops after a final action, so always finish with one -- never leave a request without a final action.`

	systemPromptRecallRules = `
Recall rules:
  - A request that carries a complete transaction (a date, a description, and amounts) is self-contained: act on it directly.
  - A request that refers to earlier work in this session ("that entry", "redo it", "change it to ...") or that omits the date, amounts, or description needed to post is not self-contained: call recent_entries (and get_entry for a specific id) to recover the details before acting.
  - Never invent a missing amount, date, or account. If a reference cannot be resolved from recall, use reject and ask for the missing detail.`
)
