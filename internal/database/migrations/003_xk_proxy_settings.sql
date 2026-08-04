CREATE TABLE xk_proxy_settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  enabled INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  ttl_seconds INTEGER NOT NULL CHECK (ttl_seconds BETWEEN 30 AND 1800),
  renew_before_seconds INTEGER NOT NULL CHECK (renew_before_seconds > 0 AND renew_before_seconds < ttl_seconds),
  version INTEGER NOT NULL,
  nonce BLOB,
  ciphertext BLOB,
  updated_at TEXT NOT NULL,
  CHECK ((nonce IS NULL AND ciphertext IS NULL) OR (nonce IS NOT NULL AND ciphertext IS NOT NULL)),
  CHECK (enabled = 0 OR ciphertext IS NOT NULL)
);
