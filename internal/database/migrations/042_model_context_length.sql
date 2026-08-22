-- Operator-owned context window declaration (tokens). 0 means "not declared";
-- /v1/models omits the field for undeclared models so clients fall back to
-- their own defaults instead of trusting a fabricated number. Neither NVIDIA
-- nor OpenCodeFree exposes context metadata in their /v1/models payloads, so
-- the value is maintained by hand through the admin PATCH endpoint.
ALTER TABLE models ADD COLUMN context_length INTEGER NOT NULL DEFAULT 0;
