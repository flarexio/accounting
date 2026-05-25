package memory

import (
	"context"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/flarexio/accounting"
)

// accountingRepository is the in-memory accounting.LedgerRepository. All
// operations are safe for concurrent use; entries and their Lines are cloned
// in and out so callers cannot mutate stored state through a returned value.
type accountingRepository struct {
	mu       sync.RWMutex
	company  *accounting.Company
	accounts map[string]accounting.Account
	branches map[string]accounting.Branch
	periods  map[string]accounting.Period
	entries  []accounting.JournalEntry
	entryIdx map[string]int
	lastSeq  map[string]uint64
	searcher accounting.AccountSearcher
}

// Option configures an in-memory repository at construction time.
type Option func(*accountingRepository)

// WithSearcher routes FindAccounts to s when NameContains is set, and indexes every PutAccount through it.
func WithSearcher(s accounting.AccountSearcher) Option {
	return func(r *accountingRepository) { r.searcher = s }
}

// NewAccountingRepository returns an empty in-memory accounting.LedgerRepository.
func NewAccountingRepository(opts ...Option) accounting.LedgerRepository {
	r := &accountingRepository{
		accounts: make(map[string]accounting.Account),
		branches: make(map[string]accounting.Branch),
		periods:  make(map[string]accounting.Period),
		entryIdx: make(map[string]int),
		lastSeq:  make(map[string]uint64),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

const findAccountsSearcherLimit = 20

func (r *accountingRepository) Account(_ context.Context, code string) (accounting.Account, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[code]
	return a, ok, nil
}

func (r *accountingRepository) Period(_ context.Context, id string) (accounting.Period, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.periods[id]
	return p, ok, nil
}

func (r *accountingRepository) Branch(_ context.Context, id string) (accounting.Branch, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.branches[id]
	return b, ok, nil
}

func (r *accountingRepository) Entry(_ context.Context, id string) (accounting.JournalEntry, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.entryIdx[id]
	if !ok {
		return accounting.JournalEntry{}, false, nil
	}
	return cloneAccountingEntry(r.entries[idx]), true, nil
}

func (r *accountingRepository) Accounts(_ context.Context) ([]accounting.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Account, 0, len(r.accounts))
	for _, a := range r.accounts {
		out = append(out, a)
	}
	return out, nil
}

// FindAccounts honors Type and ActiveOnly; NameContains is delegated to the wired AccountSearcher and ignored when none is set.
func (r *accountingRepository) FindAccounts(ctx context.Context, filter accounting.AccountFilter) ([]accounting.Account, error) {
	needle := strings.TrimSpace(filter.NameContains)
	if needle != "" && r.searcher != nil {
		return r.searcher.Search(ctx, needle, filter, findAccountsSearcherLimit)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Account, 0, len(r.accounts))
	for _, a := range r.accounts {
		if filter.ActiveOnly && !a.Active {
			continue
		}
		if filter.Type != "" && a.Type != filter.Type {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out, nil
}

func (r *accountingRepository) Periods(_ context.Context) ([]accounting.Period, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Period, 0, len(r.periods))
	for _, p := range r.periods {
		out = append(out, p)
	}
	return out, nil
}

func (r *accountingRepository) Branches(_ context.Context) ([]accounting.Branch, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Branch, 0, len(r.branches))
	for _, b := range r.branches {
		out = append(out, b)
	}
	return out, nil
}

func (r *accountingRepository) Entries(_ context.Context) ([]accounting.JournalEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.JournalEntry, len(r.entries))
	for i, e := range r.entries {
		out[i] = cloneAccountingEntry(e)
	}
	return out, nil
}

func (r *accountingRepository) Company(_ context.Context) (accounting.Company, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.company == nil {
		return accounting.Company{}, false, nil
	}
	return *r.company, true, nil
}

func (r *accountingRepository) SetCompany(_ context.Context, c accounting.Company) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.company = &c
	return nil
}

func (r *accountingRepository) PutAccount(ctx context.Context, a accounting.Account) error {
	r.mu.Lock()
	r.accounts[a.Code] = a
	r.mu.Unlock()
	if r.searcher != nil {
		return r.searcher.Index(ctx, a)
	}
	return nil
}

func (r *accountingRepository) PutPeriod(_ context.Context, p accounting.Period) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.periods[p.ID] = p
	return nil
}

func (r *accountingRepository) PutBranch(_ context.Context, b accounting.Branch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.branches[b.ID] = b
	return nil
}

// Apply writes the entry and advances LastSequence for evt.Subject under one
// mutex, so a concurrent LastSequence reader cannot see the entry without
// also seeing the new sequence.
func (r *accountingRepository) Apply(_ context.Context, evt accounting.JournalPosted) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := cloneAccountingEntry(evt.Entry)
	r.entries = append(r.entries, stored)
	r.entryIdx[stored.ID] = len(r.entries) - 1
	if evt.Subject != "" && evt.Sequence > r.lastSeq[evt.Subject] {
		r.lastSeq[evt.Subject] = evt.Sequence
	}
	return nil
}

func (r *accountingRepository) LastSequence(_ context.Context, subject string) (uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastSeq[subject], nil
}

func cloneAccountingEntry(e accounting.JournalEntry) accounting.JournalEntry {
	out := e
	out.Lines = cloneAccountingLines(e.Lines)
	return out
}

func cloneAccountingLines(in []accounting.JournalLine) []accounting.JournalLine {
	if in == nil {
		return nil
	}
	out := make([]accounting.JournalLine, len(in))
	for i, l := range in {
		out[i] = l
		if l.Dimensions.Tags != nil {
			tags := make(map[string]string, len(l.Dimensions.Tags))
			maps.Copy(tags, l.Dimensions.Tags)
			out[i].Dimensions.Tags = tags
		}
	}
	return out
}
