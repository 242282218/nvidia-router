-- 017_admin_audit_logs.sql
-- Immutable operator trail for every mutating admin action.
--
-- Before this migration the admin surface recorded neither who performed a
-- management operation nor from where; request_logs only covers /v1 data
-- traffic. This table gives operators a tamper-evidence trail of management
-- changes (key imports, policy edits, settings changes, auth events) for
-- incident review and compliance.
--
-- detail holds a compact JSON document describing the mutation without raw
-- secrets: NVIDIA key material and access-key plaintexts must never reach it.

CREATE TABLE admin_audit_logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  action      TEXT NOT NULL,      -- e.g. "nvidia_key.import" | "access_key.revoke"
  target_type TEXT NOT NULL DEFAULT '',
  target_id   TEXT NOT NULL DEFAULT '',
  detail      TEXT NOT NULL DEFAULT '',   -- compact JSON, secrets-free
  session_id  TEXT,               -- admin session that performed the action (NULL for pre-session auth events)
  client_ip   TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);

CREATE INDEX idx_admin_audit_logs_created_at ON admin_audit_logs(created_at);
CREATE INDEX idx_admin_audit_logs_action ON admin_audit_logs(action, created_at);
