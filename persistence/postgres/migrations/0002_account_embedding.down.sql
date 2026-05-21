DROP INDEX IF EXISTS accounts_embedding_cosine_idx;
ALTER TABLE accounts DROP COLUMN IF EXISTS embedding;
-- Don't drop the extension; another table or app may rely on it.
