ALTER TABLE journal_relations
    ADD COLUMN amount BIGINT NOT NULL DEFAULT 0,
    ADD CONSTRAINT journal_relations_amount_check CHECK (amount >= 0);
