package bookkeeping

import (
	"context"
	"fmt"

	"github.com/flarexio/accounting"
)

// ApplyCompany projects accounting.CompanyConfigured by upserting the company.
type ApplyCompany struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyCompany) Handle(ctx context.Context, evt Event) error {
	e, ok := evt.(accounting.CompanyConfigured)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyCompany received %T on subject %q, want CompanyConfigured", evt, evt.EventSubject())
	}
	return h.Repo.SetCompany(ctx, e.Company)
}

// ApplyAccount projects accounting.AccountAdded by upserting the chart account.
type ApplyAccount struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyAccount) Handle(ctx context.Context, evt Event) error {
	e, ok := evt.(accounting.AccountAdded)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyAccount received %T on subject %q, want AccountAdded", evt, evt.EventSubject())
	}
	return h.Repo.PutAccount(ctx, e.Account)
}

// ApplyBranch projects accounting.BranchAdded by upserting the reporting branch.
type ApplyBranch struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyBranch) Handle(ctx context.Context, evt Event) error {
	e, ok := evt.(accounting.BranchAdded)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyBranch received %T on subject %q, want BranchAdded", evt, evt.EventSubject())
	}
	return h.Repo.PutBranch(ctx, e.Branch)
}

// ApplyPeriod projects accounting.PeriodAdded by upserting the accounting period.
type ApplyPeriod struct {
	Repo accounting.LedgerRepository
}

// Handle implements bookkeeping.EventHandler.
func (h *ApplyPeriod) Handle(ctx context.Context, evt Event) error {
	e, ok := evt.(accounting.PeriodAdded)
	if !ok {
		return fmt.Errorf("bookkeeping: ApplyPeriod received %T on subject %q, want PeriodAdded", evt, evt.EventSubject())
	}
	return h.Repo.PutPeriod(ctx, e.Period)
}
