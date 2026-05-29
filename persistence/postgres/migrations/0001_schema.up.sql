-- Initial projection schema for accounting.LedgerRepository.
--
-- Journal tables are append-only: written once by the JournalPosted handler.
-- Seeded accounts/branches/companies/periods are mutated only by the seeder.
-- Business dates (entry_date, period boundaries) are DATE in the company's
-- timezone; posted_at is a real instant (TIMESTAMPTZ).

CREATE TABLE accounts (
    code   TEXT PRIMARY KEY,
    name   TEXT NOT NULL,
    type   TEXT NOT NULL,
    active BOOLEAN NOT NULL
);

CREATE TABLE branches (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE companies (
    id                     TEXT PRIMARY KEY,
    name                   TEXT NOT NULL,
    timezone               TEXT NOT NULL,
    -- Retained Earnings account code ClosePeriod plugs net income into; empty disables closing.
    retained_earnings_code TEXT NOT NULL DEFAULT ''
);

CREATE TABLE periods (
    id       TEXT PRIMARY KEY,
    start_on DATE NOT NULL,
    end_on   DATE NOT NULL,
    status   TEXT NOT NULL
);

-- Per-subject high-water sequence reported by LastSequence, advanced in the
-- same transaction that inserts the entry so a reader never sees an entry
-- without its sequence.
CREATE TABLE subject_offsets (
    subject       TEXT PRIMARY KEY,
    last_sequence BIGINT NOT NULL
);

CREATE TABLE journal_entries (
    id          TEXT PRIMARY KEY,
    sequence    BIGINT NOT NULL,
    subject     TEXT NOT NULL,
    entry_date  DATE NOT NULL,
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

-- Append-only links between posted entries, keyed by (from_entry, to_entry).
-- Relation kinds (reverses, corrects, settles, closes, adjusts) share the
-- table so a new operation needs no new schema.
CREATE TABLE journal_relations (
    from_entry TEXT NOT NULL REFERENCES journal_entries(id),
    to_entry   TEXT NOT NULL REFERENCES journal_entries(id),
    type       TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    note       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (from_entry, to_entry),
    CHECK (from_entry <> to_entry)
);

CREATE INDEX journal_relations_to_entry_idx ON journal_relations (to_entry);
CREATE INDEX journal_relations_type_idx     ON journal_relations (type);
