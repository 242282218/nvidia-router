-- 021_provider_credentials.sql
-- First step of multi-provider support: a model now names the upstream provider
-- it belongs to, and a provider_credentials table stores OpenAI-compatible
-- upstream endpoints (base URL + encrypted API token) separate from the NVIDIA
-- key store. Adding columns defaulting to "nvidia" keeps every existing model
-- and deployment on the NVIDIA provider unchanged.
--
-- provider_credentials mirrors the encrypted-at-rest shape of nvidia_keys so
-- the same crypto discipline (AES-GCM under the router master key, AAD-bound to
-- the provider) applies. No plaintext token ever touches the database.

ALTER TABLE models
  ADD COLUMN provider TEXT NOT NULL DEFAULT 'nvidia'
    CHECK (provider IN ('nvidia', 'openai_compatible'));

CREATE TABLE provider_credentials (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,          -- stable provider route name, e.g. "siliconflow"
  provider TEXT NOT NULL DEFAULT 'openai_compatible'
    CHECK (provider IN ('openai_compatible')),
  base_url TEXT NOT NULL,             -- e.g. https://api.siliconflow.cn/v1
  ciphertext BLOB NOT NULL,
  nonce BLOB NOT NULL,
  fingerprint BLOB NOT NULL UNIQUE,
  display_prefix TEXT NOT NULL,
  display_suffix TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  cooldown_until TEXT,
  cooldown_level INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  key_version INTEGER NOT NULL DEFAULT 1 CHECK (key_version > 0),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
