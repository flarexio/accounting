-- pgvector extension and the accounts.embedding column used by FindAccounts
-- for semantic similarity on the chart of accounts. Kept out of sqlc on
-- purpose: pgvector reads/writes are hand-rolled SQL in the postgres adapter.
-- Dimension matches OpenAI text-embedding-3-small (1536).

CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE accounts ADD COLUMN embedding vector(1536);

CREATE INDEX IF NOT EXISTS accounts_embedding_cosine_idx
    ON accounts USING hnsw (embedding vector_cosine_ops);
