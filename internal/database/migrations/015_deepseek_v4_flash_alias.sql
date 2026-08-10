-- 015_deepseek_v4_flash_alias.sql
-- Seed the stable public alias "deepseek-v4-flash" for the NVIDIA upstream model
-- "deepseek-ai/deepseek-v4-flash-0731". NVIDIA versioned the flash ID with a date
-- suffix (the bare deepseek-v4-flash now answers 410 Gone), but the router wants to
-- expose one stable public model name that does not change when NVIDIA rotates the
-- suffix. The alias is a real row in the models whitelist, so it flows through the
-- same catalog resolution, capability checks and admin enable/disable as every other
-- model — no hardcoded request-path alias that could bypass the whitelist.
--
-- The migration is a no-op when an admin already configured their own public_id
-- "deepseek-v4-flash" with a different upstream target: ON CONFLICT keeps the existing
-- row so operator intent is never overwritten by a seed.

INSERT INTO models (
  public_id, upstream_id, display_name, kind, enabled,
  supports_vision, supports_tools, supports_reasoning, reasoning_wire_format,
  capability_verified_at, created_at, updated_at
) VALUES (
  'deepseek-v4-flash',
  'deepseek-ai/deepseek-v4-flash-0731',
  'DeepSeek V4 Flash',
  'chat',
  1,
  0, 0, 1, 'openai',
  NULL,
  strftime('%Y-%m-%dT%H:%M:%SZ', 'now'),
  strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
)
ON CONFLICT(public_id) DO NOTHING;
