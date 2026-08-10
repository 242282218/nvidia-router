-- 011_streaming_quota.sql
-- Isolate streaming requests from the single per-key busy slot. A slow
-- generation previously held the key's only busy slot for minutes, which
-- stalled every short request routed to that key (audit R4).
-- max_streaming_per_key grants each key an independent quota of concurrent
-- streams while the busy slot keeps serving short requests unchanged.

ALTER TABLE runtime_settings
  ADD COLUMN max_streaming_per_key INTEGER NOT NULL DEFAULT 2
    CHECK (max_streaming_per_key BETWEEN 1 AND 10);
