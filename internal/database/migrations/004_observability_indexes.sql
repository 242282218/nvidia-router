-- Normalize historical request_logs timestamps to the fixed-width millisecond
-- format (2006-01-02T15:04:05.000Z) so TEXT comparisons and sorts stay
-- chronological. Legacy values were written as RFC3339Nano, whose variable
-- fractional width breaks lexicographic order (e.g. ".9Z" vs ".95Z").
UPDATE request_logs
SET created_at = strftime('%Y-%m-%dT%H:%M:%fZ', created_at)
WHERE created_at IS NOT NULL;

-- Support ListRecentErrors (outcome + error_code predicate, created_at sort)
-- without falling back to a full table scan.
CREATE INDEX idx_request_logs_outcome_error_created ON request_logs(outcome, error_code, created_at);
