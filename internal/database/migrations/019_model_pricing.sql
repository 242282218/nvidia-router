-- 019_model_pricing.sql
-- Add optional per-model token pricing (USD per million tokens). These drive a
-- best-effort cost estimate in the monitoring surface: request_logs/daily_stats
-- already capture prompt/completion token totals per model, so an operator who
-- fills in the price columns gets a USD cost breakdown without any upstream
-- billing integration (NVIDIA does not expose a public balance API).
--
-- A NULL price means "not priced" and contributes $0 to the estimate, which
-- keeps every existing row and newly discovered model working unchanged.

ALTER TABLE models
  ADD COLUMN input_usd_per_mtok REAL
    CHECK (input_usd_per_mtok IS NULL OR input_usd_per_mtok >= 0);

ALTER TABLE models
  ADD COLUMN output_usd_per_mtok REAL
    CHECK (output_usd_per_mtok IS NULL OR output_usd_per_mtok >= 0);
