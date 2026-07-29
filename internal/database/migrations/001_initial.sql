CREATE TABLE crypto_sentinel (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  version INTEGER NOT NULL,
  nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL
);

CREATE TABLE admins (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  must_change_password INTEGER NOT NULL DEFAULT 1 CHECK (must_change_password IN (0, 1)),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE admin_sessions (
  id TEXT PRIMARY KEY,
  token_digest BLOB NOT NULL UNIQUE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  revoked_at TEXT
);

CREATE TABLE nvidia_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  fingerprint BLOB NOT NULL UNIQUE,
  display_prefix TEXT NOT NULL,
  display_suffix TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  auth_invalid INTEGER NOT NULL DEFAULT 0 CHECK (auth_invalid IN (0, 1)),
  cooldown_until TEXT,
  cooldown_reason TEXT,
  cooldown_level INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  last_success_at TEXT,
  last_error_at TEXT,
  last_error_code TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE models (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  public_id TEXT NOT NULL UNIQUE,
  upstream_id TEXT NOT NULL,
  display_name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('chat', 'embedding', 'asr', 'tts')),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  supports_vision INTEGER NOT NULL DEFAULT 0 CHECK (supports_vision IN (0, 1)),
  supports_tools INTEGER NOT NULL DEFAULT 0 CHECK (supports_tools IN (0, 1)),
  supports_reasoning INTEGER NOT NULL DEFAULT 0 CHECK (supports_reasoning IN (0, 1)),
  reasoning_wire_format TEXT NOT NULL DEFAULT 'none' CHECK (reasoning_wire_format IN ('none', 'openai')),
  capability_verified_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE nvidia_key_model_blocks (
  nvidia_key_id INTEGER NOT NULL REFERENCES nvidia_keys(id) ON DELETE CASCADE,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  reason_code TEXT NOT NULL,
  upstream_status INTEGER,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (nvidia_key_id, model_id)
);

CREATE TABLE access_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  key_digest BLOB NOT NULL UNIQUE,
  key_prefix TEXT NOT NULL,
  created_at TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at TEXT
);

CREATE TABLE runtime_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  queue_capacity INTEGER NOT NULL DEFAULT 100 CHECK (queue_capacity BETWEEN 1 AND 10000),
  queue_wait_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (queue_wait_timeout_ms BETWEEN 1000 AND 600000),
  connect_timeout_ms INTEGER NOT NULL DEFAULT 10000 CHECK (connect_timeout_ms BETWEEN 1000 AND 120000),
  first_byte_timeout_ms INTEGER NOT NULL DEFAULT 60000 CHECK (first_byte_timeout_ms BETWEEN 1000 AND 600000),
  nonstream_total_timeout_ms INTEGER NOT NULL DEFAULT 300000 CHECK (nonstream_total_timeout_ms BETWEEN 1000 AND 1800000),
  shutdown_grace_ms INTEGER NOT NULL DEFAULT 60000 CHECK (shutdown_grace_ms BETWEEN 1000 AND 600000),
  updated_at TEXT NOT NULL
);

INSERT INTO runtime_settings (
  id, queue_capacity, queue_wait_timeout_ms, connect_timeout_ms,
  first_byte_timeout_ms, nonstream_total_timeout_ms, shutdown_grace_ms, updated_at
) VALUES (1, 100, 60000, 10000, 60000, 300000, 60000, '1970-01-01T00:00:00Z');

CREATE TABLE request_logs (
  request_id TEXT PRIMARY KEY,
  endpoint TEXT NOT NULL,
  model_id TEXT,
  access_key_id INTEGER REFERENCES access_keys(id) ON DELETE SET NULL,
  nvidia_key_id INTEGER REFERENCES nvidia_keys(id) ON DELETE SET NULL,
  http_status INTEGER NOT NULL,
  outcome TEXT NOT NULL,
  error_code TEXT,
  is_stream INTEGER NOT NULL CHECK (is_stream IN (0, 1)),
  queue_ms INTEGER NOT NULL DEFAULT 0,
  first_byte_ms INTEGER,
  duration_ms INTEGER NOT NULL,
  attempt_count INTEGER NOT NULL,
  prompt_tokens INTEGER,
  completion_tokens INTEGER,
  upstream_request_id TEXT,
  created_at TEXT NOT NULL
);

CREATE TABLE daily_stats (
  day TEXT NOT NULL,
  dimension_type TEXT NOT NULL CHECK (dimension_type IN ('global', 'model', 'nvidia_key', 'access_key')),
  dimension_id TEXT NOT NULL,
  request_count INTEGER NOT NULL DEFAULT 0,
  success_count INTEGER NOT NULL DEFAULT 0,
  failure_count INTEGER NOT NULL DEFAULT 0,
  total_duration_ms INTEGER NOT NULL DEFAULT 0,
  total_queue_ms INTEGER NOT NULL DEFAULT 0,
  total_attempts INTEGER NOT NULL DEFAULT 0,
  prompt_tokens INTEGER NOT NULL DEFAULT 0,
  completion_tokens INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (day, dimension_type, dimension_id)
);
