-- 0001_journal_schema.up.sql
--
-- Initial projection schema for accounting.LedgerRepository.
--
-- The journal tables are append-only: rows are written exactly once by
-- the JournalPosted handler. Seeded chart-of-accounts, branches, and
-- periods are mutated only by the Scenario seeder, which is itself an
-- out-of-band operation.

CREATE TABLE accounts (
    code   TEXT PRIMARY KEY,
    name   TEXT NOT NULL,
    type   TEXT NOT NULL,
    active BOOLEAN NOT NULL
);

CREATE TABLE branches (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL
);

CREATE TABLE periods (
    id       TEXT PRIMARY KEY,
    start_at TIMESTAMPTZ NOT NULL,
    end_at   TIMESTAMPTZ NOT NULL,
    status   TEXT NOT NULL
);

-- subject_offsets carries the per-subject high-water sequence the
-- repository reports through LastSequence. The handler advances it in
-- the same transaction that inserts the entry, so a concurrent
-- LastSequence reader can never observe an entry without also observing
-- its sequence.
CREATE TABLE subject_offsets (
    subject       TEXT PRIMARY KEY,
    last_sequence BIGINT NOT NULL
);

CREATE TABLE journal_entries (
    id          TEXT PRIMARY KEY,
    sequence    BIGINT NOT NULL,
    subject     TEXT NOT NULL,
    entry_date  TIMESTAMPTZ NOT NULL,
    period_id   TEXT NOT NULL,
    currency    TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    posted_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (subject, sequence)
);

CREATE INDEX journal_entries_subject_seq_idx
    ON journal_entries (subject, sequence);

CREATE TABLE journal_lines (
    entry_id     TEXT   NOT NULL REFERENCES journal_entries(id) ON DELETE CASCADE,
    line_no      INT    NOT NULL,
    account_code TEXT   NOT NULL,
    side         TEXT   NOT NULL,
    amount       BIGINT NOT NULL,
    memo         TEXT   NOT NULL DEFAULT '',
    branch_id    TEXT   NOT NULL DEFAULT '',
    tags         JSONB,
    PRIMARY KEY (entry_id, line_no)
);
