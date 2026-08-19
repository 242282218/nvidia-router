-- P1-8: Fix 035 partial indexes that were no-ops.
-- 035 used the SAME names as the 002 full indexes (idx_request_logs_model_created /
-- idx_request_logs_access_created). CREATE INDEX IF NOT EXISTS with an existing
-- name is a no-op, so the partial (WHERE ... IS NOT NULL) variants never took
-- effect and model_id/access_key_id time-window monitoring filters fell back to
-- full-window scans. New names + partial predicates here; the 002 full indexes
-- are kept to also cover NULL-valued rows (e.g. nvidia-key-only queries).
CREATE INDEX IF NOT EXISTS idx_request_logs_model_created_v2 ON request_logs(model_id, created_at) WHERE model_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_request_logs_access_created_v2 ON request_logs(access_key_id, created_at) WHERE access_key_id IS NOT NULL;