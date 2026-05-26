package accounting

import (
	"context"
	"errors"
	"fmt"
)

// Validator enforces the accounting invariants on a proposed JournalIntent.
// It reads from a LedgerRepository and never mutates it.
type Validator struct {
	Repo LedgerRepository
}

// Validate returns nil if intent satisfies every invariant, or a joined error
// describing every domain violation so the agent can fix them in one cycle.
// Infrastructure errors from Repo are returned immediately, not joined.
func (v Validator) Validate(ctx context.Context, intent JournalIntent) error {
	if v.Repo == nil {
		return errors.New("accounting: validator has no repository")
	}

	var errs []error

	if intent.Currency == "" {
		errs = append(errs, errors.New("currency is required"))
	}

	var (
		period   Period
		periodOK bool
	)
	switch {
	case intent.PeriodID == "":
		errs = append(errs, errors.New("period_id is required"))
	default:
		p, ok, err := v.Repo.Period(ctx, intent.PeriodID)
		if err != nil {
			return fmt.Errorf("accounting: load period %q: %w", intent.PeriodID, err)
		}
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("period %q does not exist", intent.PeriodID))
		case p.Status == PeriodClosed:
			errs = append(errs, fmt.Errorf("period %q is closed and cannot accept postings", intent.PeriodID))
		default:
			period = p
			periodOK = true
		}
	}

	switch {
	case intent.Date.IsZero():
		errs = append(errs, errors.New("date is required"))
	case periodOK && intent.Date.Before(period.Start):
		errs = append(errs, fmt.Errorf("date %s is before period %q starts (%s)", intent.Date, intent.PeriodID, period.Start))
	case periodOK && intent.Date.After(period.End):
		errs = append(errs, fmt.Errorf("date %s is after period %q ends (%s)", intent.Date, intent.PeriodID, period.End))
	}

	if len(intent.Lines) < 2 {
		errs = append(errs, fmt.Errorf("journal entry must have at least two lines, got %d", len(intent.Lines)))
	}

	var debits, credits int64
	for i, line := range intent.Lines {
		label := fmt.Sprintf("line[%d]", i)

		switch line.Side {
		case SideDebit:
			debits += line.Amount
		case SideCredit:
			credits += line.Amount
		default:
			errs = append(errs, fmt.Errorf("%s: side must be %q or %q, got %q", label, SideDebit, SideCredit, line.Side))
		}

		if line.Amount <= 0 {
			errs = append(errs, fmt.Errorf("%s: amount must be positive, got %d", label, line.Amount))
		}

		if line.AccountCode == "" {
			errs = append(errs, fmt.Errorf("%s: account_code is required", label))
		} else {
			acct, ok, err := v.Repo.Account(ctx, line.AccountCode)
			if err != nil {
				return fmt.Errorf("accounting: load account %q: %w", line.AccountCode, err)
			}
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("%s: account %q is not in the chart of accounts", label, line.AccountCode))
			case !acct.Active:
				errs = append(errs, fmt.Errorf("%s: account %q is inactive and cannot be used", label, line.AccountCode))
			}
		}

		if line.Dimensions.BranchID == "" {
			errs = append(errs, fmt.Errorf("%s: branch_id is required", label))
		} else {
			_, ok, err := v.Repo.Branch(ctx, line.Dimensions.BranchID)
			if err != nil {
				return fmt.Errorf("accounting: load branch %q: %w", line.Dimensions.BranchID, err)
			}
			if !ok {
				errs = append(errs, fmt.Errorf("%s: branch %q is not a known reporting dimension", label, line.Dimensions.BranchID))
			}
		}
	}

	if len(intent.Lines) >= 2 && debits != credits {
		errs = append(errs, fmt.Errorf("debits (%d) must equal credits (%d)", debits, credits))
	}

	branchSet := map[string]struct{}{}
	for _, line := range intent.Lines {
		if line.Dimensions.BranchID != "" {
			branchSet[line.Dimensions.BranchID] = struct{}{}
		}
	}
	if len(branchSet) > 1 {
		errs = append(errs, fmt.Errorf("all lines must carry the same branch_id, got multiple values"))
	}

	return errors.Join(errs...)
}
