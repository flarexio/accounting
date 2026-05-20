package accounting_test

import (
	"context"
	"testing"

	"github.com/flarexio/accounting"
)

type accountSlice []accounting.Account

func (s accountSlice) Accounts(context.Context) ([]accounting.Account, error) {
	return s, nil
}

func TestFindAccounts(t *testing.T) {
	chart := accountSlice{
		{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
		{Code: "5400", Name: "Office Rent Expense", Type: accounting.AccountExpense, Active: true},
		{Code: "5900", Name: "Legacy Office Rent", Type: accounting.AccountExpense, Active: false},
	}
	ctx := context.Background()

	t.Run("name substring is case-insensitive and sorted by code", func(t *testing.T) {
		got, err := accounting.FindAccounts(ctx, chart, accounting.AccountFilter{NameContains: "RENT"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Code != "5400" || got[1].Code != "5900" {
			t.Fatalf("got %+v, want 5400 then 5900", got)
		}
	})

	t.Run("ActiveOnly drops inactive accounts", func(t *testing.T) {
		got, _ := accounting.FindAccounts(ctx, chart, accounting.AccountFilter{NameContains: "rent", ActiveOnly: true})
		if len(got) != 1 || got[0].Code != "5400" {
			t.Fatalf("got %+v, want only 5400", got)
		}
	})

	t.Run("Type narrows to one account type", func(t *testing.T) {
		got, _ := accounting.FindAccounts(ctx, chart, accounting.AccountFilter{Type: accounting.AccountLiability})
		if len(got) != 1 || got[0].Code != "2100" {
			t.Fatalf("got %+v, want only 2100", got)
		}
	})

	t.Run("substring also matches the account code", func(t *testing.T) {
		got, _ := accounting.FindAccounts(ctx, chart, accounting.AccountFilter{NameContains: "21"})
		if len(got) != 1 || got[0].Code != "2100" {
			t.Fatalf("got %+v, want only 2100", got)
		}
	})

	t.Run("zero filter matches the whole chart", func(t *testing.T) {
		got, _ := accounting.FindAccounts(ctx, chart, accounting.AccountFilter{})
		if len(got) != 4 {
			t.Fatalf("got %d accounts, want 4", len(got))
		}
	})
}
