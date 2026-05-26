package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/bookkeeping"
)

func parseIntent(content string) (bookkeeping.Intent, bool) {
	const marker = "\nintent: "
	idx := strings.LastIndex(content, marker)
	if idx < 0 {
		return bookkeeping.Intent{}, false
	}
	raw := strings.TrimSpace(content[idx+len(marker):])
	var intent bookkeeping.Intent
	if err := json.Unmarshal([]byte(raw), &intent); err != nil {
		return bookkeeping.Intent{}, false
	}
	return intent, true
}

// renderReversePreview mirrors the original entry's lines (sides flipped) and
// renders them with the same layout as a post_journal preview.
func renderReversePreview(orig accounting.JournalEntry, reason string, accountName accountNameFn) string {
	mirror := &accounting.JournalIntent{
		Date:        orig.Date,
		PeriodID:    orig.PeriodID,
		Currency:    orig.Currency,
		Description: reverseDescription(orig.ID, reason),
		Lines:       make([]accounting.JournalLine, len(orig.Lines)),
	}
	for i, l := range orig.Lines {
		l.Side = flipSide(l.Side)
		mirror.Lines[i] = l
	}
	return renderJournalPreview(mirror, accountName)
}

func reverseDescription(entryID, reason string) string {
	out := "Reversal of " + entryID
	if reason != "" {
		out += ": " + reason
	}
	return out
}

func flipSide(side accounting.LineSide) accounting.LineSide {
	switch side {
	case accounting.SideDebit:
		return accounting.SideCredit
	case accounting.SideCredit:
		return accounting.SideDebit
	default:
		return side
	}
}

// accountNameFn returns the chart-of-accounts name for code, or "" when the
// resolver can't find one; renderJournalPreview then falls back to the code.
type accountNameFn func(code string) string

func renderJournalPreview(intent *accounting.JournalIntent, accountName accountNameFn) string {
	var b strings.Builder

	header := intent.Date.String()
	if intent.PeriodID != "" {
		header += " · " + intent.PeriodID
	}
	if intent.Currency != "" {
		header += " · " + intent.Currency
	}
	if intent.Description != "" {
		header += " · " + intent.Description
	}
	b.WriteString(header)

	lines := debitsFirst(intent.Lines)
	labelWidth, amountWidth := 0, 0
	labels := make([]string, len(lines))
	amounts := make([]string, len(lines))
	for i, l := range lines {
		labels[i] = lineLabel(l, accountName)
		amounts[i] = formatAmount(l.Amount)
		if w := lipgloss.Width(labels[i]); w > labelWidth {
			labelWidth = w
		}
		if w := lipgloss.Width(amounts[i]); w > amountWidth {
			amountWidth = w
		}
	}

	for i, l := range lines {
		b.WriteString("\n")
		label := padRight(labels[i], labelWidth)
		amount := padLeft(amounts[i], amountWidth)
		if l.Side == accounting.SideDebit {
			fmt.Fprintf(&b, "  %s  %s", label, amount)
		} else {
			fmt.Fprintf(&b, "       %s  %s", label, amount)
		}
	}
	return b.String()
}

// debitsFirst returns a copy of lines re-ordered for display: all debits, then
// all credits, preserving relative order within each side.
func debitsFirst(lines []accounting.JournalLine) []accounting.JournalLine {
	out := make([]accounting.JournalLine, len(lines))
	copy(out, lines)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Side == accounting.SideDebit && out[j].Side != accounting.SideDebit
	})
	return out
}

func lineLabel(l accounting.JournalLine, accountName accountNameFn) string {
	label := l.AccountCode
	if accountName != nil {
		if name := accountName(l.AccountCode); name != "" {
			label += " " + name
		}
	}
	return label
}

func padRight(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

func padLeft(s string, width int) string {
	pad := width - lipgloss.Width(s)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(" ", pad) + s
}

// formatAmount renders a non-negative minor-unit amount with thousands
// separators; journal amounts are validator-guaranteed > 0.
func formatAmount(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	head := len(s) % 3
	if head > 0 {
		b.WriteString(s[:head])
		b.WriteByte(',')
	}
	for i := head; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
