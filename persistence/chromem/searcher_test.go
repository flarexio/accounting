package chromem_test

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/chromem"
)

const stubDim = 128

// stubEmbedder bag-of-words hashes each whitespace token into a unit-norm vector, so cosine similarity reduces to token overlap.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vec := make([]float32, stubDim)
	for word := range strings.FieldsSeq(strings.ToLower(text)) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		vec[h.Sum32()%stubDim] += 1
	}
	var sumSquares float32
	for _, v := range vec {
		sumSquares += v * v
	}
	if sumSquares == 0 {
		return vec, nil
	}
	norm := float32(math.Sqrt(float64(sumSquares)))
	for i := range vec {
		vec[i] /= norm
	}
	return vec, nil
}

func mustSearcher(t *testing.T) *chromem.Searcher {
	t.Helper()
	s, err := chromem.NewSearcher(stubEmbedder{})
	if err != nil {
		t.Fatalf("new searcher: %v", err)
	}
	return s
}

func TestSearcher_EmptyCollectionReturnsNil(t *testing.T) {
	s := mustSearcher(t)
	got, err := s.Search(context.Background(), "cash", accounting.AccountFilter{}, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil on empty collection, got %+v", got)
	}
}

func TestSearcher_RanksByTokenOverlap(t *testing.T) {
	s := mustSearcher(t)
	ctx := context.Background()
	for _, a := range []accounting.Account{
		{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		{Code: "2100", Name: "Credit Card Payable", Type: accounting.AccountLiability, Active: true},
		{Code: "5200", Name: "Cloud Hosting Expense", Type: accounting.AccountExpense, Active: true},
	} {
		if err := s.Index(ctx, a); err != nil {
			t.Fatalf("index %s: %v", a.Code, err)
		}
	}
	got, err := s.Search(ctx, "cash", accounting.AccountFilter{}, 3)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) == 0 || got[0].Code != "1010" {
		t.Fatalf("expected Cash to rank first, got %+v", got)
	}
}

func TestSearcher_FiltersByTypeAndActive(t *testing.T) {
	s := mustSearcher(t)
	ctx := context.Background()
	for _, a := range []accounting.Account{
		{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true},
		{Code: "5900", Name: "Legacy Office Rent", Type: accounting.AccountExpense, Active: false},
		{Code: "5200", Name: "Cloud Hosting Expense", Type: accounting.AccountExpense, Active: true},
	} {
		if err := s.Index(ctx, a); err != nil {
			t.Fatalf("index %s: %v", a.Code, err)
		}
	}

	got, err := s.Search(ctx, "expense", accounting.AccountFilter{Type: accounting.AccountExpense, ActiveOnly: true}, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, a := range got {
		if a.Type != accounting.AccountExpense {
			t.Fatalf("type filter leaked: %+v", a)
		}
		if !a.Active {
			t.Fatalf("active filter leaked: %+v", a)
		}
	}
	if !containsCode(got, "5200") {
		t.Fatalf("expected 5200 in filtered results, got %+v", got)
	}
	if containsCode(got, "5900") {
		t.Fatalf("inactive 5900 should be filtered, got %+v", got)
	}
	if containsCode(got, "1010") {
		t.Fatalf("non-expense 1010 should be filtered, got %+v", got)
	}
}

func TestSearcher_LimitCappedAtCollectionSize(t *testing.T) {
	s := mustSearcher(t)
	ctx := context.Background()
	if err := s.Index(ctx, accounting.Account{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true}); err != nil {
		t.Fatalf("index: %v", err)
	}
	got, err := s.Search(ctx, "cash", accounting.AccountFilter{}, 100)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
}

func TestSearcher_ReindexOverwritesPrior(t *testing.T) {
	s := mustSearcher(t)
	ctx := context.Background()
	a := accounting.Account{Code: "1010", Name: "Cash", Type: accounting.AccountAsset, Active: true}
	if err := s.Index(ctx, a); err != nil {
		t.Fatalf("index v1: %v", err)
	}
	a.Active = false
	if err := s.Index(ctx, a); err != nil {
		t.Fatalf("index v2: %v", err)
	}
	got, err := s.Search(ctx, "cash", accounting.AccountFilter{ActiveOnly: true}, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected inactive Cash to be filtered out after re-index, got %+v", got)
	}
}

func containsCode(accs []accounting.Account, code string) bool {
	for _, a := range accs {
		if a.Code == code {
			return true
		}
	}
	return false
}
