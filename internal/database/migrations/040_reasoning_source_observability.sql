-- Keep whether reasoning was requested by the client or injected by the
-- router. Only the source label is persisted; no reasoning values are stored.
ALTER TABLE request_logs ADD COLUMN reasoning_source TEXT;
