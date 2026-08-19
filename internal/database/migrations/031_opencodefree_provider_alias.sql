-- Accept the task provider spelling alongside the production provider spelling.
-- SQLite requires rebuilding the table to change a CHECK constraint, so copy
-- every model and key-model block column before recreating the original names.

CREATE TABLE nvidia_key_model_blocks_029 (
  nvidia_key_id INTEGER NOT NULL,
  model_id INTEGER NOT NULL,
  reason_code TEXT NOT NULL,
  upstream_status INTEGER,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (nvidia_key_id, model_id)
);

INSERT INTO nvidia_key_model_blocks_029 (
  nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
)
SELECT nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
FROM nvidia_key_model_blocks;

CREATE TABLE models_029 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  public_id TEXT NOT NULL UNIQUE,
  upstream_id TEXT NOT NULL,
  display_name TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('chat', 'embedding', 'asr', 'tts')),
  provider TEXT NOT NULL DEFAULT 'nvidia' CHECK (provider IN ('nvidia', 'openai_compatible', 'opencode_free', 'opencodefree')),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  supports_vision INTEGER NOT NULL DEFAULT 0 CHECK (supports_vision IN (0, 1)),
  supports_tools INTEGER NOT NULL DEFAULT 0 CHECK (supports_tools IN (0, 1)),
  supports_reasoning INTEGER NOT NULL DEFAULT 0 CHECK (supports_reasoning IN (0, 1)),
  reasoning_wire_format TEXT NOT NULL DEFAULT 'none' CHECK (reasoning_wire_format IN ('none', 'openai', 'thinking')),
  reasoning_levels TEXT NOT NULL DEFAULT '["none","auto","minimal","low","medium","high","xhigh","max"]' CHECK (json_valid(reasoning_levels)),
  reasoning_min_budget INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_min_budget >= 0),
  reasoning_max_budget INTEGER NOT NULL DEFAULT 128000 CHECK (reasoning_max_budget >= reasoning_min_budget),
  reasoning_zero_allowed INTEGER NOT NULL DEFAULT 1 CHECK (reasoning_zero_allowed IN (0, 1)),
  reasoning_dynamic_allowed INTEGER NOT NULL DEFAULT 1 CHECK (reasoning_dynamic_allowed IN (0, 1)),
  capability_verified_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  stream_first_token_timeout_ms INTEGER CHECK (stream_first_token_timeout_ms IS NULL OR stream_first_token_timeout_ms BETWEEN 1000 AND 1800000),
  stream_idle_timeout_ms INTEGER CHECK (stream_idle_timeout_ms IS NULL OR stream_idle_timeout_ms BETWEEN 1000 AND 1800000),
  input_usd_per_mtok REAL CHECK (input_usd_per_mtok IS NULL OR input_usd_per_mtok >= 0),
  output_usd_per_mtok REAL CHECK (output_usd_per_mtok IS NULL OR output_usd_per_mtok >= 0)
);

INSERT INTO models_029 (
  id, public_id, upstream_id, display_name, kind, provider, enabled,
  supports_vision, supports_tools, supports_reasoning, reasoning_wire_format,
  reasoning_levels, reasoning_min_budget, reasoning_max_budget,
  reasoning_zero_allowed, reasoning_dynamic_allowed, capability_verified_at,
  created_at, updated_at, stream_first_token_timeout_ms, stream_idle_timeout_ms,
  input_usd_per_mtok, output_usd_per_mtok
)
SELECT id, public_id, upstream_id, display_name, kind, provider, enabled,
  supports_vision, supports_tools, supports_reasoning, reasoning_wire_format,
  reasoning_levels, reasoning_min_budget, reasoning_max_budget,
  reasoning_zero_allowed, reasoning_dynamic_allowed, capability_verified_at,
  created_at, updated_at, stream_first_token_timeout_ms, stream_idle_timeout_ms,
  input_usd_per_mtok, output_usd_per_mtok
FROM models;

DROP TABLE nvidia_key_model_blocks;
DROP TABLE models;
ALTER TABLE models_029 RENAME TO models;

CREATE TABLE nvidia_key_model_blocks (
  nvidia_key_id INTEGER NOT NULL REFERENCES nvidia_keys(id) ON DELETE CASCADE,
  model_id INTEGER NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  reason_code TEXT NOT NULL,
  upstream_status INTEGER,
  first_seen_at TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  PRIMARY KEY (nvidia_key_id, model_id)
);

INSERT INTO nvidia_key_model_blocks (
  nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
)
SELECT nvidia_key_id, model_id, reason_code, upstream_status, first_seen_at, last_seen_at
FROM nvidia_key_model_blocks_029;

CREATE INDEX idx_models_enabled_kind ON models(enabled, kind);
CREATE INDEX idx_key_model_blocks_model ON nvidia_key_model_blocks(model_id, nvidia_key_id);

DROP TABLE nvidia_key_model_blocks_029;

UPDATE models
SET provider = 'opencodefree'
WHERE provider = 'opencode_free';
