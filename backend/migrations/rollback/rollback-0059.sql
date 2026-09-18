-- rollback-0059.sql -- removes the P1-A/AUTH-007 optimistic commit token.
-- Additive migration, so the rollback is also additive-safe: dropping the
-- trigger and the column restores the pre-0059 schema exactly and never touches
-- session data (the column carries no business meaning).
DROP TRIGGER IF EXISTS sessions_scope_epoch_bump ON sessions;
DROP FUNCTION IF EXISTS sessions_scope_epoch_bump();
ALTER TABLE sessions DROP COLUMN IF EXISTS scope_epoch;
