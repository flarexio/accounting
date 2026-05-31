package accounting

import (
	"context"
	"strings"
)

// AccountReranker reorders retrieval candidates by relevance to the query and
// returns at most limit accounts, best first. limit <= 0 means no cap.
type AccountReranker interface {
	Rerank(ctx context.Context, query string, candidates []Account, limit int) ([]Account, error)
}

// NewRerankedRepository wraps inner so that FindAccounts results for a semantic
// Query are reordered through reranker; every other method passes through. A
// nil reranker returns inner unchanged, so reranking is opt-in.
func NewRerankedRepository(inner LedgerRepository, reranker AccountReranker) LedgerRepository {
	if reranker == nil {
		return inner
	}
	return &rerankedRepository{LedgerRepository: inner, reranker: reranker}
}

// rerankedRepository decorates a LedgerRepository; it embeds the interface so
// only FindAccounts is overridden and the rest forward unchanged.
type rerankedRepository struct {
	LedgerRepository
	reranker AccountReranker
}

// FindAccounts reranks the inner result only when a semantic Query is present
// and there is more than one candidate to reorder. The candidates are passed
// through without a cap so reranking reorders rather than drops.
func (r *rerankedRepository) FindAccounts(ctx context.Context, filter AccountFilter) ([]Account, error) {
	out, err := r.LedgerRepository.FindAccounts(ctx, filter)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(filter.Query) == "" || len(out) <= 1 {
		return out, nil
	}
	return r.reranker.Rerank(ctx, filter.Query, out, len(out))
}
