-- 009_access_key_limits.sql
-- Bound blast radius when a bearer key leaks. The original access_keys table
-- only supported manual revocation, with no expiry or per-key request budget.
ALTER TABLE access_keys
  ADD COLUMN expires_at TEXT;

ALTER TABLE access_keys
  ADD COLUMN rpm_limit INTEGER NOT NULL DEFAULT 0
    CHECK (rpm_limit BETWEEN 0 AND 100000);

ALTER TABLE access_keys
  ADD COLUMN tpm_limit INTEGER NOT NULL DEFAULT 0
    CHECK (tpm_limit BETWEEN 0 AND 1000000000);

ALTER TABLE access_keys
  ADD COLUMN max_concurrent INTEGER NOT NULL DEFAULT 0
    CHECK (max_concurrent BETWEEN 0 AND 10000);
