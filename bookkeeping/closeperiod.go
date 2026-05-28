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

// Execute publishes the pre-built closing entries, then publishes a
// PeriodClosure event to flip Period.Status. It does not re-validate;
// unvalidated callers must use Handle. Returns an already-closed result with
// no entries when the period was already closed or when the projection shows
// rev/exp accounts already net to zero (i.e. a previous Execute posted the
// closing entries but crashed before publishing the closure -- the retry just
// re-publishes the closure).
func (uc ClosePeriod) Execute(ctx context.Context, intent ClosePeriodIntent) (ClosePeriodResult, error) {
	period, plans, alreadyClosed, err := uc.prepare(ctx, intent)
	if err != nil {
		return ClosePeriodResult{}, err
	}

	posted := make([]accounting.JournalEntry, 0, len(plans))
	for _, plan := range plans {
		dispatched, err := uc.Publisher.Publish(ctx, accounting.JournalPosted{
			Entry:     plan.entry,
			Relations: plan.relations,
		}, plan.expect)
		if err != nil {
			return ClosePeriodResult{}, fmt.Errorf("bookkeeping: publish: %w", err)
		}
		posted = append(posted, dispatched.Entry)
	}

	if period.Status != accounting.PeriodClosed {
		closurePub, ok := uc.Publisher.(PeriodClosurePublisher)
		if !ok {
			return ClosePeriodResult{}, errors.New("bookkeeping: publisher does not support period closure events")
		}
		closedPeriod := period
		closedPeriod.Status = accounting.PeriodClosed
		lastClosureSeq, err := uc.Repo.LastSequence(ctx, SubjectPeriodClosure)
		if err != nil {
			return ClosePeriodResult{}, fmt.Errorf("bookkeeping: read period-closure sequence: %w", err)
		}
		if _, err := closurePub.PublishPeriodClosure(ctx, accounting.PeriodClosure{
			Period: closedPeriod,
		}, accounting.ExpectedSequence{
			Subject: SubjectPeriodClosure,
			LastSeq: lastClosureSeq,
		}); err != nil {
			return ClosePeriodResult{}, fmt.Errorf("bookkeeping: publish period closure: %w", err)
		}
	}

	return ClosePeriodResult{PeriodID: intent.PeriodID, AlreadyClosed: alreadyClosed, Entries: posted}, nil
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

// subject resolves the publish subject; empty Subject falls back to SubjectLedger.
func (uc ClosePeriod) subject() string {
	if uc.Subject == "" {
		return SubjectLedger
	}
	return uc.Subject
}

// closingPlan is one fully-built closing event ready to publish: the entry has
// its ID and PostedAt assigned, the `closes` relations are constructed, and
// expect carries the optimistic-concurrency hint for the publish.
type closingPlan struct {
	entry     accounting.JournalEntry
	relations []accounting.JournalRelation
	expect    accounting.ExpectedSequence
}

// prepare resolves the period, validates every closing intent, and returns the
// fully-built list of events ready for Execute to publish. alreadyClosed is
// true when the period is already in PeriodClosed, or when rev/exp activity
// exists but every branch nets to zero (the closing entries are already in
// place; just the period flip is missing).
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

	// per-branch aggregation: account_code -> net (sum_debit - sum_credit;
	// positive means debit balance), plus the set of source entry ids that
	// contributed.
	type branchAggregate struct {
		balances map[string]int64
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
					balances: map[string]int64{},
					sources:  map[string]struct{}{},
				}
				branches[line.Dimensions.BranchID] = agg
			}
			switch line.Side {
			case accounting.SideDebit:
				agg.balances[line.AccountCode] += line.Amount
			case accounting.SideCredit:
				agg.balances[line.AccountCode] -= line.Amount
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

	subject := uc.subject()
	lastSeq, err := uc.Repo.LastSequence(ctx, subject)
	if err != nil {
		return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: read last sequence: %w", err)
	}
	postedAt := uc.now()
	currency := inferCurrency(entries)
	validator := accounting.Validator{Repo: uc.Repo}

	plans := make([]closingPlan, 0, len(branches))
	for _, branchID := range branchIDs {
		agg := branches[branchID]
		codes := make([]string, 0, len(agg.balances))
		for code, net := range agg.balances {
			if net != 0 {
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
			net := agg.balances[code]
			// Net debit balance (>0) closes via CREDIT; net credit balance (<0) closes via DEBIT.
			if net > 0 {
				lines = append(lines, accounting.JournalLine{
					AccountCode: code,
					Side:        accounting.SideCredit,
					Amount:      net,
					Dimensions:  accounting.Dimensions{BranchID: branchID},
				})
			} else {
				lines = append(lines, accounting.JournalLine{
					AccountCode: code,
					Side:        accounting.SideDebit,
					Amount:      -net,
					Dimensions:  accounting.Dimensions{BranchID: branchID},
				})
			}
			// Posting the opposite side on Retained Earnings keeps the entry
			// balanced; -net is that side's signed magnitude regardless of
			// whether the account is revenue or expense.
			plug -= net
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
			Currency:    currency,
			Description: fmt.Sprintf("Close period %s (branch %s)", period.ID, branchID),
			Lines:       lines,
		}
		if err := validator.Validate(ctx, closeIntent); err != nil {
			return accounting.Period{}, nil, false, fmt.Errorf("bookkeeping: close period %q branch %q: %w", period.ID, branchID, err)
		}

		// Pre-assign the entry ID and PostedAt so Execute publishes a
		// fully-formed entry. Each successive publish advances the broker's
		// stream sequence by one, so plan i expects lastSeq + i and assigns
		// JE-{lastSeq+i+1}.
		expectLast := lastSeq + uint64(len(plans))
		entry := accounting.JournalEntry{
			ID:          accounting.FormatEntryID(expectLast + 1),
			Date:        closeIntent.Date,
			PeriodID:    closeIntent.PeriodID,
			Currency:    closeIntent.Currency,
			Description: closeIntent.Description,
			Lines:       closeIntent.Lines,
			PostedAt:    postedAt,
		}
		relations := make([]accounting.JournalRelation, len(sources))
		for i, src := range sources {
			relations[i] = accounting.JournalRelation{
				FromEntry: entry.ID,
				ToEntry:   src,
				Type:      accounting.RelationCloses,
				Reason:    accounting.ReasonPeriodEnd,
			}
		}
		plans = append(plans, closingPlan{
			entry:     entry,
			relations: relations,
			expect:    accounting.ExpectedSequence{Subject: subject, LastSeq: expectLast},
		})
	}

	// rev/exp activity exists but every branch nets to zero: either every
	// closing entry has already been posted (a previous Execute crashed before
	// flipping the period and the retry sees nothing left to do) or the period
	// genuinely netted to zero from offsetting entries. Either way Execute
	// just needs to flip the period.
	if len(plans) == 0 {
		return period, nil, true, nil
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
