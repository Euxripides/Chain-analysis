-- Ankr Management MCP OAuth connection.
-- OAuth bearer and PKCE verifier are encrypted by the API before persistence.
CREATE TABLE IF NOT EXISTS ankr_management_connection (
  id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  client_id TEXT NOT NULL DEFAULT '',
  redirect_uri TEXT NOT NULL DEFAULT '',
  token_ciphertext BYTEA,
  token_nonce BYTEA,
  token_expires_at TIMESTAMPTZ,
  account_address TEXT NOT NULL DEFAULT '',
  remaining_credits NUMERIC,
  balance_usd NUMERIC,
  plan TEXT NOT NULL DEFAULT '',
  raw_balance JSONB,
  connected_at TIMESTAMPTZ,
  last_synced_at TIMESTAMPTZ,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO ankr_management_connection(id) VALUES (1) ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS ankr_management_oauth_state (
  state_hash TEXT PRIMARY KEY,
  client_id TEXT NOT NULL,
  redirect_uri TEXT NOT NULL,
  verifier_ciphertext BYTEA NOT NULL,
  verifier_nonce BYTEA NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ankr_management_oauth_state_expiry
  ON ankr_management_oauth_state(expires_at);
