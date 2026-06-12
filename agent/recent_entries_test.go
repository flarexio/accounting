package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/flarexio/accounting"
)

func entry(id string) accounting.JournalEntry {
	return accounting.JournalEntry{
		ID:          id,
		Date:        accounting.NewDate(2026, 5, 18),
		PeriodID:    "2026-05",
		Description: "gift to client",
		Lines: []accounting.JournalLine{
			{AccountCode: "6115", Side: accounting.SideDebit, Amount: 6000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
			{AccountCode: "1101", Side: accounting.SideCredit, Amount: 6000, Dimensions: accounting.Dimensions{BranchID: "hq"}},
		},
	}
}

func TestRecentEntries_BoundedMostRecentFirst(t *testing.T) {
	m := NewRecentEntries(3)
	for _, id := range []string{"JE-0001", "JE-0002", "JE-0003", "JE-0004"} {
		m.Add(entry(id))
	}
	got := m.Recent(0)
	if len(got) != 3 {
		t.Fatalf("cap not enforced: got %d entries", len(got))
	}
	want := []string{"JE-0004", "JE-0003", "JE-0002"} // most recent first, JE-0001 dropped
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("Recent[%d] = %s, want %s", i, got[i].ID, id)
		}
	}
	if lim := m.Recent(2); len(lim) != 2 || lim[0].ID != "JE-0004" {
		t.Errorf("Recent(2) wrong: %+v", lim)
	}
}

func TestRecentEntries_IgnoresEmptyID(t *testing.T) {
	m := NewRecentEntries(3)
	m.Add(accounting.JournalEntry{})
	if got := m.Recent(0); len(got) != 0 {
		t.Errorf("empty-id entry should not be recorded, got %d", len(got))
	}
}

func TestRecentEntriesTool_ListsSessionPostings(t *testing.T) {
	m := NewRecentEntries(5)
	m.Add(entry("JE-0001"))
	tool := recallTools(nil, m)[toolRecentEntries]

	out, err := tool.Handler(context.Background(), []byte(`{"limit":0}`))
	if err != nil {
		t.Fatalf("recent_entries: %v", err)
	}
	for _, want := range []string{"JE-0001", "6115", "1101", "gift to client"} {
		if !strings.Contains(out, want) {
			t.Errorf("recent_entries output missing %q:\n%s", want, out)
		}
	}
}

func TestRecentEntriesTool_EmptyWhenNothingPosted(t *testing.T) {
	tool := recallTools(nil, NewRecentEntries(5))[toolRecentEntries]
	out, err := tool.Handler(context.Background(), []byte(`{"limit":0}`))
	if err != nil {
		t.Fatalf("recent_entries: %v", err)
	}
	if !strings.Contains(out, "No entries posted yet") {
		t.Errorf("expected empty notice, got: %s", out)
	}
}
