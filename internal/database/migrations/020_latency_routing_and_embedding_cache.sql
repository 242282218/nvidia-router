-- 020_latency_routing_and_embedding_cache.sql
-- Add scheduling and caching toggles to the runtime settings.
--
-- latency_routing_enabled: when on, the key pool selects among eligible keys
-- with a weighted random draw favouring faster historical response times
-- (EWMA) instead of pure round-robin. Off preserves the legacy behaviour and
-- all existing deployments start with it on by default (the improvement is
-- opt-out).
--
-- embedding_cache_enabled / embedding_cache_max_entries: gate and size the
-- in-memory exact-match cache for /v1/embeddings. Entries are keyed by a hash
-- of (model + input list), never by raw input text, and never persisted.
--
-- Every column defaults to a safe, non-disruptive value so pre-existing rows
-- keep behaving as before.

ALTER TABLE runtime_settings
  ADD COLUMN latency_routing_enabled INTEGER NOT NULL DEFAULT 1
    CHECK (latency_routing_enabled IN (0, 1));

ALTER TABLE runtime_settings
  ADD COLUMN embedding_cache_enabled INTEGER NOT NULL DEFAULT 1
    CHECK (embedding_cache_enabled IN (0, 1));

ALTER TABLE runtime_settings
  ADD COLUMN embedding_cache_max_entries INTEGER NOT NULL DEFAULT 256
    CHECK (embedding_cache_max_entries BETWEEN 1 AND 10000);
