-- The Platform Swagger account key is separate from the runtime RPC key.
ALTER TABLE provider_runtime_settings
    ADD COLUMN IF NOT EXISTS platform_ciphertext bytea,
    ADD COLUMN IF NOT EXISTS platform_nonce bytea,
    ADD COLUMN IF NOT EXISTS platform_expires_at timestamptz;
