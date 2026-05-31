package accounting

import (
	"sort"
	"strings"
)

// rrfK is the reciprocal rank fusion constant from Cormack, Clarke & Büttcher
// (2009); 60 is the canonical default, not a tuned parameter.
const rrfK = 60

// FuseAccountsRRF combines per-channel ranked account lists by reciprocal rank
// fusion and returns up to limit accounts, best first. Each channel must be
// ranked best-first; a code present in several channels sums their
// contributions, so an account both lexically and semantically matched ranks
// above one matched by a single channel. The Account value is taken from the
// first channel that carries the code. A non-positive limit means no cap.
func FuseAccountsRRF(channels [][]Account, limit int) []Account {
	byCode := make(map[string]Account)
	lists := make([][]string, len(channels))
	for i, ch := range channels {
		codes := make([]string, len(ch))
		for j, a := range ch {
			codes[j] = a.Code
			if _, ok := byCode[a.Code]; !ok {
				byCode[a.Code] = a
			}
		}
		lists[i] = codes
	}
	fused := fuseRRF(lists)
	if limit > 0 && len(fused) > limit {
		fused = fused[:limit]
	}
	out := make([]Account, 0, len(fused))
	for _, code := range fused {
		out = append(out, byCode[code])
	}
	return out
}

// fuseRRF scores each code by the sum of 1/(rrfK+rank) across the lists that
// contain it (rank counted from 1) and returns codes by score, best first.
// Ties break on code so fusion is deterministic.
func fuseRRF(lists [][]string) []string {
	score := make(map[string]float64)
	for _, list := range lists {
		for rank, code := range list {
			score[code] += 1.0 / float64(rrfK+rank+1)
		}
	}
	codes := make([]string, 0, len(score))
	for code := range score {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool {
		if score[codes[i]] != score[codes[j]] {
			return score[codes[i]] > score[codes[j]]
		}
		return codes[i] < codes[j]
	})
	return codes
}

// LexicalAccountTier scores how exactly query matches an account's code or
// name for the lexical retrieval channel; a lower tier is a stronger match and
// ok is false when neither relates, so the account is not a lexical candidate.
// Matching is case-insensitive. The Postgres adapter mirrors these tiers in
// SQL -- keep the two in sync.
func LexicalAccountTier(query, code, name string) (tier int, ok bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0, false
	}
	c := strings.ToLower(code)
	n := strings.ToLower(name)
	switch {
	case q == c:
		return 0, true
	case q == n:
		return 1, true
	case strings.Contains(n, q) || strings.Contains(q, n):
		return 2, true
	case strings.Contains(q, c):
		return 3, true
	default:
		return 0, false
	}
}
