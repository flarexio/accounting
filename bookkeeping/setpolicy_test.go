package bookkeeping_test

import (
	"context"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
	"github.com/flarexio/accounting/messaging/inproc"
	"github.com/flarexio/accounting/persistence/memory"
)

func policyBus(t *testing.T, repo accounting.LedgerRepository) bookkeeping.EventBus {
	t.Helper()
	bus := inproc.NewAccountingBus()
	router := bookkeeping.NewRouter().
		On(accounting.SubjectPolicySet, &bookkeeping.ApplyPolicy{Repo: repo})
	if err := bus.Subscribe(router); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return bus
}

func TestSetPolicy_ProjectsThroughEvent(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	if err := repo.SetCompany(ctx, accounting.Company{ID: "acme", Name: "Acme"}); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	bus := policyBus(t, repo)

	const policy = "- 交際費 vs 廣告費: 對特定客戶的餽贈走交際費。"
	if err := (bookkeeping.SetPolicy{Publisher: bus}).Execute(ctx, "  "+policy+"\n"); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	company, _, err := repo.Company(ctx)
	if err != nil {
		t.Fatalf("load company: %v", err)
	}
	if company.Policy != policy {
		t.Errorf("policy not projected (or not trimmed): %q", company.Policy)
	}
}

func TestSetCompany_DoesNotClobberPolicy(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	if err := repo.SetCompany(ctx, accounting.Company{ID: "acme", Name: "Acme"}); err != nil {
		t.Fatalf("seed company: %v", err)
	}
	if err := repo.SetPolicy(ctx, "house rules"); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// A re-seed re-applies CompanyConfigured (no policy field); it must not wipe it.
	if err := repo.SetCompany(ctx, accounting.Company{ID: "acme", Name: "Acme Renamed"}); err != nil {
		t.Fatalf("re-seed company: %v", err)
	}

	company, _, err := repo.Company(ctx)
	if err != nil {
		t.Fatalf("load company: %v", err)
	}
	if company.Policy != "house rules" {
		t.Errorf("re-seed clobbered policy: %q", company.Policy)
	}
	if company.Name != "Acme Renamed" {
		t.Errorf("re-seed should still update other fields, name=%q", company.Name)
	}
}

func TestSetPolicy_NoCompanyIsError(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewAccountingRepository()
	bus := policyBus(t, repo)
	if err := (bookkeeping.SetPolicy{Publisher: bus}).Execute(ctx, "rules"); err == nil {
		t.Fatal("expected error setting policy with no company configured")
	}
}
