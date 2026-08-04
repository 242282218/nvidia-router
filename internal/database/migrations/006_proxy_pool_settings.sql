CREATE TABLE proxy_pool_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  proxy_url TEXT NOT NULL DEFAULT '',
  auth_key_nonce BLOB,
  auth_key_ciphertext BLOB,
  version INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL,
  CHECK ((auth_key_nonce IS NULL AND auth_key_ciphertext IS NULL)
      OR (auth_key_nonce IS NOT NULL AND auth_key_ciphertext IS NOT NULL)),
  CHECK (enabled = 0 OR (proxy_url <> '' AND auth_key_ciphertext IS NOT NULL))
);
