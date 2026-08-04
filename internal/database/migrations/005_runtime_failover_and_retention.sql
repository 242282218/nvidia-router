-- 005_runtime_failover_and_retention.sql
-- B4/B5 of the gpt-load comparison plan: expose operator-tunable failover
-- status codes and make request-log retention configurable rather than the
-- previously hardcoded 30-day value.

ALTER TABLE runtime_settings
  ADD COLUMN failover_status_codes TEXT NOT NULL DEFAULT '429,500,502,503,504';

ALTER TABLE runtime_settings
  ADD COLUMN request_log_retention_days INTEGER NOT NULL DEFAULT 30
    CHECK (request_log_retention_days BETWEEN 1 AND 365);
