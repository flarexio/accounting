package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/flarexio/accounting"
	"github.com/flarexio/accounting/persistence/postgres/pgstore"
)

// accountingRepository implements accounting.LedgerRepository against Postgres.
type accountingRepository struct {
	pool *pgxpool.Pool
	q    *pgstore.Queries
}

// NewAccountingRepository opens a pgxpool.Pool from dsn and returns the
// accounting.LedgerRepository it backs, plus an io.Closer the caller defers.
func NewAccountingRepository(ctx context.Context, dsn string) (accounting.LedgerRepository, io.Closer, error) {
	pool, closer, err := connectPool(ctx, dsn)
	if err != nil {
		return nil, nil, err
	}
	return &accountingRepository{pool: pool, q: pgstore.New(pool)}, closer, nil
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

func (r *accountingRepository) PutAccount(ctx context.Context, a accounting.Account) error {
	if err := r.q.UpsertAccount(ctx, pgstore.UpsertAccountParams{
		Code:   a.Code,
		Name:   a.Name,
		Type:   string(a.Type),
		Active: a.Active,
	}); err != nil {
		return fmt.Errorf("postgres: UpsertAccount: %w", err)
	}
	return nil
}

func (r *accountingRepository) PutPeriod(ctx context.Context, p accounting.Period) error {
	if err := r.q.UpsertPeriod(ctx, pgstore.UpsertPeriodParams{
		ID:      p.ID,
		StartAt: pgtype.Timestamptz{Time: p.Start, Valid: true},
		EndAt:   pgtype.Timestamptz{Time: p.End, Valid: true},
		Status:  string(p.Status),
	}); err != nil {
		return fmt.Errorf("postgres: UpsertPeriod: %w", err)
	}
	return nil
}

func (r *accountingRepository) PutBranch(ctx context.Context, b accounting.Branch) error {
	if err := r.q.UpsertBranch(ctx, pgstore.UpsertBranchParams{
		ID:   b.ID,
		Name: b.Name,
	}); err != nil {
		return fmt.Errorf("postgres: UpsertBranch: %w", err)
	}
	return nil
}

// Apply writes the entry, its lines, and the new last-sequence record in one
// transaction, so a concurrent LastSequence reader cannot see the entry
// without also seeing the new sequence.
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
		EntryDate:   pgtype.Timestamptz{Time: entry.Date, Valid: true},
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
	return accounting.Branch{ID: row.ID, Name: row.Name}
}

func periodFromRow(row pgstore.Period) accounting.Period {
	return accounting.Period{
		ID:     row.ID,
		Start:  row.StartAt.Time,
		End:    row.EndAt.Time,
		Status: accounting.PeriodStatus(row.Status),
	}
}

func entryFromRow(row pgstore.JournalEntry) accounting.JournalEntry {
	return accounting.JournalEntry{
		ID:          row.ID,
		Date:        row.EntryDate.Time,
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
