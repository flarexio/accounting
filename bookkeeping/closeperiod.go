package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/flarexio/accounting"
)

// ClosePeriodIntent names the period to close. The use case is rule-driven, not
// LLM-driven; the trigger is a scheduler invoking the ledger CLI.
type ClosePeriodIntent struct {
	PeriodID string `json:"period_id"`
}

// ClosePeriodResult is the per-branch summary the use case returns to the CLI.
type ClosePeriodResult struct {
	PeriodID      string                    `json:"period_id"`
	AlreadyClosed bool                      `json:"already_closed"`
	Entries       []accounting.JournalEntry `json:"entries,omitempty"`
}

// ClosePeriod zeros temporary accounts (revenue, expense) at period end. For
// each branch with revenue or expense activity in the period it posts one
// balanced closing entry that drains every contributing account into Retained
// Earnings and links the closing entry to each source entry via `closes`
// JournalRelation rows. After every entry is applied it flips the period to
// closed. Re-invoking against an already-closed period is a no-op.
type ClosePeriod struct {
	Repo      accounting.LedgerRepository
	Publisher EventPublisher
	Clock     Clock
	Subject   string
}

// Validate reports whether the period exists, is still open, has elapsed in
// the company's timezone, has revenue or expense activity to close, and the
// company is configured with a Retained Earnings account. It runs no side
// effect. An already-closed period is treated as a valid no-op.
func (uc ClosePeriod) Validate(ctx context.Context, intent ClosePeriodIntent) error {
	_, _, _, err := uc.prepare(ctx, intent)
	return err
}

// Execute closes the period: posts one closing JournalEntry per branch with
// activity, with `closes` relations to each contributing source entry, then
// flips Period.Status to closed. It does not re-validate; unvalidated callers
// must use Handle. Returns an already-closed result with no entries when the
// period was already closed.
func (uc ClosePeriod) Execute(ctx context.Context, intent ClosePeriodIntent) (ClosePeriodResult, error) {
	period, plans, alreadyClosed, err := uc.prepare(ctx, intent)
	if err != nil {
		return ClosePeriodResult{}, err
	}
	if alreadyClosed {
		return ClosePeriodResult{PeriodID: intent.PeriodID, AlreadyClosed: true}, nil
	}

	subject := uc.Subject
	if subject == "" {
		subject = SubjectLedger
	}

	posted := make([]accounting.JournalEntry, 0, len(plans))
	for _, plan := range plans {
		lastSeq, err := uc.Repo.LastSequence(ctx, subject)
		if err != nil {
			return ClosePeriodResult{}, fmt.Errorf("bookkeeping: read last sequence: %w", err)
		}

		entry := accounting.JournalEntry{
			ID:          accounting.FormatEntryID(lastSeq + 1),
			Date:        plan.intent.Date,
			PeriodID:    plan.intent.PeriodID,
			Currency:    plan.intent.Currency,
			Description: plan.intent.Description,
			Lines:       plan.intent.Lines,
			PostedAt:    uc.now(),
		}

		relations := make([]accounting.JournalRelation, len(plan.sources))
		for i, src := range plan.sources {
			relations[i] = accounting.JournalRelation{
				FromEntry: entry.ID,
				ToEntry:   src,
				Type:      accounting.RelationCloses,
				Reason:    accounting.ReasonPeriodEnd,
			}
		}

		dispatched, err := uc.Publisher.Publish(ctx, accounting.JournalPosted{
			Entry:     entry,
			Relations: relations,
		}, accounting.ExpectedSequence{
			Subject: subject,
			LastSeq: lastSeq,
		})
		if err != nil {
			return ClosePeriodResult{}, fmt.Errorf("bookkeeping: publish: %w", err)
		}
		posted = append(posted, dispatched.Entry)
	}

	period.Status = accounting.PeriodClosed
	if err := uc.Repo.PutPeriod(ctx, period); err != nil {
		return ClosePeriodResult{}, fmt.Errorf("bookkeeping: close period %q: %w", intent.PeriodID, err)
	}

	return ClosePeriodResult{PeriodID: intent.PeriodID, Entries: posted}, nil
}

// Handle validates intent and, if clean, executes it.
func (uc ClosePeriod) Handle(ctx context.Context, intent ClosePeriodIntent) (ClosePeriodResult, error) {
	if err := uc.Validate(ctx, intent); err != nil {
		return ClosePeriodResult{}, err
	}
	return uc.Execute(ctx, intent)
}

// now resolves the clock; nil Clock falls back to time.Now().UTC().
func (uc ClosePeriod) now() time.Time {
	if uc.Clock == nil {
		return time.Now().UTC()
	}
	return uc.Clock()
}

type closingPlan struct {
	intent  accounting.JournalIntent
	sources []string
}

func (uc ClosePeriod) prepare(ctx context.Context, intent ClosePeriodIntent) (accounting.Period, []closingPlan, bool, error) {
	if uc.Repo == nil {
		return accounting.Period{}, nil, false, errors.New("bookkeeping: close period has no repository")
	}
	if uc.Publisher == nil {
		return accounting.Period{}, nil, false, errors.New("bookkeeping: close period has no event publisher")
	}
	if intent.PeriodID == "" {
		return accounting.Period{}, nil, false, errors.New("bookkeeping: close period needs a period_id")
	}

	period, ok, err := uc.Repo.Period(ctx, intent.PeriodID)
	if err != nil {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: load period %q: %w", intent.PeriodID, err)
	}
	if !ok {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: period %q does not exist", intent.PeriodID)
	}
	if period.Status == accounting.PeriodClosed {
		return period, nil, true, nil
	}

	company, ok, err := uc.Repo.Company(ctx)
	if err != nil {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: load company: %w", err)
	}
	if !ok {
		return accounting.Period{}, nil, false, errors.New("bookkeeping: ledger has no company set")
	}
	if company.RetainedEarningsCode == "" {
		return accounting.Period{}, nil, false, errors.New("bookkeeping: company has no retained_earnings_code configured")
	}

	today := accounting.DateOf(uc.now(), company.Location())
	if !today.After(period.End) {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: period %q ends %s in %s; cannot close before %s",
			period.ID, period.End, company.TimeZone, period.End)
	}

	accounts, err := uc.Repo.Accounts(ctx)
	if err != nil {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: load accounts: %w", err)
	}
	accountType := make(map[string]accounting.AccountType, len(accounts))
	for _, a := range accounts {
		accountType[a.Code] = a.Type
	}
	switch accountType[company.RetainedEarningsCode] {
	case accounting.AccountEquity:
	case "":
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: retained_earnings_code %q is not in the chart of accounts", company.RetainedEarningsCode)
	default:
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: retained_earnings_code %q must be an equity account", company.RetainedEarningsCode)
	}

	entries, err := uc.Repo.EntriesByPeriod(ctx, intent.PeriodID)
	if err != nil {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: load entries: %w", err)
	}

	type accountBalance struct {
		code string
		typ  accounting.AccountType
		net  int64 // sum_debit - sum_credit (positive => debit balance)
	}
	type branchAggregate struct {
		balances map[string]*accountBalance
		sources  map[string]struct{}
	}
	branches := map[string]*branchAggregate{}

	for _, entry := range entries {
		for _, line := range entry.Lines {
			t, isTemporary := accountType[line.AccountCode]
			if !isTemporary {
				continue
			}
			if t != accounting.AccountRevenue && t != accounting.AccountExpense {
				continue
			}
			agg := branches[line.Dimensions.BranchID]
			if agg == nil {
				agg = &branchAggregate{
					balances: map[string]*accountBalance{},
					sources:  map[string]struct{}{},
				}
				branches[line.Dimensions.BranchID] = agg
			}
			bal := agg.balances[line.AccountCode]
			if bal == nil {
				bal = &accountBalance{code: line.AccountCode, typ: t}
				agg.balances[line.AccountCode] = bal
			}
			switch line.Side {
			case accounting.SideDebit:
				bal.net += line.Amount
			case accounting.SideCredit:
				bal.net -= line.Amount
			}
			agg.sources[entry.ID] = struct{}{}
		}
	}

	if len(branches) == 0 {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: period %q has no revenue or expense activity to close", intent.PeriodID)
	}

	branchIDs := make([]string, 0, len(branches))
	for id := range branches {
		branchIDs = append(branchIDs, id)
	}
	sort.Strings(branchIDs)

	validator := accounting.Validator{Repo: uc.Repo}
	plans := make([]closingPlan, 0, len(branches))
	for _, branchID := range branchIDs {
		agg := branches[branchID]
		codes := make([]string, 0, len(agg.balances))
		for code, bal := range agg.balances {
			if bal.net != 0 {
				codes = append(codes, code)
			}
		}
		if len(codes) == 0 {
			continue
		}
		sort.Strings(codes)

		var lines []accounting.JournalLine
		var plug int64 // signed: positive = retained earnings ends up CR (net income)
		for _, code := range codes {
			bal := agg.balances[code]
			// Net debit balance (>0) closes via CREDIT; net credit balance (<0) closes via DEBIT.
			if bal.net > 0 {
				lines = append(lines, accounting.JournalLine{
					AccountCode: code,
					Side:        accounting.SideCredit,
					Amount:      bal.net,
					Dimensions:  accounting.Dimensions{BranchID: branchID},
				})
			} else {
				lines = append(lines, accounting.JournalLine{
					AccountCode: code,
					Side:        accounting.SideDebit,
					Amount:      -bal.net,
					Dimensions:  accounting.Dimensions{BranchID: branchID},
				})
			}
			// Posting the opposite side on Retained Earnings keeps the entry
			// balanced; -bal.net is that side's signed magnitude regardless of
			// whether the account is revenue or expense.
			plug -= bal.net
		}
		// plug = net income contribution from this branch: positive => CR Retained Earnings.
		if plug == 0 {
			continue
		}
		plugLine := accounting.JournalLine{
			AccountCode: company.RetainedEarningsCode,
			Amount:      abs64(plug),
			Dimensions:  accounting.Dimensions{BranchID: branchID},
		}
		if plug > 0 {
			plugLine.Side = accounting.SideCredit
		} else {
			plugLine.Side = accounting.SideDebit
		}
		lines = append(lines, plugLine)

		sources := make([]string, 0, len(agg.sources))
		for id := range agg.sources {
			sources = append(sources, id)
		}
		sort.Strings(sources)

		closeIntent := accounting.JournalIntent{
			Date:        period.End,
			PeriodID:    period.ID,
			Currency:    inferCurrency(entries),
			Description: fmt.Sprintf("Close period %s (branch %s)", period.ID, branchID),
			Lines:       lines,
		}
		if err := validator.Validate(ctx, closeIntent); err != nil {
			return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: close period %q branch %q: %w", period.ID, branchID, err)
		}
		plans = append(plans, closingPlan{intent: closeIntent, sources: sources})
	}

	if len(plans) == 0 {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: period %q nets to zero across every branch; nothing to close", intent.PeriodID)
	}

	return period, plans, false, nil
}

// inferCurrency picks the currency of the first entry with a non-empty value;
// closing entries inherit it so the validator's invariants stay symmetric.
func inferCurrency(entries []accounting.JournalEntry) string {
	for _, e := range entries {
		if e.Currency != "" {
			return e.Currency
		}
	}
	return ""
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
