-- 0002_pgvector.up.sql
--
-- pgvector extension and the accounts.embedding column used by FindAccounts
-- for semantic similarity search on the chart of accounts. Kept out of sqlc
-- on purpose: pgvector reads/writes are hand-rolled SQL in the postgres
-- adapter, so the sqlc-generated pgstore does not depend on pgvector-go.
-- Dimension matches OpenAI text-embedding-3-small (1536).

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE accounts ADD COLUMN embedding vector(1536);

-- HNSW index for cosine similarity. Only useful on larger charts but harmless
-- on small ones; build cost is negligible at seed time.
CREATE INDEX IF NOT EXISTS accounts_embedding_cosine_idx
    ON accounts USING hnsw (embedding vector_cosine_ops);
