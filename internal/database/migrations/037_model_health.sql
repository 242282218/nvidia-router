-- Persisted active model health probes are intentionally separate from
-- request_logs: synthetic checks must never change request success metrics.
CREATE TABLE model_health_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  interval_seconds INTEGER NOT NULL DEFAULT 60 CHECK (interval_seconds BETWEEN 10 AND 3600),
  concurrency INTEGER NOT NULL DEFAULT 2 CHECK (concurrency BETWEEN 1 AND 8),
  updated_at TEXT NOT NULL
);

INSERT INTO model_health_settings (id, enabled, interval_seconds, concurrency, updated_at)
VALUES (1, 0, 60, 2, '1970-01-01T00:00:00Z');

CREATE TABLE model_health_probes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure', 'timeout', 'skipped', 'canceled')),
  duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
  error_code TEXT,
  created_at TEXT NOT NULL
);

CREATE INDEX idx_model_health_probes_model_created
  ON model_health_probes(model_id, created_at);
CREATE INDEX idx_model_health_probes_created
  ON model_health_probes(created_at);

CREATE TABLE model_health_latest (
  model_id INTEGER PRIMARY KEY REFERENCES models(id) ON DELETE CASCADE,
  outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure', 'timeout', 'skipped', 'canceled')),
  duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
  error_code TEXT,
  last_probe_at TEXT NOT NULL,
  consecutive_failures INTEGER NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0)
);
