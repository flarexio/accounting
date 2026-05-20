// Package accounting is the bookkeeping domain: the ledger model and the
// rules a proposed JournalIntent must satisfy before it can be posted.
// It depends on no LLM, harness, transport, or CLI code.
//
// A posted JournalEntry is immutable; corrections post a new reversing entry.
// This is a double-entry invariant required by SOX, GAAP, and IFRS, so the
// package exposes only one entry-affecting event, JournalPosted.
package accounting

import "time"

// LineSide is "debit" or "credit" on a JournalLine.
type LineSide string

const (
	SideDebit  LineSide = "debit"
	SideCredit LineSide = "credit"
)

// AccountType classifies an Account on the chart of accounts.
type AccountType string

const (
	AccountAsset     AccountType = "asset"
	AccountLiability AccountType = "liability"
	AccountEquity    AccountType = "equity"
	AccountRevenue   AccountType = "revenue"
	AccountExpense   AccountType = "expense"
)

// PeriodStatus is "open" or "closed"; a closed period rejects new postings.
type PeriodStatus string

const (
	PeriodOpen   PeriodStatus = "open"
	PeriodClosed PeriodStatus = "closed"
)

// Company is the legal entity that owns the ledger.
type Company struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// Account is one row in the chart of accounts. Inactive accounts cannot be
// used in new postings.
type Account struct {
	Code   string      `json:"code" yaml:"code"`
	Name   string      `json:"name" yaml:"name"`
	Type   AccountType `json:"type" yaml:"type"`
	Active bool        `json:"active" yaml:"active"`
}

// Branch is a reporting dimension within the single ledger.
type Branch struct {
	ID   string `json:"id" yaml:"id"`
	Name string `json:"name" yaml:"name"`
}

// Period is an accounting period. Closed periods cannot accept postings.
type Period struct {
	ID     string       `json:"id" yaml:"id"`
	Start  time.Time    `json:"start" yaml:"start"`
	End    time.Time    `json:"end" yaml:"end"`
	Status PeriodStatus `json:"status" yaml:"status"`
}

// Dimensions tag a journal line with reporting cuts.
type Dimensions struct {
	BranchID string            `json:"branch_id,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

// JournalLine is one debit or credit on a journal entry. Amount is in minor
// units of Currency (e.g. cents) so balance checks never rely on floating point.
type JournalLine struct {
	AccountCode string     `json:"account_code"`
	Side        LineSide   `json:"side"`
	Amount      int64      `json:"amount"`
	Memo        string     `json:"memo,omitempty"`
	Dimensions  Dimensions `json:"dimensions"`
}

// JournalIntent is a proposed transaction; it must clear Validator before posting.
type JournalIntent struct {
	Date        time.Time     `json:"date"`
	PeriodID    string        `json:"period_id"`
	Currency    string        `json:"currency"`
	Description string        `json:"description"`
	Lines       []JournalLine `json:"lines"`
}

// JournalEntry is a posted, sealed accounting entry. Entries are immutable;
// corrections go through new reversing entries.
type JournalEntry struct {
	ID          string        `json:"id"`
	Date        time.Time     `json:"date"`
	PeriodID    string        `json:"period_id"`
	Currency    string        `json:"currency"`
	Description string        `json:"description"`
	Lines       []JournalLine `json:"lines"`
	PostedAt    time.Time     `json:"posted_at"`
}
