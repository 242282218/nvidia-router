-- TTFT (time to first token) for streaming requests. NULL until the first SSE
-- data event reaches the client; mirrors first_byte_ms semantics but only for
-- streams that actually produced a token.
ALTER TABLE request_logs ADD COLUMN first_token_ms INTEGER;

ALTER TABLE daily_stats ADD COLUMN total_first_token_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE daily_stats ADD COLUMN first_token_count INTEGER NOT NULL DEFAULT 0;
