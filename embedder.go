package accounting

import (
	"context"
	"strings"
)

// Embedder turns text into a fixed-dimension vector for similarity search.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// AccountSearcher provides semantic search over the chart. Adapters delegate
// FindAccounts to it when AccountFilter.Query is set; results are ranked
// best-first and honor Type and ActiveOnly.
type AccountSearcher interface {
	Index(ctx context.Context, a Account) error
	Search(ctx context.Context, query string, filter AccountFilter, limit int) ([]Account, error)
}

// AccountEmbeddingText builds the natural-language text indexed for semantic
// account search: name, then Description and Aliases when present. The code is
// deliberately excluded so its digits do not dilute the semantic vector --
// code lookups are exact (Account) or a future lexical channel, not semantic.
func AccountEmbeddingText(a Account) string {
	parts := make([]string, 0, 2+len(a.Aliases))
	parts = append(parts, a.Name)
	if a.Description != "" {
		parts = append(parts, a.Description)
	}
	parts = append(parts, a.Aliases...)
	return strings.Join(parts, " ")
}
