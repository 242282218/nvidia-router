CREATE TABLE proxy_pool_settings_v025 (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  proxy_url TEXT NOT NULL DEFAULT '',
  auth_key_nonce BLOB,
  auth_key_ciphertext BLOB,
  upstream_url_nonce BLOB,
  upstream_url_ciphertext BLOB,
  pool_config TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL DEFAULT 1,
  key_version INTEGER NOT NULL DEFAULT 1 CHECK (key_version > 0),
  updated_at TEXT NOT NULL,
  CHECK ((auth_key_nonce IS NULL AND auth_key_ciphertext IS NULL)
      OR (auth_key_nonce IS NOT NULL AND auth_key_ciphertext IS NOT NULL)),
  CHECK ((upstream_url_nonce IS NULL AND upstream_url_ciphertext IS NULL)
      OR (upstream_url_nonce IS NOT NULL AND upstream_url_ciphertext IS NOT NULL)),
  CHECK (enabled = 0 OR (proxy_url <> '' AND
    (proxy_url = '__built_in_xk_pool__'
      OR (proxy_url <> '__built_in_xk_pool__' AND auth_key_ciphertext IS NOT NULL))))
);

INSERT INTO proxy_pool_settings_v025 (
  id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext,
  pool_config, version, key_version, updated_at
)
SELECT
  id, enabled, proxy_url, auth_key_nonce, auth_key_ciphertext,
  pool_config, version, key_version, updated_at
FROM proxy_pool_settings;

DROP TABLE proxy_pool_settings;
ALTER TABLE proxy_pool_settings_v025 RENAME TO proxy_pool_settings;
