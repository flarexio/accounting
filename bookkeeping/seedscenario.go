package bookkeeping

import (
	"context"
	"errors"
	"fmt"

	"github.com/flarexio/accounting"
)

// SeedScenario is the "seed reference data" use case: it publishes one event per
// entity (company, each account, each branch, each period) so the projection
// handlers upsert them. It is operator-driven (the `ledger seed` CLI), not an
// agent Intent.
type SeedScenario struct {
	Publisher Publisher
}

// Execute validates the scenario and publishes its reference data as a stream
// of per-entity events, in dependency order: company, accounts, branches, periods.
func (uc SeedScenario) Execute(ctx context.Context, scenario accounting.Scenario) error {
	if uc.Publisher == nil {
		return errors.New("bookkeeping: seed scenario has no event publisher")
	}
	if err := scenario.Validate(); err != nil {
		return err
	}

	if scenario.Company.ID != "" {
		if err := uc.publish(ctx, accounting.CompanyConfigured{Company: scenario.Company}); err != nil {
			return err
		}
	}
	for _, a := range scenario.Accounts {
		if err := uc.publish(ctx, accounting.AccountAdded{Account: a}); err != nil {
			return err
		}
	}
	for i, b := range scenario.Branches {
		if b.Position == 0 {
			b.Position = i + 1
		}
		if err := uc.publish(ctx, accounting.BranchAdded{Branch: b}); err != nil {
			return err
		}
	}
	for _, p := range scenario.Periods {
		if err := uc.publish(ctx, accounting.PeriodAdded{Period: p}); err != nil {
			return err
		}
	}
	return nil
}

func (uc SeedScenario) publish(ctx context.Context, evt Event) error {
	if err := uc.Publisher.Publish(ctx, evt, accounting.ExpectedSequence{}); err != nil {
		return fmt.Errorf("bookkeeping: publish %s: %w", evt.EventSubject(), err)
	}
	return nil
}
