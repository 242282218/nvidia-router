-- Accelerate monitoring and request-log queries that filter by
-- endpoint / outcome / HTTP status together with a created_at window.
-- Existing indexes cover created_at alone and model/access/nvidia + created_at,
-- but monitoring queries also filter on endpoint and outcome (see
-- observability.monitoring_repository.go:requestLogWhere), which previously fell
-- back to full table scans on large request_logs. Composite indexes let SQLite
-- narrow by the predicate first then range-scan by time.
CREATE INDEX IF NOT EXISTS idx_request_logs_endpoint_created ON request_logs(endpoint, created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_outcome_created ON request_logs(outcome, created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_status_created ON request_logs(http_status, created_at);
CREATE INDEX IF NOT EXISTS idx_request_logs_endpoint_outcome_created ON request_logs(endpoint, outcome, created_at);
-- TTFT percentile queries scan first_token_ms with the same window predicate.
-- A partial index on non-null samples avoids scanning rows that have no TTFT.
CREATE INDEX IF NOT EXISTS idx_request_logs_first_token ON request_logs(first_token_ms, created_at) WHERE first_token_ms IS NOT NULL;
