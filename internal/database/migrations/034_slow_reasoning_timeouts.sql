-- 034_slow_reasoning_timeouts.sql
-- Backfill per-model streaming timeout overrides for reasoning-heavy NVIDIA
-- models whose TTFT routinely exceeds the fleet-wide 60s/180s defaults.
-- Migration 022 covered only deepseek-v4-flash-0731; the 2026-08-19
-- stability analysis (P1-1) showed z-ai/glm-4.5, z-ai/glm-5.2 and
-- minimaxai/minimax-m3 also hit 60-90s first token and 499 when the client
-- uses a 60s deadline. Set both windows to 300s where the operator has not
-- already customized them (IS NULL guard).
--
-- The list is intentionally narrow (exact upstream_id matches) so a future
-- alias or a user-provided override is never overwritten.

UPDATE models
   SET stream_first_token_timeout_ms = 300000,
       stream_idle_timeout_ms        = 300000
 WHERE upstream_id IN (
       'z-ai/glm-4.5',
       'z-ai/glm-4.5v',
       'z-ai/glm-5.2',
       'minimaxai/minimax-m3',
       'minimaxai/minimax-m3-2509',
       'deepseek-ai/deepseek-v4',
       'deepseek-ai/deepseek-v4-0528',
       'deepseek-ai/deepseek-v3.1',
       'deepseek-ai/deepseek-v3.1-terminus'
 )
   AND stream_first_token_timeout_ms IS NULL
   AND stream_idle_timeout_ms        IS NULL;

-- Keep the deepseek alias from 022 at 300s even if an operator later nulled
-- it and re-ran migrations (the alias is the same upstream_id, but guard
-- against IS NULL already covers the normal path; this is a no-op for most
-- deployments).
UPDATE models
   SET stream_first_token_timeout_ms = 300000,
       stream_idle_timeout_ms        = 300000
 WHERE upstream_id = 'deepseek-ai/deepseek-v4-flash-0731'
   AND (stream_first_token_timeout_ms IS NULL OR stream_first_token_timeout_ms < 120000);
