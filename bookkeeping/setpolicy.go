package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flarexio/accounting"
)

// SetPolicy is the "set company bookkeeping policy" use case: it publishes a
// PolicySet event for the projection to store. It is operator-driven (the
// `ledger policy` CLI), not an agent Intent.
type SetPolicy struct {
	Publisher Publisher
}

// Execute publishes the policy document as a PolicySet event. The text is
// trimmed; an empty document is a valid clear.
func (uc SetPolicy) Execute(ctx context.Context, policy string) error {
	if uc.Publisher == nil {
		return errors.New("bookkeeping: set policy has no event publisher")
	}
	evt := accounting.PolicySet{Policy: strings.TrimSpace(policy)}
	if err := uc.Publisher.Publish(ctx, evt, accounting.ExpectedSequence{}); err != nil {
		return fmt.Errorf("bookkeeping: publish %s: %w", evt.EventSubject(), err)
	}
	return nil
}
