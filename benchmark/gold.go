package benchmark

import (
	"fmt"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

// Gold is the expected answer for a Case: an Intent kind plus the payload
// fields that must match. Free-text fields (description, memo, date) are not
// represented here, so they are never part of the comparison.
type Gold struct {
	Kind    bookkeeping.IntentKind `yaml:"kind"`
	Post    *GoldPost              `yaml:"post_journal,omitempty"`
	Reverse *GoldReverse           `yaml:"reverse_journal,omitempty"`
	Reject  *GoldReject            `yaml:"reject,omitempty"`
}

// GoldPost is the expected payload of a post_journal Intent.
type GoldPost struct {
	PeriodID string     `yaml:"period_id"`
	Currency string     `yaml:"currency"`
	Lines    []GoldLine `yaml:"lines"`
}

// GoldLine is one expected journal line; lines compare as a set, not by order.
type GoldLine struct {
	AccountCode string              `yaml:"account_code"`
	Side        accounting.LineSide `yaml:"side"`
	Amount      int64               `yaml:"amount"`
	BranchID    string              `yaml:"branch_id,omitempty"`
}

// GoldReverse is the expected payload of a reverse_journal Intent.
type GoldReverse struct {
	EntryID string `yaml:"entry_id"`
}

// GoldReject marks reject as the expected Intent kind. No fields are
// compared today; the kind match is the whole signal.
type GoldReject struct{}

func (g Gold) validate() error {
	switch g.Kind {
	case bookkeeping.IntentPostJournal:
		if g.Post == nil {
			return fmt.Errorf("gold.post_journal is required when kind is post_journal")
		}
		if g.Post.PeriodID == "" {
			return fmt.Errorf("gold.post_journal.period_id is required")
		}
		if g.Post.Currency == "" {
			return fmt.Errorf("gold.post_journal.currency is required")
		}
		if len(g.Post.Lines) < 2 {
			return fmt.Errorf("gold.post_journal.lines needs at least two lines")
		}
		for i, l := range g.Post.Lines {
			if l.AccountCode == "" {
				return fmt.Errorf("gold.post_journal.lines[%d].account_code is required", i)
			}
			if l.Side != accounting.SideDebit && l.Side != accounting.SideCredit {
				return fmt.Errorf("gold.post_journal.lines[%d].side must be debit or credit", i)
			}
			if l.Amount <= 0 {
				return fmt.Errorf("gold.post_journal.lines[%d].amount must be positive", i)
			}
		}
	case bookkeeping.IntentReverseJournal:
		if g.Reverse == nil || g.Reverse.EntryID == "" {
			return fmt.Errorf("gold.reverse_journal.entry_id is required when kind is reverse_journal")
		}
	case bookkeeping.IntentReject:
		// reject needs no payload today
	case "":
		return fmt.Errorf("gold.kind is required")
	default:
		return fmt.Errorf("gold.kind %q is not supported", g.Kind)
	}
	return nil
}

// goldLineKey is the structural identity of a line used for set comparison.
type goldLineKey struct {
	AccountCode string
	Side        accounting.LineSide
	Amount      int64
	BranchID    string
}

func (l GoldLine) key() goldLineKey {
	return goldLineKey{AccountCode: l.AccountCode, Side: l.Side, Amount: l.Amount, BranchID: l.BranchID}
}

func lineKey(l accounting.JournalLine) goldLineKey {
	return goldLineKey{AccountCode: l.AccountCode, Side: l.Side, Amount: l.Amount, BranchID: l.Dimensions.BranchID}
}
