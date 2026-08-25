-- Periodic capability probing (2026-08-25 P3): a background worker re-runs the
-- detailed model probe on a schedule so capability drift is written back
-- automatically instead of being patched by hand. Off by default: enabling it
-- changes upstream request volume, so that stays an operator decision.
ALTER TABLE runtime_settings
  ADD COLUMN capability_probe_enabled INTEGER NOT NULL DEFAULT 0
    CHECK (capability_probe_enabled IN (0, 1));

ALTER TABLE runtime_settings
  ADD COLUMN capability_probe_interval_hours INTEGER NOT NULL DEFAULT 24
    CHECK (capability_probe_interval_hours BETWEEN 6 AND 168);
