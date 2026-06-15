package bookkeeping_test

import (
	"context"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
)

func TestSeedScenario_ProjectsEveryEntityViaEvents(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectCompanyConfigured, &bookkeeping.ApplyCompany{Repo: repo}).
		On(accounting.SubjectAccountAdded, &bookkeeping.ApplyAccount{Repo: repo}).
		On(accounting.SubjectBranchAdded, &bookkeeping.ApplyBranch{Repo: repo}).
		On(accounting.SubjectPeriodAdded, &bookkeeping.ApplyPeriod{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	scn := accounting.Scenario{
		Company:  accounting.Company{ID: "acme", Name: "Acme Co.", TimeZone: "UTC"},
		Accounts: []accounting.Account{{Code: "1000", Name: "Cash", Type: accounting.AccountAsset, Active: true}},
		Branches: []accounting.Branch{{ID: "main", Name: "Main"}}, // Position 0 -> defaulted to 1
		Periods:  []accounting.Period{{ID: "2026-05", Start: accounting.NewDate(2026, 5, 1), End: accounting.NewDate(2026, 5, 31), Status: accounting.PeriodOpen}},
	}

	// inproc dispatches synchronously, so the projection is complete on return.
	if err := (bookkeeping.SeedScenario{Publisher: bus}).Execute(ctx, scn); err != nil {
		t.Fatalf("seed scenario: %v", err)
	}

	company, ok, err := repo.Company(ctx)
	if err != nil || !ok || company.ID != "acme" {
		t.Fatalf("company not projected: ok=%v err=%v %+v", ok, err, company)
	}
	if accounts, _ := repo.Accounts(ctx); len(accounts) != 1 || accounts[0].Code != "1000" {
		t.Fatalf("accounts not projected: %+v", accounts)
	}
	branches, _ := repo.Branches(ctx)
	if len(branches) != 1 || branches[0].Position != 1 {
		t.Fatalf("branch not projected with defaulted position: %+v", branches)
	}
	if periods, _ := repo.Periods(ctx); len(periods) != 1 || periods[0].ID != "2026-05" {
		t.Fatalf("period not projected: %+v", periods)
	}
}

// Counterparties are not seeded reference data; they are created by publishing
// CounterpartyAdded, which ApplyCounterparty projects.
func TestApplyCounterparty_Projects(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectCounterpartyAdded, &bookkeeping.ApplyCounterparty{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	cp := accounting.Counterparty{ID: "CP-0001", Name: "TSMC", Kind: accounting.CounterpartyCustomer, Active: true}
	if err := bus.Publish(ctx, accounting.CounterpartyAdded{Counterparty: cp}, accounting.ExpectedSequence{}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	got, ok, err := repo.Counterparty(ctx, "CP-0001")
	if err != nil || !ok || got.Name != "TSMC" {
		t.Fatalf("counterparty not projected: ok=%v err=%v %+v", ok, err, got)
	}
}
