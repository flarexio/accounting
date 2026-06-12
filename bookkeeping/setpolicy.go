package bookkeeping

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/flarexio/accounting"
)

// SetPolicy is the operator use case that publishes a PolicySet event.
type SetPolicy struct {
	Publisher Publisher
}

// Execute publishes the trimmed policy as a PolicySet event; empty clears it.
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
