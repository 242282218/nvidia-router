-- Apply the code-level OpenCodeFree family hints to rows that were already
-- saved before capability-aware candidate discovery existed. Probe-confirmed
-- statuses are preserved so a later release cannot erase a real verdict.
UPDATE models
SET supports_reasoning = 1,
    supports_tools = 1,
    reasoning_status = 'inferred',
    reasoning_wire_format = 'openai',
    reasoning_levels = '["none","auto","minimal","low","medium","high","xhigh","max"]',
    reasoning_min_budget = 0,
    reasoning_max_budget = 128000,
    reasoning_zero_allowed = 1,
    reasoning_dynamic_allowed = 1
WHERE provider = 'opencodefree'
  AND reasoning_status IN ('unknown', 'inferred')
  AND (
    upstream_id LIKE 'deepseek-%'
    OR upstream_id LIKE 'glm-%'
    OR upstream_id LIKE 'minimax-%'
    OR upstream_id LIKE 'kimi-%'
    OR upstream_id LIKE 'qwen3.%'
    OR upstream_id LIKE 'gemini-3%'
    OR upstream_id LIKE 'gpt-5%'
    OR upstream_id LIKE 'grok-4%'
  );
