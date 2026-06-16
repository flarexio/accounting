DROP INDEX IF EXISTS accounts_embedding_cosine_idx;
ALTER TABLE accounts DROP COLUMN IF EXISTS embedding;
-- vector extension is shared infrastructure; leave it installed.
