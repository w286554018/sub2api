-- 238_global_model_pricing.sql
-- Global model pricing overrides. Exact model names and trailing-prefix
-- patterns (for example gpt-5*) apply before group/channel/catalog pricing.
CREATE TABLE IF NOT EXISTS global_model_pricing (
    id BIGSERIAL PRIMARY KEY,
    model_pattern VARCHAR(200) NOT NULL,
    billing_mode VARCHAR(16) NOT NULL DEFAULT 'token',
    input_price DOUBLE PRECISION,
    output_price DOUBLE PRECISION,
    cache_write_price DOUBLE PRECISION,
    cache_write_1h_price DOUBLE PRECISION,
    cache_read_price DOUBLE PRECISION,
    per_request_price DOUBLE PRECISION,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_global_model_pricing_mode
        CHECK (billing_mode IN ('token', 'per_request', 'image', 'video')),
    CONSTRAINT chk_global_model_pricing_token_prices
        CHECK (
            (billing_mode = 'token' AND input_price IS NOT NULL AND output_price IS NOT NULL AND per_request_price IS NULL)
            OR (billing_mode <> 'token' AND input_price IS NULL AND output_price IS NULL
                AND cache_write_price IS NULL AND cache_write_1h_price IS NULL
                AND cache_read_price IS NULL AND per_request_price IS NOT NULL)
        ),
    CONSTRAINT chk_global_model_pricing_pattern
        CHECK (
            length(btrim(model_pattern)) > 0
            AND model_pattern = lower(btrim(model_pattern))
            AND position(' ' IN model_pattern) = 0
            AND (length(model_pattern) - length(replace(model_pattern, '*', ''))) <= 1
            AND (position('*' IN model_pattern) = 0 OR right(model_pattern, 1) = '*')
            AND btrim(model_pattern, '*') <> ''
        ),
    CONSTRAINT chk_global_model_pricing_non_negative
        CHECK (
            (input_price IS NULL OR (input_price >= 0 AND input_price < 'Infinity'::double precision)) AND
            (output_price IS NULL OR (output_price >= 0 AND output_price < 'Infinity'::double precision)) AND
            (cache_write_price IS NULL OR (cache_write_price >= 0 AND cache_write_price < 'Infinity'::double precision)) AND
            (cache_write_1h_price IS NULL OR (cache_write_1h_price >= 0 AND cache_write_1h_price < 'Infinity'::double precision)) AND
            (cache_read_price IS NULL OR (cache_read_price >= 0 AND cache_read_price < 'Infinity'::double precision)) AND
            (per_request_price IS NULL OR (per_request_price >= 0 AND per_request_price < 'Infinity'::double precision))
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_global_model_pricing_pattern ON global_model_pricing(model_pattern);
CREATE INDEX IF NOT EXISTS idx_global_model_pricing_enabled ON global_model_pricing(enabled);
