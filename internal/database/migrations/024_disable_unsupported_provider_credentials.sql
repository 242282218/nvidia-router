-- 024_disable_unsupported_provider_credentials.sql
-- The runtime currently routes only NVIDIA models. Preserve configured
-- credentials while preventing unsupported providers from being resolved.
UPDATE provider_credentials
SET enabled = 0
WHERE provider <> 'nvidia' AND enabled <> 0;
