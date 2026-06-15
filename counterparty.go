package accounting

import (
	"fmt"
	"strings"
)

// CounterpartyKind classifies a Counterparty as a customer, a supplier, or both.
type CounterpartyKind string

const (
	CounterpartyCustomer CounterpartyKind = "customer"
	CounterpartySupplier CounterpartyKind = "supplier"
	CounterpartyBoth     CounterpartyKind = "both"
)

// Counterparty is a customer or supplier the ledger transacts with. ID is
// producer-assigned (CP-0001); TaxID is the Taiwan 統一編號. Inactive
// counterparties cannot be referenced by new postings. Aliases enrich lexical
// lookup and carry no posting invariant.
type Counterparty struct {
	ID          string           `json:"id" yaml:"id"`
	Name        string           `json:"name" yaml:"name"`
	Kind        CounterpartyKind `json:"kind" yaml:"kind"`
	TaxID       string           `json:"tax_id,omitempty" yaml:"tax_id,omitempty"`
	Active      bool             `json:"active" yaml:"active"`
	Aliases     []string         `json:"aliases,omitempty" yaml:"aliases,omitempty"`
	Description string           `json:"description,omitempty" yaml:"description,omitempty"`
}

// FormatCounterpartyID formats a per-subject counter into the canonical Counterparty.ID.
func FormatCounterpartyID(seq uint64) string {
	return fmt.Sprintf("CP-%04d", seq)
}

// CounterpartyMatch scores how exactly query identifies c for lexical lookup; a
// lower tier is a stronger match and ok is false when nothing relates. Matching
// is case-insensitive.
func CounterpartyMatch(query string, c Counterparty) (tier int, ok bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0, false
	}
	switch {
	case q == strings.ToLower(c.ID):
		return 0, true
	case q == strings.ToLower(c.Name):
		return 1, true
	case q == strings.ToLower(c.TaxID):
		return 2, true
	}
	for _, a := range c.Aliases {
		if q == strings.ToLower(a) {
			return 3, true
		}
	}
	n := strings.ToLower(c.Name)
	if strings.Contains(n, q) || strings.Contains(q, n) {
		return 4, true
	}
	for _, a := range c.Aliases {
		if al := strings.ToLower(a); strings.Contains(al, q) || strings.Contains(q, al) {
			return 5, true
		}
	}
	return 0, false
}
