package accounting

import "context"

// Embedder turns text into a fixed-dimension vector for similarity search.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// AccountSearcher provides semantic search over the chart. Adapters delegate
// FindAccounts to it when AccountFilter.NameContains is set; results are
// ranked best-first and honor Type and ActiveOnly.
type AccountSearcher interface {
	Index(ctx context.Context, a Account) error
	Search(ctx context.Context, query string, filter AccountFilter, limit int) ([]Account, error)
}
