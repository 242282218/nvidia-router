-- 016_model_stream_timeouts.sql
-- Add per-model streaming timeout overrides to the models table.
--
-- When non-null, these columns override the global runtime_settings values for
-- the matching request. A null value means "use the global setting", so existing
-- rows and newly added models continue to behave exactly as before.
--
-- deepseek-v4-flash is pre-seeded with 300 000 ms (5 minutes) for both windows:
--   • stream_first_token_timeout_ms: DeepSeek flash TTFT routinely reaches
--     60-180s on NVIDIA's infrastructure; 300s provides headroom without
--     pinning the lease indefinitely.
--   • stream_idle_timeout_ms: once the first token arrives the generation
--     is fast, but a generous idle window absorbs occasional inter-chunk
--     silences that can follow slow reasoning steps.
--
-- The ON CONFLICT guard ensures an operator who already adjusted these values
-- via a future admin surface is never silently overwritten by the migration.

ALTER TABLE models
  ADD COLUMN stream_first_token_timeout_ms INTEGER
    CHECK (stream_first_token_timeout_ms IS NULL
        OR stream_first_token_timeout_ms BETWEEN 1000 AND 1800000);

ALTER TABLE models
  ADD COLUMN stream_idle_timeout_ms INTEGER
    CHECK (stream_idle_timeout_ms IS NULL
        OR stream_idle_timeout_ms BETWEEN 1000 AND 1800000);

-- Seed deepseek-v4-flash with generous per-model windows.
-- ON CONFLICT(public_id) targets the unique index on public_id.
UPDATE models
  SET stream_first_token_timeout_ms = 300000,
      stream_idle_timeout_ms        = 300000
WHERE public_id = 'deepseek-v4-flash'
  AND stream_first_token_timeout_ms IS NULL
  AND stream_idle_timeout_ms        IS NULL;
