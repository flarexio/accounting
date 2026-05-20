package accounting

import (
	"context"
	"sort"
	"strings"
)

// AccountFilter narrows the chart of accounts for a FindAccounts query.
// NameContains matches name and code case-insensitively; the zero filter
// matches every account.
type AccountFilter struct {
	NameContains string
	Type         AccountType
	ActiveOnly   bool
}

// AccountLister is the narrow read access FindAccounts needs;
// LedgerRepository satisfies it.
type AccountLister interface {
	Accounts(ctx context.Context) ([]Account, error)
}

// FindAccounts returns the accounts in repo's chart that match filter, ordered by code.
func FindAccounts(ctx context.Context, repo AccountLister, filter AccountFilter) ([]Account, error) {
	all, err := repo.Accounts(ctx)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(strings.TrimSpace(filter.NameContains))

	var matched []Account
	for _, a := range all {
		if filter.ActiveOnly && !a.Active {
			continue
		}
		if filter.Type != "" && a.Type != filter.Type {
			continue
		}
		if needle != "" &&
			!strings.Contains(strings.ToLower(a.Name), needle) &&
			!strings.Contains(strings.ToLower(a.Code), needle) {
			continue
		}
		matched = append(matched, a)
	}

	sort.Slice(matched, func(i, j int) bool { return matched[i].Code < matched[j].Code })
	return matched, nil
}
