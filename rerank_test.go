package accounting_test

import (
	"context"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/memory"
)

type reverseReranker struct{ calls int }

func (r *reverseReranker) Rerank(_ context.Context, _ string, candidates []accounting.Account, _ int) ([]accounting.Account, error) {
	r.calls++
	out := make([]accounting.Account, len(candidates))
	for i, a := range candidates {
		out[len(candidates)-1-i] = a
	}
	return out, nil
}

func rerankInnerRepo(t *testing.T, codes ...string) accounting.LedgerRepository {
	t.Helper()
	repo := memory.NewAccountingRepository()
	for _, c := range codes {
		if err := repo.PutAccount(context.Background(), accounting.Account{Code: c, Name: c, Type: accounting.AccountAsset, Active: true}); err != nil {
			t.Fatalf("put %s: %v", c, err)
		}
	}
	return repo
}

func TestRerankedRepository_ReordersWhenQuerySet(t *testing.T) {
	rr := &reverseReranker{}
	repo := accounting.NewRerankedRepository(rerankInnerRepo(t, "1010", "2020", "3030"), rr)
	got, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{Query: "anything"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	// Inner returns codes sorted ascending; the reranker reverses them.
	if want := []string{"3030", "2020", "1010"}; !equal(codes(got), want) {
		t.Errorf("got %v, want %v", codes(got), want)
	}
	if rr.calls != 1 {
		t.Errorf("expected reranker called once, got %d", rr.calls)
	}
}

func TestRerankedRepository_SkipsRerankWithoutQuery(t *testing.T) {
	rr := &reverseReranker{}
	repo := accounting.NewRerankedRepository(rerankInnerRepo(t, "1010", "2020"), rr)
	got, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if want := []string{"1010", "2020"}; !equal(codes(got), want) {
		t.Errorf("got %v, want %v", codes(got), want)
	}
	if rr.calls != 0 {
		t.Errorf("reranker should not run without a query, got %d calls", rr.calls)
	}
}

func TestRerankedRepository_SkipsRerankForSingleCandidate(t *testing.T) {
	rr := &reverseReranker{}
	repo := accounting.NewRerankedRepository(rerankInnerRepo(t, "1010"), rr)
	if _, err := repo.FindAccounts(context.Background(), accounting.AccountFilter{Query: "x"}); err != nil {
		t.Fatalf("find: %v", err)
	}
	if rr.calls != 0 {
		t.Errorf("a single candidate needs no reranking, got %d calls", rr.calls)
	}
}

func TestNewRerankedRepository_NilRerankerReturnsInner(t *testing.T) {
	inner := rerankInnerRepo(t, "1010")
	if got := accounting.NewRerankedRepository(inner, nil); got != inner {
		t.Error("a nil reranker should return the inner repository unchanged")
	}
}
