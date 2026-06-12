package agent

import (
	"sync"

	"github.com/flarexio/accounting"
)

// RecentEntries is a bounded, most-recent-first record of the journal entries
// posted during one bookkeeper session. It is the agent's cross-turn working
// set: the recall tools read it so a later turn can resolve a reference like
// "redo that entry" without the whole transcript being replayed into the prompt.
// It holds at most Cap entries; older ones are dropped.
type RecentEntries struct {
	mu      sync.Mutex
	cap     int
	entries []accounting.JournalEntry
}

// NewRecentEntries returns a RecentEntries holding at most cap entries (cap < 1
// is treated as 1).
func NewRecentEntries(cap int) *RecentEntries {
	if cap < 1 {
		cap = 1
	}
	return &RecentEntries{cap: cap}
}

// Add records a posted entry as the most recent, dropping the oldest past Cap.
func (m *RecentEntries) Add(e accounting.JournalEntry) {
	if m == nil || e.ID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
	if len(m.entries) > m.cap {
		m.entries = m.entries[len(m.entries)-m.cap:]
	}
}

// Recent returns up to limit entries, most recent first; limit < 1 returns all.
func (m *RecentEntries) Recent(limit int) []accounting.JournalEntry {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit < 1 || limit > len(m.entries) {
		limit = len(m.entries)
	}
	out := make([]accounting.JournalEntry, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, m.entries[len(m.entries)-1-i])
	}
	return out
}
