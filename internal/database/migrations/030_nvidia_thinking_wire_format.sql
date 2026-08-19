-- NVIDIA's reasoning models accept the native thinking object. Convert legacy
-- NVIDIA rows that were seeded with the generic OpenAI reasoning field while
-- leaving OpenCode and other compatible providers unchanged.

UPDATE models
SET reasoning_wire_format = 'thinking'
WHERE provider = 'nvidia'
  AND supports_reasoning = 1
  AND reasoning_wire_format = 'openai';
