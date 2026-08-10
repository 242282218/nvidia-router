-- 010_master_key_versions.sql
-- Key generation metadata is not secret; ciphertext and digests remain in their
-- existing columns so old databases can be upgraded without rewriting secrets.
ALTER TABLE crypto_sentinel
  ADD COLUMN key_version INTEGER NOT NULL DEFAULT 1
    CHECK (key_version > 0);

ALTER TABLE nvidia_keys
  ADD COLUMN key_version INTEGER NOT NULL DEFAULT 1
    CHECK (key_version > 0);

ALTER TABLE proxy_pool_settings
  ADD COLUMN key_version INTEGER NOT NULL DEFAULT 1
    CHECK (key_version > 0);

ALTER TABLE access_keys
  ADD COLUMN digest_key_version INTEGER NOT NULL DEFAULT 1
    CHECK (digest_key_version > 0);

ALTER TABLE admin_sessions
  ADD COLUMN digest_key_version INTEGER NOT NULL DEFAULT 1
    CHECK (digest_key_version > 0);
