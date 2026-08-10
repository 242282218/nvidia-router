-- 012_drop_xk_proxy_settings.sql
-- The xk_proxy_settings table from migration 003 has zero readers since the
-- XApi URL extraction was retired in favor of the proxy_pool_settings table
-- (migration 006). Drop it so the schema stops carrying dead storage.
DROP TABLE IF EXISTS xk_proxy_settings;
