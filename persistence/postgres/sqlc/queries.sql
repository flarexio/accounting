-- name: GetAccount :one
SELECT code, name, type, active
FROM accounts
WHERE code = $1;

-- name: ListAccounts :many
SELECT code, name, type, active
FROM accounts
ORDER BY code;

-- name: UpsertAccount :exec
INSERT INTO accounts (code, name, type, active)
VALUES ($1, $2, $3, $4)
ON CONFLICT (code) DO UPDATE
SET name = EXCLUDED.name,
    type = EXCLUDED.type,
    active = EXCLUDED.active;

-- name: GetBranch :one
SELECT id, name
FROM branches
WHERE id = $1;

-- name: ListBranches :many
SELECT id, name
FROM branches
ORDER BY id;

-- name: UpsertBranch :exec
INSERT INTO branches (id, name)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE
SET name = EXCLUDED.name;

-- name: GetPeriod :one
SELECT id, start_at, end_at, status
FROM periods
WHERE id = $1;

-- name: ListPeriods :many
SELECT id, start_at, end_at, status
FROM periods
ORDER BY id;

-- name: UpsertPeriod :exec
INSERT INTO periods (id, start_at, end_at, status)
VALUES ($1, $2, $3, $4)
ON CONFLICT (id) DO UPDATE
SET start_at = EXCLUDED.start_at,
    end_at = EXCLUDED.end_at,
    status = EXCLUDED.status;

-- name: GetEntry :one
SELECT id, sequence, subject, entry_date, period_id, currency, description, posted_at
FROM journal_entries
WHERE id = $1;

-- name: ListEntries :many
SELECT id, sequence, subject, entry_date, period_id, currency, description, posted_at
FROM journal_entries
ORDER BY sequence;

-- name: ListEntryLines :many
SELECT entry_id, line_no, account_code, side, amount, memo, branch_id, tags
FROM journal_lines
WHERE entry_id = $1
ORDER BY line_no;

-- name: ListLinesForEntries :many
SELECT entry_id, line_no, account_code, side, amount, memo, branch_id, tags
FROM journal_lines
WHERE entry_id = ANY($1::text[])
ORDER BY entry_id, line_no;

-- name: InsertEntry :exec
-- Idempotent: NATS JetStream redelivers on consumer crash after commit but
-- before Ack; a duplicate INSERT would loop forever as Nak'd unique-key
-- violations. Entry.ID is derived from the broker sequence so duplicates
-- collapse to the same row.
INSERT INTO journal_entries (
    id, sequence, subject, entry_date, period_id, currency, description, posted_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO NOTHING;

-- name: InsertLine :exec
-- Idempotent for the same reason as InsertEntry.
INSERT INTO journal_lines (
    entry_id, line_no, account_code, side, amount, memo, branch_id, tags
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (entry_id, line_no) DO NOTHING;

-- name: GetLastSequence :one
SELECT last_sequence FROM subject_offsets WHERE subject = $1;

-- name: UpsertLastSequence :exec
INSERT INTO subject_offsets (subject, last_sequence)
VALUES ($1, $2)
ON CONFLICT (subject) DO UPDATE
SET last_sequence = GREATEST(subject_offsets.last_sequence, EXCLUDED.last_sequence);
