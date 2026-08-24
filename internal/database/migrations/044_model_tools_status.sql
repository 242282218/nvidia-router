ALTER TABLE models ADD COLUMN tools_status TEXT NOT NULL DEFAULT 'unknown'
  CHECK (tools_status IN ('unknown', 'inferred', 'supported', 'unsupported'));
ALTER TABLE models ADD COLUMN tools_verified_at TEXT;

UPDATE models
SET tools_status = CASE WHEN supports_tools = 1 THEN 'inferred' ELSE 'unknown' END
WHERE tools_status = 'unknown';
