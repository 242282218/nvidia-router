-- 022_deepseek_timeout_backfill.sql
-- Backfill per-model streaming timeout overrides for every public alias whose
-- upstream target is deepseek-v4-flash-0731, the reasoning flash model whose
-- TTFT on NVIDIA routinely reaches 60-180s.
--
-- Migration 016 seeded only the "deepseek-v4-flash" alias. The legacy row
-- "deepseek-ai/deepseek-v4-flash" (which predates the alias and resolves to the
-- same slow upstream) was left on the fleet-wide 60s default and therefore timed
-- out on first token. Keying on upstream_id covers both aliases and any future
-- alias an operator points at the same slow model, without overwriting an
-- operator's own override (the IS NULL guard).

UPDATE models
  SET stream_first_token_timeout_ms = 300000,
      stream_idle_timeout_ms        = 300000
WHERE upstream_id = 'deepseek-ai/deepseek-v4-flash-0731'
  AND stream_first_token_timeout_ms IS NULL
  AND stream_idle_timeout_ms        IS NULL;
