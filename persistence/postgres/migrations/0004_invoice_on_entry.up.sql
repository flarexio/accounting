-- Posting-time counterparty + source-document fields. counterparty_id attributes
-- a line to a customer/supplier (AR/AP aging); source_kind/source_number record
-- the invoice or receipt the entry came from. All default to '' so existing rows
-- and non-AR/AP postings are unaffected.

ALTER TABLE journal_lines
    ADD COLUMN counterparty_id TEXT NOT NULL DEFAULT '';

ALTER TABLE journal_entries
    ADD COLUMN source_kind   TEXT NOT NULL DEFAULT '',
    ADD COLUMN source_number TEXT NOT NULL DEFAULT '';
