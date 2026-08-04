-- 008_retry_budget.sql
-- Bound the pre-commit retry loop. Before this, a request retried until it had
-- tried every key in the pool, with no delay between attempts, and streaming
-- requests carried no total deadline at all (only non-stream requests got one
-- from nonstream_total_timeout_ms). A large key pool therefore turned one client
-- request into an unbounded burst of upstream attempts.
--
-- max_attempts_per_request caps the number of keys a single request may try.
-- retry_budget_ms bounds the acquire/retry phase. It deliberately does not bound
-- a committed stream body, which stays governed by the in-stream idle timeout so
-- long generations are not truncated.

ALTER TABLE runtime_settings
  ADD COLUMN max_attempts_per_request INTEGER NOT NULL DEFAULT 5
    CHECK (max_attempts_per_request BETWEEN 1 AND 50);

ALTER TABLE runtime_settings
  ADD COLUMN retry_budget_ms INTEGER NOT NULL DEFAULT 120000
    CHECK (retry_budget_ms BETWEEN 1000 AND 600000);
