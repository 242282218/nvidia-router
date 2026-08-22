-- Add the operator-controlled default reasoning policy and a compact model
-- reasoning capability state. Unknown models remain safe: reasoning is off until
-- a static hint or a successful probe supplies a positive result.
ALTER TABLE runtime_settings
  ADD COLUMN auto_reasoning_enabled INTEGER NOT NULL DEFAULT 1
    CHECK (auto_reasoning_enabled IN (0, 1));

ALTER TABLE models
  ADD COLUMN reasoning_status TEXT NOT NULL DEFAULT 'unknown'
    CHECK (reasoning_status IN ('unknown', 'inferred', 'visible', 'hidden', 'unsupported'));

UPDATE models
SET reasoning_status = CASE
  WHEN supports_reasoning = 1 THEN 'inferred'
  ELSE 'unknown'
END
WHERE reasoning_status = 'unknown';
