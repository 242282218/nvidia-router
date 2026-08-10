-- 014_stream_timeouts.sql
-- Split the old first_byte_timeout_ms window for streaming requests into two
-- operator-tunable budgets. Before this, the first-byte window doubled as the
-- inter-token idle guard, so a model that took long between tokens after a fast
-- first token (or vice versa) could only be configured as one number.
--
-- stream_first_token_timeout_ms bounds the pre-commit wait for the first SSE
-- data event (TTFT). stream_idle_timeout_ms bounds the silence between data
-- events once the stream is committed, so a slow-but-live generation is not
-- truncated while a stalled upstream still cannot pin the lease forever.

ALTER TABLE runtime_settings
  ADD COLUMN stream_first_token_timeout_ms INTEGER NOT NULL DEFAULT 60000
    CHECK (stream_first_token_timeout_ms BETWEEN 1000 AND 1800000);

ALTER TABLE runtime_settings
  ADD COLUMN stream_idle_timeout_ms INTEGER NOT NULL DEFAULT 180000
    CHECK (stream_idle_timeout_ms BETWEEN 1000 AND 1800000);
