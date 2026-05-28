-- 0004_companies_retained_earnings.up.sql
--
-- ClosePeriod plugs net income into the company's Retained Earnings account;
-- this column names which account code that is. Empty means closing is not
-- configured for the company.

ALTER TABLE companies ADD COLUMN retained_earnings_code TEXT NOT NULL DEFAULT '';
