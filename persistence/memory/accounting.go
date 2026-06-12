package memory

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/flarexio/accounting"
)

// Repository is the in-memory accounting.LedgerRepository. All operations are
// safe for concurrent use; entries and their Lines are cloned in and out so
// callers cannot mutate stored state through a returned value. The type is
// exported so callers that need its non-interface methods (e.g. ClearJournals
// in benchmarks) can hold the concrete pointer.
type Repository struct {
	mu        sync.RWMutex
	company   *accounting.Company
	accounts  map[string]accounting.Account
	branches  map[string]accounting.Branch
	periods   map[string]accounting.Period
	entries   []accounting.JournalEntry
	entryIdx  map[string]int
	lastSeq   map[string]uint64
	relations []accounting.JournalRelation
	searcher  accounting.AccountSearcher
}

// Option configures an in-memory repository at construction time.
type Option func(*Repository)

// WithSearcher routes FindAccounts to s when Query is set, and indexes every PutAccount through it.
func WithSearcher(s accounting.AccountSearcher) Option {
	return func(r *Repository) { r.searcher = s }
}

// NewAccountingRepository returns an empty in-memory Repository. The concrete
// pointer satisfies accounting.LedgerRepository for callers that only need the
// interface, and exposes ClearJournals for callers that need to reset ledger
// state between runs.
func NewAccountingRepository(opts ...Option) *Repository {
	r := &Repository{
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

func (r *Repository) Account(_ context.Context, code string) (accounting.Account, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.accounts[code]
	return a, ok, nil
}

func (r *Repository) Period(_ context.Context, id string) (accounting.Period, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.periods[id]
	return p, ok, nil
}

func (r *Repository) Branch(_ context.Context, id string) (accounting.Branch, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.branches[id]
	return b, ok, nil
}

func (r *Repository) Entry(_ context.Context, id string) (accounting.JournalEntry, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	idx, ok := r.entryIdx[id]
	if !ok {
		return accounting.JournalEntry{}, false, nil
	}
	return cloneAccountingEntry(r.entries[idx]), true, nil
}

func (r *Repository) Accounts(_ context.Context) ([]accounting.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Account, 0, len(r.accounts))
	for _, a := range r.accounts {
		out = append(out, a)
	}
	return out, nil
}

// FindAccounts honors Type and ActiveOnly; when Query is set and a searcher is
// wired it runs hybrid retrieval (semantic + lexical, fused by RRF), otherwise
// Query is ignored.
func (r *Repository) FindAccounts(ctx context.Context, filter accounting.AccountFilter) ([]accounting.Account, error) {
	needle := strings.TrimSpace(filter.Query)
	if needle != "" && r.searcher != nil {
		return r.hybridSearch(ctx, needle, filter)
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

// hybridSearch fuses the searcher's semantic ranking with a lexical pass over
// the chart (exact code, exact or substring name) using reciprocal rank
// fusion, so an exact code or name hit the embedding buries still surfaces.
func (r *Repository) hybridSearch(ctx context.Context, query string, filter accounting.AccountFilter) ([]accounting.Account, error) {
	dense, err := r.searcher.Search(ctx, query, filter, findAccountsSearcherLimit)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	lexical := r.lexicalCandidates(query, filter)
	r.mu.RUnlock()
	return accounting.FuseAccountsRRF([][]accounting.Account{dense, lexical}, findAccountsSearcherLimit), nil
}

// lexicalCandidates returns chart accounts whose code or name relates to query
// (per accounting.LexicalAccountTier), honoring Type and ActiveOnly, ordered
// by match strength then code. The caller holds the read lock.
func (r *Repository) lexicalCandidates(query string, filter accounting.AccountFilter) []accounting.Account {
	type scored struct {
		account accounting.Account
		tier    int
	}
	var matched []scored
	for _, a := range r.accounts {
		if filter.ActiveOnly && !a.Active {
			continue
		}
		if filter.Type != "" && a.Type != filter.Type {
			continue
		}
		if tier, ok := accounting.LexicalAccountTier(query, a.Code, a.Name); ok {
			matched = append(matched, scored{a, tier})
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].tier != matched[j].tier {
			return matched[i].tier < matched[j].tier
		}
		return matched[i].account.Code < matched[j].account.Code
	})
	out := make([]accounting.Account, 0, len(matched))
	for _, m := range matched {
		out = append(out, m.account)
	}
	if len(out) > findAccountsSearcherLimit {
		out = out[:findAccountsSearcherLimit]
	}
	return out
}

func (r *Repository) Periods(_ context.Context) ([]accounting.Period, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Period, 0, len(r.periods))
	for _, p := range r.periods {
		out = append(out, p)
	}
	return out, nil
}

func (r *Repository) Branches(_ context.Context) ([]accounting.Branch, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.Branch, 0, len(r.branches))
	for _, b := range r.branches {
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (r *Repository) Entries(_ context.Context) ([]accounting.JournalEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]accounting.JournalEntry, len(r.entries))
	for i, e := range r.entries {
		out[i] = cloneAccountingEntry(e)
	}
	return out, nil
}

func (r *Repository) EntryCount(_ context.Context) (uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return uint64(len(r.entries)), nil
}

func (r *Repository) EntriesByPeriod(_ context.Context, periodID string) ([]accounting.JournalEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []accounting.JournalEntry
	for _, e := range r.entries {
		if e.PeriodID == periodID {
			out = append(out, cloneAccountingEntry(e))
		}
	}
	return out, nil
}

func (r *Repository) Company(_ context.Context) (accounting.Company, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.company == nil {
		return accounting.Company{}, false, nil
	}
	return *r.company, true, nil
}

func (r *Repository) SetCompany(_ context.Context, c accounting.Company) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.company != nil {
		c.Policy = r.company.Policy // policy has its own write path; never clobber on re-seed
	}
	r.company = &c
	return nil
}

// SetPolicy stores the company's policy document; an absent company is an error.
func (r *Repository) SetPolicy(_ context.Context, policy string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.company == nil {
		return errors.New("memory: SetPolicy: no company configured")
	}
	r.company.Policy = policy
	return nil
}

func (r *Repository) PutAccount(ctx context.Context, a accounting.Account) error {
	r.mu.Lock()
	r.accounts[a.Code] = a
	r.mu.Unlock()
	if r.searcher != nil {
		return r.searcher.Index(ctx, a)
	}
	return nil
}

func (r *Repository) PutPeriod(_ context.Context, p accounting.Period) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.periods[p.ID] = p
	return nil
}

// SetPeriodStatus transitions the named period's status under one mutex,
// advancing LastSequence from any EventMeta in the context. An unknown id is an error.
func (r *Repository) SetPeriodStatus(ctx context.Context, periodID string, status accounting.PeriodStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.periods[periodID]
	if !ok {
		return fmt.Errorf("memory: SetPeriodStatus: period %q does not exist", periodID)
	}
	p.Status = status
	r.periods[periodID] = p
	r.advanceSequence(ctx)
	return nil
}

func (r *Repository) PutBranch(_ context.Context, b accounting.Branch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.branches[b.ID] = b
	return nil
}

// AppendEntry writes the entry and its relations under one mutex, advancing
// LastSequence from any EventMeta in the context.
func (r *Repository) AppendEntry(ctx context.Context, entry accounting.JournalEntry, relations []accounting.JournalRelation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	stored := cloneAccountingEntry(entry)
	r.entries = append(r.entries, stored)
	r.entryIdx[stored.ID] = len(r.entries) - 1
	r.relations = append(r.relations, relations...)
	r.advanceSequence(ctx)
	return nil
}

// advanceSequence bumps LastSequence from the context's EventMeta when present.
// The caller holds the write lock.
func (r *Repository) advanceSequence(ctx context.Context) {
	meta, ok := accounting.EventMetaFrom(ctx)
	if !ok || meta.Subject == "" {
		return
	}
	if meta.Sequence > r.lastSeq[meta.Subject] {
		r.lastSeq[meta.Subject] = meta.Sequence
	}
}

// ClearJournals drops every posted entry, every relation, and resets
// per-subject sequence counters; the chart of accounts, periods, branches,
// company, and any attached searcher are preserved so the same Repository
// can be reused across benchmark iterations without paying the seed-time
// embedding cost again.
func (r *Repository) ClearJournals(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = nil
	r.entryIdx = make(map[string]int)
	r.lastSeq = make(map[string]uint64)
	r.relations = nil
	return nil
}

func (r *Repository) Relation(_ context.Context, fromEntry, toEntry string) (accounting.JournalRelation, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rel := range r.relations {
		if rel.FromEntry == fromEntry && rel.ToEntry == toEntry {
			return rel, true, nil
		}
	}
	return accounting.JournalRelation{}, false, nil
}

func (r *Repository) RelationsFrom(_ context.Context, entryID string) ([]accounting.JournalRelation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []accounting.JournalRelation
	for _, rel := range r.relations {
		if rel.FromEntry == entryID {
			out = append(out, rel)
		}
	}
	return out, nil
}

func (r *Repository) RelationsTo(_ context.Context, entryID string) ([]accounting.JournalRelation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []accounting.JournalRelation
	for _, rel := range r.relations {
		if rel.ToEntry == entryID {
			out = append(out, rel)
		}
	}
	return out, nil
}

func (r *Repository) LastSequence(_ context.Context, subject string) (uint64, error) {
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
