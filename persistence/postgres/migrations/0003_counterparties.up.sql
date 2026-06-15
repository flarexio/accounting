-- Customer/supplier master data. Referenced by AR/AP postings (added later);
-- here it is plain reference data projected from CounterpartyAdded, mirroring
-- accounts/branches. TaxID is the Taiwan 統一編號; aliases enrich lexical lookup.

CREATE TABLE counterparties (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    tax_id      TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL,
    aliases     TEXT[] NOT NULL DEFAULT '{}',
    description TEXT NOT NULL DEFAULT ''
);
