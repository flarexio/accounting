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

	// EntriesByPeriod returns every posted entry whose PeriodID matches.
	EntriesByPeriod(ctx context.Context, periodID string) ([]JournalEntry, error)

	// Company returns the single company; >1 row is an error.
	Company(ctx context.Context) (Company, bool, error)

	// FindAccounts searches the chart by filter; the strategy varies by adapter.
	FindAccounts(ctx context.Context, filter AccountFilter) ([]Account, error)

	// SetCompany stores the (single) company, overwriting any prior value.
	SetCompany(ctx context.Context, c Company) error
	PutAccount(ctx context.Context, a Account) error
	PutPeriod(ctx context.Context, p Period) error
	PutBranch(ctx context.Context, b Branch) error

	// Apply writes the entry, every JournalRelation in evt.Relations, and
	// bumps LastSequence for evt.Subject atomically.
	Apply(ctx context.Context, evt JournalPosted) error

	// LastSequence returns the broker sequence of the most recent applied
	// JournalPosted on subject, or 0 when none has been seen.
	LastSequence(ctx context.Context, subject string) (uint64, error)

	// Relation looks up a single relation row by composite identity.
	Relation(ctx context.Context, fromEntry, toEntry string) (JournalRelation, bool, error)

	// RelationsFrom returns every relation whose FromEntry equals entryID.
	RelationsFrom(ctx context.Context, entryID string) ([]JournalRelation, error)

	// RelationsTo returns every relation whose ToEntry equals entryID.
	RelationsTo(ctx context.Context, entryID string) ([]JournalRelation, error)
}

// AccountFilter narrows FindAccounts. NameContains is a semantic hint; Type and ActiveOnly are exact.
type AccountFilter struct {
	NameContains string
	Type         AccountType
	ActiveOnly   bool
}
