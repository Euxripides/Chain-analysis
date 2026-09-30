-- Provider control and official account snapshots for the Ankr / dRPC center.
-- Credentials are encrypted by the application; only their ciphertext is stored.
CREATE TABLE IF NOT EXISTS provider_runtime_settings (
    provider_key text PRIMARY KEY,
    enabled boolean NOT NULL DEFAULT true,
    endpoints jsonb NOT NULL DEFAULT '{}'::jsonb,
    runtime_ciphertext bytea,
    runtime_nonce bytea,
    statistics_ciphertext bytea,
    statistics_nonce bytea,
    keys_ciphertext bytea,
    keys_nonce bytea,
    revision bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (provider_key IN ('ankr', 'drpc'))
);

CREATE TABLE IF NOT EXISTS provider_official_snapshots (
    provider_key text PRIMARY KEY,
    connection_status text NOT NULL DEFAULT 'unavailable',
    balance text,
    balance_unit text,
    requests_day bigint,
    cu_day numeric,
    keys jsonb NOT NULL DEFAULT '[]'::jsonb,
    usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    source text NOT NULL DEFAULT '',
    synced_at timestamptz,
    error_code text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (provider_key IN ('ankr', 'drpc'))
);

INSERT INTO provider_descriptors
    (provider_key, display_name, kind, chains, datasets, endpoint, status, config, priority)
VALUES ('drpc', 'dRPC', 'rpc', ARRAY['solana'], ARRAY['transactions','balances'],
        'https://lb.drpc.live/solana/{key}', 'disconnected', '{"mode":"config"}'::jsonb, 15)
ON CONFLICT (provider_key) DO NOTHING;

GRANT SELECT, INSERT, UPDATE, DELETE ON provider_runtime_settings TO blockchain_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON provider_official_snapshots TO blockchain_app;
