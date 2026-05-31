package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

type fakeSearcher struct {
	indexed []accounting.Account
	want    string
	results []accounting.Account
}

func (f *fakeSearcher) Index(_ context.Context, a accounting.Account) error {
	f.indexed = append(f.indexed, a)
	return nil
}

func (f *fakeSearcher) Search(_ context.Context, query string, _ accounting.AccountFilter, _ int) ([]accounting.Account, error) {
	if f.want != "" && query != f.want {
		return nil, errors.New("unexpected query")
	}
	return f.results, nil
}

func TestRepository_WithSearcher_IndexesEachPutAccount(t *testing.T) {
	s := &fakeSearcher{}
	repo := memory.NewAccountingRepository(memory.WithSearcher(s))
	for _, a := range []accounting.Account{
		{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
	} {
		if err := repo.PutAccount(context.Background(), a); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	if len(s.indexed) != 2 || s.indexed[0].Code != "1010" || s.indexed[1].Code != "2100" {
		t.Fatalf("expected two indexed accounts in order, got %+v", s.indexed)
	}
}

func TestRepository_WithoutSearcher_DoesNotIndex(t *testing.T) {
	repo := memory.NewAccountingRepository()
	if err := repo.PutAccount(context.Background(), accounting.Account{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true}); err != nil {
		t.Fatalf("put: %v", err)
	}
}

func TestRepository_FindAccounts_DelegatesWhenQuerySet(t *testing.T) {
	s := &fakeSearcher{
		want:    "cash",
		results: []accounting.Account{{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true}},
	}
	repo := memory.NewAccountingRepository(memory.WithSearcher(s))
	got, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{Query: "cash"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 || got[0].Code != "1010" {
		t.Fatalf("expected searcher result, got %+v", got)
	}
}

func TestRepository_FindAccounts_HybridPromotesLexicalHit(t *testing.T) {
	// Dense ranks Credit Card Payable above Cash; an exact-name lexical hit on
	// "Cash" should fuse high enough to overtake it.
	s := &fakeSearcher{
		want: "Cash",
		results: []accounting.Account{
			{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
			{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		},
	}
	repo := memory.NewAccountingRepository(memory.WithSearcher(s))
	for _, a := range []accounting.Account{
		{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
	} {
		if err := repo.PutAccount(context.Background(), a); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	got, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{Query: "Cash"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both accounts fused, got %+v", got)
	}
	if got[0].Code != "1010" {
		t.Fatalf("expected lexical hit 1010 promoted to first, got %+v", got)
	}
}

func TestRepository_FindAccounts_SkipsSearcherWhenQueryEmpty(t *testing.T) {
	s := &fakeSearcher{want: "should-not-be-called"}
	repo := memory.NewAccountingRepository(memory.WithSearcher(s))
	if err := repo.PutAccount(context.Background(), accounting.Account{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true}); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected fallback path result, got %+v", got)
	}
}
