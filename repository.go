package accounting

import "context"

// LedgerRepository is the port a persistence adapter satisfies (memory,
// postgres). It speaks only domain models; transport metadata for a projection
// write rides on the context as EventMeta. Point reads return (value, true, nil)
// when found and (zero, false, nil) for not-found.
type LedgerRepository interface {
	Account(ctx context.Context, code string) (Account, bool, error)
	Period(ctx context.Context, id string) (Period, bool, error)
	Branch(ctx context.Context, id string) (Branch, bool, error)
	Counterparty(ctx context.Context, id string) (Counterparty, bool, error)
	Entry(ctx context.Context, id string) (JournalEntry, bool, error)

	Accounts(ctx context.Context) ([]Account, error)
	Periods(ctx context.Context) ([]Period, error)
	Branches(ctx context.Context) ([]Branch, error)
	Counterparties(ctx context.Context) ([]Counterparty, error)
	Entries(ctx context.Context) ([]JournalEntry, error)

	// EntriesByPeriod returns every posted entry whose PeriodID matches.
	EntriesByPeriod(ctx context.Context, periodID string) ([]JournalEntry, error)

	// EntryCount returns the number of posted journal entries; the journal is
	// append-only, so the next entry's number is EntryCount + 1. This is the
	// dense, transport-independent basis for JournalEntry.ID, separate from the
	// per-subject stream sequence used for optimistic concurrency.
	EntryCount(ctx context.Context) (uint64, error)

	// Company returns the single company; >1 row is an error.
	Company(ctx context.Context) (Company, bool, error)

	// FindAccounts searches the chart by filter; the strategy varies by adapter.
	FindAccounts(ctx context.Context, filter AccountFilter) ([]Account, error)

	// SetCompany stores the (single) company, overwriting any prior value. It
	// never touches Policy, which has its own write path (SetPolicy).
	SetCompany(ctx context.Context, c Company) error

	// SetPolicy stores the company's policy, overwriting any prior value; an
	// absent company is an error.
	SetPolicy(ctx context.Context, policy string) error
	PutAccount(ctx context.Context, a Account) error
	// PutPeriod stores a period, overwriting any prior value.
	PutPeriod(ctx context.Context, p Period) error
	PutBranch(ctx context.Context, b Branch) error
	// PutCounterparty stores a counterparty, overwriting any prior value.
	PutCounterparty(ctx context.Context, c Counterparty) error

	// AppendEntry writes the entry, its lines, and relations in one atomic write,
	// recording the sequence from any EventMeta in the context.
	AppendEntry(ctx context.Context, entry JournalEntry, relations []JournalRelation) error

	// SetPeriodStatus transitions the period's status (an unknown id is an error),
	// advancing LastSequence from any EventMeta in the context.
	SetPeriodStatus(ctx context.Context, periodID string, status PeriodStatus) error

	// LastSequence returns the broker sequence of the most recent applied event
	// on subject, or 0 when none has been seen.
	LastSequence(ctx context.Context, subject string) (uint64, error)

	// Relation looks up a single relation row by composite identity.
	Relation(ctx context.Context, fromEntry, toEntry string) (JournalRelation, bool, error)

	// RelationsFrom returns every relation whose FromEntry equals entryID.
	RelationsFrom(ctx context.Context, entryID string) ([]JournalRelation, error)

	// RelationsTo returns every relation whose ToEntry equals entryID.
	RelationsTo(ctx context.Context, entryID string) ([]JournalRelation, error)
}

// AccountFilter narrows FindAccounts. Query is a natural-language semantic
// search over the chart; Type and ActiveOnly are exact.
type AccountFilter struct {
	Query      string
	Type       AccountType
	ActiveOnly bool
}
