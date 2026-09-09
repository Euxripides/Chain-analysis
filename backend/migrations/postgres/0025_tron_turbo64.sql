-- V5.10: live TRON Node API boundary testing selected 64 as the stable
-- production turbo ceiling. 96 remains an observed-success stress point, but
-- its worse tail latency keeps it outside the production envelope.
ALTER TABLE ankr_production_profiles
    DROP CONSTRAINT IF EXISTS ankr_production_profiles_hard_ceiling_check;

ALTER TABLE ankr_production_profiles
    ADD CONSTRAINT ankr_production_profiles_hard_ceiling_check
    CHECK (hard_ceiling BETWEEN 1 AND 64);

UPDATE ankr_production_profiles
SET turbo_concurrency = 64,
    hard_ceiling = 64,
    reject_boundary = 96,
    version = '1.5.10-tron64',
    verified_at = now(),
    updated_at = now(),
    updated_by = 'migration-0025'
WHERE scope = 'global' AND chain = '' AND dataset = '';
