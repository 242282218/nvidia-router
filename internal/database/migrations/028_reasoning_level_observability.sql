-- Persist only the normalized reasoning level labels; reasoning text and
-- request payloads remain intentionally absent from observability storage.
ALTER TABLE request_logs ADD COLUMN reasoning_requested_level TEXT;
ALTER TABLE request_logs ADD COLUMN reasoning_effective_level TEXT;
