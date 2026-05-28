package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/postgres/pgstore"
)

type accountingRepository struct {
	pool     *pgxpool.Pool
	q        *pgstore.Queries
	embedder accounting.Embedder
}

// NewAccountingRepository opens a pgxpool.Pool from dsn and returns the LedgerRepository plus its Closer. embedder is required.
func NewAccountingRepository(ctx context.Context, dsn string, embedder accounting.Embedder) (accounting.LedgerRepository, io.Closer, error) {
	if embedder == nil {
		return nil, nil, errors.New("postgres: NewAccountingRepository requires a non-nil Embedder")
	}
	pool, closer, err := connectPool(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return &accountingRepository{pool: pool, q: pgstore.New(pool), embedder: embedder}, closer, nil
}

func (r *accountingRepository) Company(ctx context.Context) (accounting.Company, bool, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, timezone, retained_earnings_code FROM companies LIMIT 2`)
	if err != nil {
		return accounting.Company{}, false, fmt.Errorf("postgres: Company: %w", err)
	}
	defer rows.Close()
	var companies []accounting.Company
	for rows.Next() {
		var c accounting.Company
		if err := rows.Scan(&c.ID, &c.Name, &c.TimeZone, &c.RetainedEarningsCode); err != nil {
			return accounting.Company{}, false, fmt.Errorf("postgres: scan company: %w", err)
		}
		companies = append(companies, c)
	}
	if err := rows.Err(); err != nil {
		return accounting.Company{}, false, fmt.Errorf("postgres: iterate companies: %w", err)
	}
	switch len(companies) {
	case 0:
		return accounting.Company{}, false, nil
	case 1:
		return companies[0], true, nil
	default:
		return accounting.Company{}, false, fmt.Errorf("postgres: expected single company, found %d", len(companies))
	}
}

func (r *accountingRepository) SetCompany(ctx context.Context, c accounting.Company) error {
	if _, err := r.pool.Exec(ctx, `
INSERT INTO companies (id, name, timezone, retained_earnings_code) VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    timezone = EXCLUDED.timezone,
    retained_earnings_code = EXCLUDED.retained_earnings_code
`, c.ID, c.Name, c.TimeZone, c.RetainedEarningsCode); err != nil {
		return fmt.Errorf("postgres: SetCompany: %w", err)
	}
	return nil
}

func (r *accountingRepository) Account(ctx context.Context, code string) (accounting.Account, bool, error) {
	row, err := r.q.GetAccount(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accounting.Account{}, false, nil
		}
		return accounting.Account{}, false, fmt.Errorf("postgres: GetAccount: %w", err)
	}
	return accountFromRow(row), true, nil
}

func (r *accountingRepository) Period(ctx context.Context, id string) (accounting.Period, bool, error) {
	row, err := r.q.GetPeriod(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accounting.Period{}, false, nil
		}
		return accounting.Period{}, false, fmt.Errorf("postgres: GetPeriod: %w", err)
	}
	return periodFromRow(row), true, nil
}

func (r *accountingRepository) Branch(ctx context.Context, id string) (accounting.Branch, bool, error) {
	row, err := r.q.GetBranch(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accounting.Branch{}, false, nil
		}
		return accounting.Branch{}, false, fmt.Errorf("postgres: GetBranch: %w", err)
	}
	return branchFromRow(row), true, nil
}

func (r *accountingRepository) Entry(ctx context.Context, id string) (accounting.JournalEntry, bool, error) {
	row, err := r.q.GetEntry(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accounting.JournalEntry{}, false, nil
		}
		return accounting.JournalEntry{}, false, fmt.Errorf("postgres: GetEntry: %w", err)
	}
	lines, err := r.q.ListEntryLines(ctx, id)
	if err != nil {
		return accounting.JournalEntry{}, false, fmt.Errorf("postgres: ListEntryLines: %w", err)
	}
	entry := entryFromRow(row)
	entry.Lines, err = linesFromRows(lines)
	if err != nil {
		return accounting.JournalEntry{}, false, err
	}
	return entry, true, nil
}

func (r *accountingRepository) Accounts(ctx context.Context) ([]accounting.Account, error) {
	rows, err := r.q.ListAccounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListAccounts: %w", err)
	}
	out := make([]accounting.Account, len(rows))
	for i, row := range rows {
		out[i] = accountFromRow(row)
	}
	return out, nil
}

// FindAccounts uses pgvector cosine similarity when NameContains is set; otherwise a plain Type/ActiveOnly SQL filter.
func (r *accountingRepository) FindAccounts(ctx context.Context, filter accounting.AccountFilter) ([]accounting.Account, error) {
	needle := strings.TrimSpace(filter.NameContains)
	if needle == "" {
		return r.findAccountsByFilter(ctx, filter)
	}
	return r.findAccountsBySimilarity(ctx, filter, needle)
}

const findAccountsSimilarityLimit = 20

func (r *accountingRepository) findAccountsByFilter(ctx context.Context, filter accounting.AccountFilter) ([]accounting.Account, error) {
	rows, err := r.pool.Query(ctx, `
SELECT code, name, type, active
FROM accounts
WHERE (NOT $1::bool OR active)
  AND ($2::text = '' OR type = $2::text)
ORDER BY code
`, filter.ActiveOnly, string(filter.Type))
	if err != nil {
		return nil, fmt.Errorf("postgres: FindAccounts: %w", err)
	}
	defer rows.Close()
	return scanAccountRows(rows)
}

func (r *accountingRepository) findAccountsBySimilarity(ctx context.Context, filter accounting.AccountFilter, query string) ([]accounting.Account, error) {
	vec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
SELECT code, name, type, active
FROM accounts
WHERE (NOT $1::bool OR active)
  AND ($2::text = '' OR type = $2::text)
  AND embedding IS NOT NULL
ORDER BY embedding <=> $3::vector
LIMIT $4
`, filter.ActiveOnly, string(filter.Type), formatVector(vec), findAccountsSimilarityLimit)
	if err != nil {
		return nil, fmt.Errorf("postgres: FindAccounts (similarity): %w", err)
	}
	defer rows.Close()
	return scanAccountRows(rows)
}

func scanAccountRows(rows pgx.Rows) ([]accounting.Account, error) {
	var out []accounting.Account
	for rows.Next() {
		var row pgstore.Account
		if err := rows.Scan(&row.Code, &row.Name, &row.Type, &row.Active); err != nil {
			return nil, fmt.Errorf("postgres: scan account: %w", err)
		}
		out = append(out, accountFromRow(row))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate accounts: %w", err)
	}
	return out, nil
}

// formatVector encodes a float32 slice in pgvector's text input format ("[v1,v2,...]").
func formatVector(v []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%g", x)
	}
	b.WriteByte(']')
	return b.String()
}

func (r *accountingRepository) Periods(ctx context.Context) ([]accounting.Period, error) {
	rows, err := r.q.ListPeriods(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListPeriods: %w", err)
	}
	out := make([]accounting.Period, len(rows))
	for i, row := range rows {
		out[i] = periodFromRow(row)
	}
	return out, nil
}

func (r *accountingRepository) Branches(ctx context.Context) ([]accounting.Branch, error) {
	rows, err := r.q.ListBranches(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListBranches: %w", err)
	}
	out := make([]accounting.Branch, len(rows))
	for i, row := range rows {
		out[i] = branchFromRow(row)
	}
	return out, nil
}

// Entries returns every posted entry sorted by sequence, each with its lines
// populated -- one query for entries, one for all their lines, stitched in memory.
func (r *accountingRepository) Entries(ctx context.Context) ([]accounting.JournalEntry, error) {
	rows, err := r.q.ListEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListEntries: %w", err)
	}
	return r.attachLines(ctx, rows)
}

// EntriesByPeriod returns posted entries filtered to periodID in the database,
// each with its lines populated.
func (r *accountingRepository) EntriesByPeriod(ctx context.Context, periodID string) ([]accounting.JournalEntry, error) {
	rows, err := r.q.ListEntriesByPeriod(ctx, periodID)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListEntriesByPeriod: %w", err)
	}
	return r.attachLines(ctx, rows)
}

// attachLines fetches every line for rows in one query and stitches them on by entry id.
func (r *accountingRepository) attachLines(ctx context.Context, rows []pgstore.JournalEntry) ([]accounting.JournalEntry, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]string, len(rows))
	for i, e := range rows {
		ids[i] = e.ID
	}
	allLines, err := r.q.ListLinesForEntries(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListLinesForEntries: %w", err)
	}
	bucket := make(map[string][]pgstore.JournalLine, len(rows))
	for _, l := range allLines {
		bucket[l.EntryID] = append(bucket[l.EntryID], l)
	}
	out := make([]accounting.JournalEntry, len(rows))
	for i, row := range rows {
		entry := entryFromRow(row)
		entry.Lines, err = linesFromRows(bucket[row.ID])
		if err != nil {
			return nil, err
		}
		out[i] = entry
	}
	return out, nil
}

// PutAccount upserts the account row and writes its embedding in the same statement.
func (r *accountingRepository) PutAccount(ctx context.Context, a accounting.Account) error {
	vec, err := r.embedder.Embed(ctx, a.Code+" "+a.Name)
	if err != nil {
		return err
	}
	if _, err := r.pool.Exec(ctx, `
INSERT INTO accounts (code, name, type, active, embedding)
VALUES ($1, $2, $3, $4, $5::vector)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name,
    type = EXCLUDED.type,
    active = EXCLUDED.active,
    embedding = EXCLUDED.embedding
`, a.Code, a.Name, string(a.Type), a.Active, formatVector(vec)); err != nil {
		return fmt.Errorf("postgres: PutAccount: %w", err)
	}
	return nil
}

func (r *accountingRepository) PutPeriod(ctx context.Context, p accounting.Period) error {
	if err := r.q.UpsertPeriod(ctx, pgstore.UpsertPeriodParams{
		ID:      p.ID,
		StartOn: pgtype.Date{Time: p.Start.Time(time.UTC), Valid: true},
		EndOn:   pgtype.Date{Time: p.End.Time(time.UTC), Valid: true},
		Status:  string(p.Status),
	}); err != nil {
		return fmt.Errorf("postgres: UpsertPeriod: %w", err)
	}
	return nil
}

func (r *accountingRepository) PutBranch(ctx context.Context, b accounting.Branch) error {
	if err := r.q.UpsertBranch(ctx, pgstore.UpsertBranchParams{
		ID:       b.ID,
		Name:     b.Name,
		Position: int32(b.Position),
	}); err != nil {
		return fmt.Errorf("postgres: UpsertBranch: %w", err)
	}
	return nil
}

// Apply writes the entry, its lines, every JournalRelation in evt.Relations,
// and the new last-sequence record in one transaction, so a concurrent reader
// cannot see the entry without its relations or its sequence.
func (r *accountingRepository) Apply(ctx context.Context, evt accounting.JournalPosted) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := r.q.WithTx(tx)

	entry := evt.Entry
	if err := q.InsertEntry(ctx, pgstore.InsertEntryParams{
		ID:          entry.ID,
		Sequence:    int64(evt.Sequence),
		Subject:     evt.Subject,
		EntryDate:   pgtype.Date{Time: entry.Date.Time(time.UTC), Valid: true},
		PeriodID:    entry.PeriodID,
		Currency:    entry.Currency,
		Description: entry.Description,
		PostedAt:    pgtype.Timestamptz{Time: entry.PostedAt, Valid: true},
	}); err != nil {
		return fmt.Errorf("postgres: InsertEntry: %w", err)
	}

	for idx, line := range entry.Lines {
		tags, err := marshalAccountingTags(line.Dimensions.Tags)
		if err != nil {
			return err
		}
		if err := q.InsertLine(ctx, pgstore.InsertLineParams{
			EntryID:     entry.ID,
			LineNo:      int32(idx),
			AccountCode: line.AccountCode,
			Side:        string(line.Side),
			Amount:      line.Amount,
			Memo:        line.Memo,
			BranchID:    line.Dimensions.BranchID,
			Tags:        tags,
		}); err != nil {
			return fmt.Errorf("postgres: InsertLine: %w", err)
		}
	}

	for _, rel := range evt.Relations {
		if err := q.InsertRelation(ctx, pgstore.InsertRelationParams{
			FromEntry: rel.FromEntry,
			ToEntry:   rel.ToEntry,
			Type:      string(rel.Type),
			Reason:    string(rel.Reason),
			Note:      rel.Note,
		}); err != nil {
			return fmt.Errorf("postgres: InsertRelation: %w", err)
		}
	}

	if evt.Subject != "" {
		if err := q.UpsertLastSequence(ctx, pgstore.UpsertLastSequenceParams{
			Subject:      evt.Subject,
			LastSequence: int64(evt.Sequence),
		}); err != nil {
			return fmt.Errorf("postgres: UpsertLastSequence: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

// ApplyPeriodClosure flips the named period to closed and advances
// LastSequence for evt.Subject in one transaction so a concurrent reader
// cannot observe the period flip without its sequence. An unknown period id
// is an error so an event for a never-seeded period does not silently
// disappear.
func (r *accountingRepository) ApplyPeriodClosure(ctx context.Context, evt accounting.PeriodClosure) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := r.q.WithTx(tx)

	rows, err := q.UpdatePeriodStatus(ctx, pgstore.UpdatePeriodStatusParams{
		ID:     evt.Period.ID,
		Status: string(accounting.PeriodClosed),
	})
	if err != nil {
		return fmt.Errorf("postgres: UpdatePeriodStatus: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("postgres: ApplyPeriodClosure: period %q does not exist", evt.Period.ID)
	}

	if evt.Subject != "" {
		if err := q.UpsertLastSequence(ctx, pgstore.UpsertLastSequenceParams{
			Subject:      evt.Subject,
			LastSequence: int64(evt.Sequence),
		}); err != nil {
			return fmt.Errorf("postgres: UpsertLastSequence: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit: %w", err)
	}
	return nil
}

func (r *accountingRepository) Relation(ctx context.Context, fromEntry, toEntry string) (accounting.JournalRelation, bool, error) {
	row, err := r.q.GetRelation(ctx, pgstore.GetRelationParams{FromEntry: fromEntry, ToEntry: toEntry})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accounting.JournalRelation{}, false, nil
		}
		return accounting.JournalRelation{}, false, fmt.Errorf("postgres: GetRelation: %w", err)
	}
	return relationFromRow(row), true, nil
}

func (r *accountingRepository) RelationsFrom(ctx context.Context, entryID string) ([]accounting.JournalRelation, error) {
	rows, err := r.q.ListRelationsFrom(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListRelationsFrom: %w", err)
	}
	return relationsFromRows(rows), nil
}

func (r *accountingRepository) RelationsTo(ctx context.Context, entryID string) ([]accounting.JournalRelation, error) {
	rows, err := r.q.ListRelationsTo(ctx, entryID)
	if err != nil {
		return nil, fmt.Errorf("postgres: ListRelationsTo: %w", err)
	}
	return relationsFromRows(rows), nil
}

func relationFromRow(row pgstore.JournalRelation) accounting.JournalRelation {
	return accounting.JournalRelation{
		FromEntry: row.FromEntry,
		ToEntry:   row.ToEntry,
		Type:      accounting.JournalRelationType(row.Type),
		Reason:    accounting.RelationReason(row.Reason),
		Note:      row.Note,
	}
}

func relationsFromRows(rows []pgstore.JournalRelation) []accounting.JournalRelation {
	if len(rows) == 0 {
		return nil
	}
	out := make([]accounting.JournalRelation, len(rows))
	for i, row := range rows {
		out[i] = relationFromRow(row)
	}
	return out
}

func (r *accountingRepository) LastSequence(ctx context.Context, subject string) (uint64, error) {
	seq, err := r.q.GetLastSequence(ctx, subject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("postgres: GetLastSequence: %w", err)
	}
	if seq < 0 {
		return 0, fmt.Errorf("postgres: negative last_sequence %d on %q", seq, subject)
	}
	return uint64(seq), nil
}

func accountFromRow(row pgstore.Account) accounting.Account {
	return accounting.Account{
		Code:   row.Code,
		Name:   row.Name,
		Type:   accounting.AccountType(row.Type),
		Active: row.Active,
	}
}

func branchFromRow(row pgstore.Branch) accounting.Branch {
	return accounting.Branch{ID: row.ID, Name: row.Name, Position: int(row.Position)}
}

func periodFromRow(row pgstore.Period) accounting.Period {
	return accounting.Period{
		ID:     row.ID,
		Start:  accounting.DateOf(row.StartOn.Time, time.UTC),
		End:    accounting.DateOf(row.EndOn.Time, time.UTC),
		Status: accounting.PeriodStatus(row.Status),
	}
}

func entryFromRow(row pgstore.JournalEntry) accounting.JournalEntry {
	return accounting.JournalEntry{
		ID:          row.ID,
		Date:        accounting.DateOf(row.EntryDate.Time, time.UTC),
		PeriodID:    row.PeriodID,
		Currency:    row.Currency,
		Description: row.Description,
		PostedAt:    row.PostedAt.Time,
	}
}

func linesFromRows(rows []pgstore.JournalLine) ([]accounting.JournalLine, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]accounting.JournalLine, len(rows))
	for i, row := range rows {
		tags, err := unmarshalAccountingTags(row.Tags)
		if err != nil {
			return nil, err
		}
		out[i] = accounting.JournalLine{
			AccountCode: row.AccountCode,
			Side:        accounting.LineSide(row.Side),
			Amount:      row.Amount,
			Memo:        row.Memo,
			Dimensions: accounting.Dimensions{
				BranchID: row.BranchID,
				Tags:     tags,
			},
		}
	}
	return out, nil
}

func marshalAccountingTags(tags map[string]string) ([]byte, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("postgres: marshal tags: %w", err)
	}
	return b, nil
}

func unmarshalAccountingTags(raw []byte) (map[string]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var tags map[string]string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil, fmt.Errorf("postgres: unmarshal tags: %w", err)
	}
	if len(tags) == 0 {
		return nil, nil
	}
	return tags, nil
}
