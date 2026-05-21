-- Enable pgvector for semantic similarity search on the chart of accounts.
-- The dimension matches OpenAI text-embedding-3-small (1536).
CREATE EXTENSION IF NOT EXISTS vector;

ALTER TABLE accounts ADD COLUMN embedding vector(1536);

-- HNSW index for cosine similarity. Only useful on larger charts but harmless
-- on small ones; build cost is negligible at seed time.
CREATE INDEX IF NOT EXISTS accounts_embedding_cosine_idx
    ON accounts USING hnsw (embedding vector_cosine_ops);
