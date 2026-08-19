-- P1-7: Optimize TTFT percentile queries.
-- queryFirstTokenPercentiles does COUNT(*) + LIMIT/OFFSET with ORDER BY first_token_ms
-- filtered by created_at window + first_token_ms IS NOT NULL. The 032 index
-- (first_token_ms, created_at) does not support the window predicate efficiently;
-- the leading column should be the range filter (created_at) so SQLite can
-- narrow by time window first, then sort the small subset. Partial index keeps
-- size small.
CREATE INDEX IF NOT EXISTS idx_request_logs_created_first_token ON request_logs(created_at, first_token_ms) WHERE first_token_ms IS NOT NULL;

-- Accelerate frequent monitoring filters that combine model_id + created_at.
-- Existing indexes cover some but not model_id alone with time window.
CREATE INDEX IF NOT EXISTS idx_request_logs_model_created ON request_logs(model_id, created_at) WHERE model_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_request_logs_access_created ON request_logs(access_key_id, created_at) WHERE access_key_id IS NOT NULL;
