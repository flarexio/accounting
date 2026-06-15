ALTER TABLE journal_entries
    DROP COLUMN IF EXISTS source_kind,
    DROP COLUMN IF EXISTS source_number;

ALTER TABLE journal_lines
    DROP COLUMN IF EXISTS counterparty_id;
