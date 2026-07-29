CREATE INDEX idx_admin_sessions_expires ON admin_sessions(expires_at) WHERE revoked_at IS NULL;
CREATE INDEX idx_nvidia_keys_schedulable ON nvidia_keys(enabled, auth_invalid, cooldown_until);
CREATE INDEX idx_models_enabled_kind ON models(enabled, kind);
CREATE INDEX idx_key_model_blocks_model ON nvidia_key_model_blocks(model_id, nvidia_key_id);
CREATE INDEX idx_access_keys_active ON access_keys(key_digest) WHERE revoked_at IS NULL;
CREATE INDEX idx_request_logs_created ON request_logs(created_at);
CREATE INDEX idx_request_logs_model_created ON request_logs(model_id, created_at);
CREATE INDEX idx_request_logs_nvidia_key_created ON request_logs(nvidia_key_id, created_at);
CREATE INDEX idx_request_logs_access_key_created ON request_logs(access_key_id, created_at);
