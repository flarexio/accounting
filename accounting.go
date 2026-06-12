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

// Company is the legal entity that owns the ledger. RetainedEarningsCode names
// the equity account ClosePeriod plugs net income into; empty disables closing.
// Policy is operator-authored bookkeeping guidance with its own write path
// (SetPolicy), so seed (yaml:"-") never sets or clobbers it.
type Company struct {
	ID                   string `json:"id" yaml:"id"`
	Name                 string `json:"name" yaml:"name"`
	TimeZone             string `json:"timezone" yaml:"timezone"`
	RetainedEarningsCode string `json:"retained_earnings_code,omitempty" yaml:"retained_earnings_code,omitempty"`
	Policy               string `json:"policy,omitempty" yaml:"-"`
}

// Location parses the IANA name in TimeZone; returns time.UTC if empty or invalid.
func (c Company) Location() *time.Location {
	if c.TimeZone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Account is one row in the chart of accounts. Inactive accounts cannot be
// used in new postings. Aliases and Description are semantic-search hints that
// enrich the indexed text (see AccountEmbeddingText); they carry no posting
// invariant and the projection persists only the resulting embedding.
type Account struct {
	Code        string      `json:"code" yaml:"code"`
	Name        string      `json:"name" yaml:"name"`
	Type        AccountType `json:"type" yaml:"type"`
	Active      bool        `json:"active" yaml:"active"`
	Aliases     []string    `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`
}

// Branch is a reporting dimension within the single ledger. Position drives
// display order; Scenario.Seed fills it from the seed file's array index when
// callers leave it zero.
type Branch struct {
	ID       string `json:"id" yaml:"id"`
	Name     string `json:"name" yaml:"name"`
	Position int    `json:"position,omitempty" yaml:"position,omitempty"`
}

// Period is an accounting period. Closed periods cannot accept postings.
// Start and End are calendar dates in the company's timezone; End is inclusive.
type Period struct {
	ID     string       `json:"id" yaml:"id"`
	Start  Date         `json:"start" yaml:"start"`
	End    Date         `json:"end" yaml:"end"`
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

// JournalIntent is a proposed transaction; it must clear Validator before
// posting. Date is the business date in the company's timezone.
type JournalIntent struct {
	Date        Date          `json:"date"`
	PeriodID    string        `json:"period_id"`
	Currency    string        `json:"currency"`
	Description string        `json:"description"`
	Lines       []JournalLine `json:"lines"`
}

// JournalEntry is a posted, sealed accounting entry. Entries are immutable;
// corrections go through new reversing entries. PostedAt is the UTC instant
// the entry was written; Date is the business date it belongs to.
type JournalEntry struct {
	ID          string        `json:"id"`
	Date        Date          `json:"date"`
	PeriodID    string        `json:"period_id"`
	Currency    string        `json:"currency"`
	Description string        `json:"description"`
	Lines       []JournalLine `json:"lines"`
	PostedAt    time.Time     `json:"posted_at"`
}

// JournalRelationType classifies a JournalRelation; new kinds are added when
// a new business operation needs structural linkage between posted entries.
type JournalRelationType string

const (
	RelationReverses JournalRelationType = "reverses"
	RelationCorrects JournalRelationType = "corrects"
	RelationSettles  JournalRelationType = "settles"
	RelationCloses   JournalRelationType = "closes"
	RelationAdjusts  JournalRelationType = "adjusts"
)

// RelationReason classifies why a relation was created; free-text rationale
// belongs in JournalRelation.Note rather than here.
type RelationReason string

const (
	ReasonAmountError    RelationReason = "amount_error"
	ReasonAccountError   RelationReason = "account_error"
	ReasonDuplicate      RelationReason = "duplicate"
	ReasonCustomerCancel RelationReason = "customer_cancel"
	ReasonPeriodEnd      RelationReason = "period_end"
	ReasonOther          RelationReason = "other"
)

// JournalRelation is a directional, typed link between two posted entries.
// It is append-only with composite identity (FromEntry, ToEntry); a wrong
// relation is corrected by appending another relation, not by editing the row.
type JournalRelation struct {
	FromEntry string              `json:"from_entry"`
	ToEntry   string              `json:"to_entry"`
	Type      JournalRelationType `json:"type"`
	Reason    RelationReason      `json:"reason,omitempty"`
	Note      string              `json:"note,omitempty"`
}
