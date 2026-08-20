-- Backfill reasoning metadata for NVIDIA models observed emitting
-- reasoning_content despite being seeded as non-reasoning chat models.
-- NVIDIA's native reasoning transport is the thinking object; keep the
-- OpenCodeFree provider rows untouched.

UPDATE models
SET supports_reasoning = 1,
    reasoning_wire_format = 'thinking',
    reasoning_levels = '["none","auto","minimal","low","medium","high","xhigh","max"]',
    reasoning_min_budget = 0,
    reasoning_max_budget = 128000,
    reasoning_zero_allowed = 1,
    reasoning_dynamic_allowed = 1
WHERE provider = 'nvidia'
  AND upstream_id IN (
    'nvidia/nemotron-3-ultra-550b-a55b',
    'stepfun-ai/step-3.7-flash'
  );
