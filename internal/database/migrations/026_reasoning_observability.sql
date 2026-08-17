-- Reasoning observability (report §6.3): per-request metadata only. No
-- reasoning text, prompts, or key material is ever persisted; only booleans,
-- field names, and character counts.
ALTER TABLE request_logs ADD COLUMN reasoning_requested INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN reasoning_wire_fields TEXT;
ALTER TABLE request_logs ADD COLUMN reasoning_present INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN reasoning_chars INTEGER;
ALTER TABLE request_logs ADD COLUMN stream_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs ADD COLUMN route_mode TEXT;
