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

		if cp := line.Dimensions.CounterpartyID; cp != "" {
			c, ok, err := v.Repo.Counterparty(ctx, cp)
			if err != nil {
				return fmt.Errorf("accounting: load counterparty %q: %w", cp, err)
			}
			switch {
			case !ok:
				errs = append(errs, fmt.Errorf("%s: counterparty %q does not exist", label, cp))
			case !c.Active:
				errs = append(errs, fmt.Errorf("%s: counterparty %q is inactive and cannot be used", label, cp))
			}
		}
	}

	if len(intent.Lines) >= 2 && debits != credits {
		errs = append(errs, fmt.Errorf("debits (%d) must equal credits (%d)", debits, credits))
	}

	branchSet := map[string]struct{}{}
	cpSet := map[string]struct{}{}
	for _, line := range intent.Lines {
		if line.Dimensions.BranchID != "" {
			branchSet[line.Dimensions.BranchID] = struct{}{}
		}
		if line.Dimensions.CounterpartyID != "" {
			cpSet[line.Dimensions.CounterpartyID] = struct{}{}
		}
	}
	if len(branchSet) > 1 {
		errs = append(errs, fmt.Errorf("all lines must carry the same branch_id, got multiple values"))
	}
	if len(cpSet) > 1 {
		errs = append(errs, fmt.Errorf("all lines must reference the same counterparty_id, got multiple values"))
	}

	if intent.Source != nil {
		switch intent.Source.Kind {
		case SourceInvoice, SourceBill, SourceReceipt:
		case "":
			errs = append(errs, errors.New("source.kind is required when a source document is set"))
		default:
			errs = append(errs, fmt.Errorf("source.kind %q is not a known document kind", intent.Source.Kind))
		}
	}

	return errors.Join(errs...)
}

// ValidateRelation enforces relation invariants for rel. fromEntry is the
// source entry the relation links from; it is passed separately because it
// may not yet be persisted at validation time (e.g. a reversal is validated
// before its own entry has been applied to the projection).
func (v Validator) ValidateRelation(ctx context.Context, rel JournalRelation, fromEntry JournalEntry) error {
	if v.Repo == nil {
		return errors.New("accounting: validator has no repository")
	}
	if fromEntry.ID == "" || fromEntry.ID != rel.FromEntry {
		return fmt.Errorf("accounting: from_entry %q does not match provided entry %q", rel.FromEntry, fromEntry.ID)
	}

	var errs []error

	switch rel.Type {
	case RelationReverses, RelationCorrects, RelationSettles, RelationCloses, RelationAdjusts:
	case "":
		errs = append(errs, errors.New("type is required"))
	default:
		errs = append(errs, fmt.Errorf("type %q is not a known relation kind", rel.Type))
	}

	if rel.ToEntry == "" {
		errs = append(errs, errors.New("to_entry is required"))
		return errors.Join(errs...)
	}
	if rel.FromEntry == rel.ToEntry {
		errs = append(errs, errors.New("from_entry must differ from to_entry"))
	}

	to, ok, err := v.Repo.Entry(ctx, rel.ToEntry)
	if err != nil {
		return fmt.Errorf("accounting: load to_entry %q: %w", rel.ToEntry, err)
	}
	if !ok {
		errs = append(errs, fmt.Errorf("to_entry %q is not in the ledger", rel.ToEntry))
		return errors.Join(errs...)
	}

	if fromEntry.PostedAt.Before(to.PostedAt) {
		errs = append(errs, fmt.Errorf("from_entry posted %s precedes to_entry posted %s", fromEntry.PostedAt, to.PostedAt))
	}

	if rel.Type == RelationReverses {
		if err := validateReversalMirror(fromEntry, to); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateReversalMirror checks that from's lines are the line-by-line mirror
// of to's lines: same account, amount, and branch_id, with the side flipped.
func validateReversalMirror(from, to JournalEntry) error {
	if len(from.Lines) != len(to.Lines) {
		return fmt.Errorf("expected %d mirror lines, got %d", len(to.Lines), len(from.Lines))
	}
	for i, fl := range from.Lines {
		tl := to.Lines[i]
		switch {
		case fl.AccountCode != tl.AccountCode:
			return fmt.Errorf("line[%d]: account_code mismatch (%q vs %q)", i, fl.AccountCode, tl.AccountCode)
		case fl.Amount != tl.Amount:
			return fmt.Errorf("line[%d]: amount mismatch (%d vs %d)", i, fl.Amount, tl.Amount)
		case fl.Dimensions.BranchID != tl.Dimensions.BranchID:
			return fmt.Errorf("line[%d]: branch_id mismatch (%q vs %q)", i, fl.Dimensions.BranchID, tl.Dimensions.BranchID)
		case fl.Side == tl.Side:
			return fmt.Errorf("line[%d]: side not flipped (both %q)", i, fl.Side)
		}
	}
	return nil
}
