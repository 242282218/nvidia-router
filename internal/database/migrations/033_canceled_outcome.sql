-- 033_canceled_outcome.sql
-- Separate client-cancel (HTTP 499) from hard failures so TTFT-driven 499s
-- do not drag the success rate down (2026-08-19 P2). The new column is only
-- populated for outcome='canceled' rows; historical rows keep 0 and the
-- application-level outcomeCounts already stops billing 499 as failure.
ALTER TABLE daily_stats ADD COLUMN canceled_count INTEGER NOT NULL DEFAULT 0;

-- Index outcome=canceled for the separate 499 dimension in monitoring.
CREATE INDEX IF NOT EXISTS idx_request_logs_canceled_created ON request_logs(outcome, created_at) WHERE outcome = 'canceled';
