-- Persist only the normalized capability names requested by the client. Request
-- and response bodies, tool definitions, images, and reasoning values remain
-- intentionally outside observability storage.
ALTER TABLE request_logs ADD COLUMN requested_capabilities TEXT;
