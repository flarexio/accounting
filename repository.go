package accounting

import "context"

// LedgerRepository is the port a persistence adapter satisfies (memory,
// postgres). Apply is the only mutation path for journal state -- producers
// publish a JournalPosted and a subscribed handler invokes Apply. Point reads
// return (value, true, nil) when found and (zero, false, nil) for not-found.
type LedgerRepository interface {
	Account(ctx context.Context, code string) (Account, bool, error)
	Period(ctx context.Context, id string) (Period, bool, error)
	Branch(ctx context.Context, id string) (Branch, bool, error)
	Entry(ctx context.Context, id string) (JournalEntry, bool, error)

	Accounts(ctx context.Context) ([]Account, error)
	Periods(ctx context.Context) ([]Period, error)
	Branches(ctx context.Context) ([]Branch, error)
	Entries(ctx context.Context) ([]JournalEntry, error)

	// FindAccounts searches the chart by filter. Adapters choose the strategy:
	// substring match, vector similarity, etc.
	FindAccounts(ctx context.Context, filter AccountFilter) ([]Account, error)

	PutAccount(ctx context.Context, a Account) error
	PutPeriod(ctx context.Context, p Period) error
	PutBranch(ctx context.Context, b Branch) error

	// Apply writes the entry and bumps LastSequence for evt.Subject atomically.
	Apply(ctx context.Context, evt JournalPosted) error

	// LastSequence returns the broker sequence of the most recent applied
	// JournalPosted on subject, or 0 when none has been seen.
	LastSequence(ctx context.Context, subject string) (uint64, error)
}

// AccountFilter narrows the chart of accounts for a FindAccounts query.
// NameContains is a semantic hint -- adapters may match it via substring,
// vector similarity, or any other strategy. Type and ActiveOnly are exact.
type AccountFilter struct {
	NameContains string
	Type         AccountType
	ActiveOnly   bool
}
