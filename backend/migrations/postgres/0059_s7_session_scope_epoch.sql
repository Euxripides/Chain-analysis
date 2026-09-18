-- 0059_s7_session_scope_epoch.sql
--
-- P1-A / AUTH-007 optimistic commit token.
--
-- The commit gate validates the caller session scope twice: once without a lock
-- before the handler runs, and once with SELECT ... FOR UPDATE immediately
-- before COMMIT. Between the two reads the session row may have been re-scoped
-- (workspace switch), revoked (logout, forced logout, family revoke), rotated or
-- otherwise changed. The gate needs a single monotone value that changes
-- whenever ANY authorization-relevant column changes, so the comparison is not
-- a hand-maintained column list that a future mutation path can silently miss.
--
-- scope_epoch is that value. A BEFORE UPDATE trigger bumps it whenever an UPDATE
-- changes any column except the two pure liveness columns:
--
--   last_seen_at  refreshed by the 60s idle renewal
--   expires_at    extended by the same idle renewal
--
-- Those two are the only session writes that carry no authorization meaning, so
-- excluding them keeps a concurrent idle renewal from producing a spurious
-- conflict, while every scope mutation (workspace_id, revoked_at, replaced_by,
-- rotation_count, token_hash, csrf_hash, audience, organization_id,
-- identity_id, mfa_satisfied, device_id, ...) bumps the epoch.
--
-- The column is additive with a constant DEFAULT, so applying this migration to
-- a live table is a catalogue-only change and the already deployed API binary
-- keeps working untouched (it never reads the column).

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS scope_epoch BIGINT NOT NULL DEFAULT 0;

CREATE OR REPLACE FUNCTION sessions_scope_epoch_bump() RETURNS trigger AS $$
BEGIN
    IF (to_jsonb(NEW) - 'last_seen_at' - 'expires_at' - 'scope_epoch')
       IS DISTINCT FROM
       (to_jsonb(OLD) - 'last_seen_at' - 'expires_at' - 'scope_epoch') THEN
        NEW.scope_epoch := OLD.scope_epoch + 1;
    ELSE
        NEW.scope_epoch := OLD.scope_epoch;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS sessions_scope_epoch_bump ON sessions;

CREATE TRIGGER sessions_scope_epoch_bump
    BEFORE UPDATE ON sessions
    FOR EACH ROW EXECUTE FUNCTION sessions_scope_epoch_bump();

COMMENT ON COLUMN sessions.scope_epoch IS
    'P1-A/AUTH-007 optimistic commit token: bumped by sessions_scope_epoch_bump on every UPDATE that changes any column except last_seen_at/expires_at';
