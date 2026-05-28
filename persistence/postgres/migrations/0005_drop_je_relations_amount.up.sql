-- 0005_drop_je_relations_amount.up.sql
--
-- Drop the amount column on journal_relations. It was reserved in #15 for
-- future partial relations (partial reversal, partial settlement) but the
-- validator always rejected non-zero values, and the int64-on-the-relation
-- shape is the wrong granularity for real partials -- partial cases need
-- line-level references, not entry-level. Removing the speculative field
-- avoids signalling a design we have not committed to.

ALTER TABLE journal_relations DROP CONSTRAINT IF EXISTS journal_relations_amount_check;
ALTER TABLE journal_relations DROP COLUMN amount;
