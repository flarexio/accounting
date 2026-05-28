-- 0003_je_relations.up.sql
--
-- Structural reversal/correction tracking. A JournalRelation links one posted
-- entry (from_entry) to another (to_entry); it is append-only and identified
-- by the composite key (from_entry, to_entry). Different relation kinds
-- (reverses, corrects, settles, closes, adjusts) share this one table so a
-- new business operation does not require a new schema.
--
-- Apply writes the entry, its lines, and every relation in one transaction;
-- the foreign keys ensure a relation can never reference a missing entry.

CREATE TABLE journal_relations (
    from_entry TEXT NOT NULL REFERENCES journal_entries(id),
    to_entry   TEXT NOT NULL REFERENCES journal_entries(id),
    type       TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    amount     BIGINT NOT NULL DEFAULT 0,
    note       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (from_entry, to_entry),
    CHECK (from_entry <> to_entry),
    CHECK (amount >= 0)
);

CREATE INDEX journal_relations_to_entry_idx ON journal_relations (to_entry);
CREATE INDEX journal_relations_type_idx     ON journal_relations (type);
