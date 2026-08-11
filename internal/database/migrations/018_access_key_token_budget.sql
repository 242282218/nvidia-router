-- 018_access_key_token_budget.sql
-- Add a cumulative token budget to access keys. Unlike rpm_limit/tpm_limit
-- (sliding 1-minute windows), the budget caps the total tokens an access key
-- may ever consume; once consumed_tokens reaches token_budget the key stops
-- serving further requests until an operator raises the budget or resets it.
--
-- token_budget 0 means "unlimited" (the historical behaviour), so existing
-- keys and newly created keys are unaffected.

ALTER TABLE access_keys
  ADD COLUMN token_budget INTEGER NOT NULL DEFAULT 0
    CHECK (token_budget BETWEEN 0 AND 1000000000000);

ALTER TABLE access_keys
  ADD COLUMN consumed_tokens INTEGER NOT NULL DEFAULT 0
    CHECK (consumed_tokens BETWEEN 0 AND 1000000000000);
