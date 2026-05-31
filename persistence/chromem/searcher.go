// Package chromem provides an in-process AccountSearcher backed by
// chromem-go, so the in-memory ledger adapter can answer AccountFilter.Query
// searches with semantic ranking instead of dropping the hint.
package chromem

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	chromem "github.com/philippgille/chromem-go"

	"github.com/flarexio/accounting"
)

const collectionName = "accounts"

// Searcher indexes accounts in a chromem-go collection and answers FindAccounts Query searches by cosine similarity.
type Searcher struct {
	coll *chromem.Collection
}

// NewSearcher builds a Searcher whose embeddings come from embedder.
func NewSearcher(embedder accounting.Embedder) (*Searcher, error) {
	if embedder == nil {
		return nil, errors.New("chromem: NewSearcher requires a non-nil Embedder")
	}
	db := chromem.NewDB()
	coll, err := db.CreateCollection(collectionName, nil, embeddingFunc(embedder))
	if err != nil {
		return nil, fmt.Errorf("chromem: create collection: %w", err)
	}
	return &Searcher{coll: coll}, nil
}

// Index upserts a into the collection; re-indexing the same code overwrites prior state.
func (s *Searcher) Index(ctx context.Context, a accounting.Account) error {
	if a.Code == "" {
		return errors.New("chromem: Index requires a non-empty account code")
	}
	doc := chromem.Document{
		ID:      a.Code,
		Content: accounting.AccountEmbeddingText(a),
		Metadata: map[string]string{
			"code":   a.Code,
			"name":   a.Name,
			"type":   string(a.Type),
			"active": strconv.FormatBool(a.Active),
		},
	}
	if err := s.coll.AddDocument(ctx, doc); err != nil {
		return fmt.Errorf("chromem: index %q: %w", a.Code, err)
	}
	return nil
}

// Search returns up to limit accounts ranked by cosine similarity, honoring filter.Type and filter.ActiveOnly via metadata equality.
func (s *Searcher) Search(ctx context.Context, query string, filter accounting.AccountFilter, limit int) ([]accounting.Account, error) {
	if limit <= 0 {
		return nil, nil
	}
	count := s.coll.Count()
	if count == 0 {
		return nil, nil
	}
	if limit > count {
		limit = count
	}
	where := map[string]string{}
	if filter.Type != "" {
		where["type"] = string(filter.Type)
	}
	if filter.ActiveOnly {
		where["active"] = "true"
	}
	if len(where) == 0 {
		where = nil
	}
	results, err := s.coll.Query(ctx, query, limit, where, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem: query %q: %w", query, err)
	}
	out := make([]accounting.Account, 0, len(results))
	for _, r := range results {
		out = append(out, accounting.Account{
			Code:   r.Metadata["code"],
			Name:   r.Metadata["name"],
			Type:   accounting.AccountType(r.Metadata["type"]),
			Active: r.Metadata["active"] == "true",
		})
	}
	return out, nil
}

func embeddingFunc(e accounting.Embedder) chromem.EmbeddingFunc {
	return func(ctx context.Context, text string) ([]float32, error) {
		return e.Embed(ctx, text)
	}
}
