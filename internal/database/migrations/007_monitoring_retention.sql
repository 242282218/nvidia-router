-- Keep request metadata available for at least one month while preserving
-- existing operator values that already fall inside the supported range.
CREATE TABLE runtime_settings_v7 (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  queue_capacity INTEGER NOT NULL DEFAULT 100 CHECK (queue_capacity BETWEEN 1 AND 10000),
  queue_wait_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (queue_wait_timeout_ms BETWEEN 1000 AND 600000),
  connect_timeout_ms INTEGER NOT NULL DEFAULT 10000 CHECK (connect_timeout_ms BETWEEN 1000 AND 120000),
  first_byte_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (first_byte_timeout_ms BETWEEN 1000 AND 600000),
  nonstream_total_timeout_ms INTEGER NOT NULL DEFAULT 300000 CHECK (nonstream_total_timeout_ms BETWEEN 1000 AND 1800000),
  shutdown_grace_ms INTEGER NOT NULL DEFAULT 60000 CHECK (shutdown_grace_ms BETWEEN 1000 AND 600000),
  updated_at TEXT NOT NULL,
  failover_status_codes TEXT NOT NULL DEFAULT '429,500,502,503,504',
  request_log_retention_days INTEGER NOT NULL DEFAULT 30 CHECK (request_log_retention_days BETWEEN 30 AND 365)
);

INSERT INTO runtime_settings_v7 (
  id, queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
  first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms,
  updated_at, failover_status_codes, request_log_retention_days
)
SELECT
  id, queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
  first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms,
  updated_at, failover_status_codes,
  CASE
    WHEN request_log_retention_days < 30 THEN 30
    WHEN request_log_retention_days > 365 THEN 365
    ELSE request_log_retention_days
  END
FROM runtime_settings;

DROP TABLE runtime_settings;
ALTER TABLE runtime_settings_v7 RENAME TO runtime_settings;

ALTER TABLE daily_stats ADD COLUMN total_first_byte_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE daily_stats ADD COLUMN first_byte_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX idx_request_logs_created_request ON request_logs(created_at DESC, request_id DESC);
CREATE INDEX idx_request_logs_created_model ON request_logs(created_at, model_id);
CREATE INDEX idx_request_logs_created_status ON request_logs(created_at, http_status, outcome);
CREATE INDEX idx_request_logs_created_access ON request_logs(created_at, access_key_id);
CREATE INDEX idx_request_logs_created_nvidia ON request_logs(created_at, nvidia_key_id);
