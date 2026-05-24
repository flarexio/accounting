package accounting_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

// awsBillRepo: April 2026 closed, May 2026 open, account 5900 inactive.
func awsBillRepo(t *testing.T) accounting.LedgerRepository {
	t.Helper()
	scenario, err := accounting.LoadScenarioFile("testdata/aws_bill.json")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	repo := memory.NewAccountingRepository()
	if err := scenario.Seed(context.Background(), repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return repo
}

func balancedAWSIntent() accounting.JournalIntent {
	return accounting.JournalIntent{
		Date:        time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		PeriodID:    "2026-05",
		Currency:    "USD",
		Description: "Paid AWS bill on company credit card",
		Lines: []accounting.JournalLine{
			{
				AccountCode: "5200",
				Side:        accounting.SideDebit,
				Amount:      10000,
				Memo:        "AWS monthly invoice",
				Dimensions:  accounting.Dimensions{BranchID: "hq"},
			},
			{
				AccountCode: "2100",
				Side:        accounting.SideCredit,
				Amount:      10000,
				Memo:        "Charged to Visa",
				Dimensions:  accounting.Dimensions{BranchID: "hq"},
			},
		},
	}
}

func TestValidator_BalancedAWSBill(t *testing.T) {
	repo := awsBillRepo(t)
	v := accounting.Validator{Repo: repo}
	if err := v.Validate(context.Background(), balancedAWSIntent()); err != nil {
		t.Fatalf("expected balanced AWS bill to pass, got %v", err)
	}
}

func TestValidator_RejectsUnbalanced(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[1].Amount = 9000
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "debits") {
		t.Fatalf("expected unbalanced error, got %v", err)
	}
}

func TestValidator_RejectsSingleLine(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines = intent.Lines[:1]
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "at least two lines") {
		t.Fatalf("expected at-least-two-lines error, got %v", err)
	}
}

func TestValidator_RejectsClosedPeriod(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.PeriodID = "2026-04"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed-period error, got %v", err)
	}
}

func TestValidator_RejectsUnknownPeriod(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.PeriodID = "1999-12"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected unknown-period error, got %v", err)
	}
}

func TestValidator_RejectsZeroDate(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Date = time.Time{}
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "date is required") {
		t.Fatalf("expected zero-date error, got %v", err)
	}
}

func TestValidator_RejectsDateBeforePeriod(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Date = time.Date(2026, 4, 30, 23, 0, 0, 0, time.UTC)
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "before period") {
		t.Fatalf("expected date-before-period error, got %v", err)
	}
}

func TestValidator_RejectsDateAfterPeriod(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Date = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "after period") {
		t.Fatalf("expected date-after-period error, got %v", err)
	}
}

func TestValidator_RejectsInactiveAccount(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[0].AccountCode = "5900"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "inactive") {
		t.Fatalf("expected inactive-account error, got %v", err)
	}
}

func TestValidator_RejectsUnknownAccount(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[0].AccountCode = "9999"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "chart of accounts") {
		t.Fatalf("expected unknown-account error, got %v", err)
	}
}

func TestValidator_RejectsUnknownBranch(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[0].Dimensions.BranchID = "atlantis"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatalf("expected unknown-branch error, got %v", err)
	}
}

func TestValidator_RejectsNonPositiveAmount(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[0].Amount = 0
	intent.Lines[1].Amount = 0
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("expected positive-amount error, got %v", err)
	}
}

func TestValidator_RejectsInvalidSide(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[0].Side = "sideways"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "side") {
		t.Fatalf("expected invalid-side error, got %v", err)
	}
}

func TestValidator_RejectsMissingCurrency(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Currency = ""
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "currency") {
		t.Fatalf("expected currency error, got %v", err)
	}
}

func TestValidator_RejectsInconsistentBranchID(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[1].Dimensions.BranchID = "" // line[0] has "hq", line[1] has none
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("expected branch_id consistency error, got %v", err)
	}
}

func TestValidator_RejectsMixedBranchIDs(t *testing.T) {
	repo := awsBillRepo(t)
	intent := balancedAWSIntent()
	intent.Lines[1].Dimensions.BranchID = "tc" // line[0]="hq", line[1]="tc"
	err := accounting.Validator{Repo: repo}.Validate(context.Background(), intent)
	if err == nil || !strings.Contains(err.Error(), "branch_id") {
		t.Fatalf("expected mixed branch_id error, got %v", err)
	}
}

func TestValidator_NilRepo(t *testing.T) {
	err := accounting.Validator{}.Validate(context.Background(), balancedAWSIntent())
	if err == nil {
		t.Fatal("expected error when validator has no repository")
	}
}

func TestScenarioLoader_SeedsRepository(t *testing.T) {
	ctx := context.Background()
	scenario, err := accounting.LoadScenarioFile("testdata/aws_bill.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if scenario.Company.ID != "acme" {
		t.Fatalf("unexpected company: %+v", scenario.Company)
	}
	repo := memory.NewAccountingRepository()
	if err := scenario.Seed(ctx, repo); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, ok, _ := repo.Account(ctx, "5200"); !ok {
		t.Fatal("expected cloud hosting account in chart of accounts")
	}
	if _, ok, _ := repo.Branch(ctx, "hq"); !ok {
		t.Fatal("expected hq branch")
	}
	if p, ok, _ := repo.Period(ctx, "2026-04"); !ok || p.Status != accounting.PeriodClosed {
		t.Fatalf("expected closed April period, got %+v ok=%v", p, ok)
	}
}
