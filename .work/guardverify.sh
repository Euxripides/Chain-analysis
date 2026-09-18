#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
cat > /tmp/guardcheck.sql <<'SQLEOF'
BEGIN;
DO $$
DECLARE n int; r record;
BEGIN
  -- ============ 1) count_provider_capabilities ============
  SELECT * INTO r FROM count_provider_capabilities LIMIT 1;
  IF r IS NULL THEN RAISE NOTICE 'capabilities: no row, skip'; ELSE
  INSERT INTO count_provider_capabilities (provider_id, chain_id, dataset, enabled, runtime_status, supports_range, range_verified, supports_batch, max_batch_size, semantics, accuracy, reason, updated_at)
  VALUES (r.provider_id, r.chain_id, r.dataset, r.enabled, r.runtime_status, r.supports_range, r.range_verified, r.supports_batch, r.max_batch_size, r.semantics, r.accuracy, r.reason, now())
  ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET enabled=EXCLUDED.enabled, runtime_status=EXCLUDED.runtime_status, supports_range=EXCLUDED.supports_range, range_verified=EXCLUDED.range_verified, supports_batch=EXCLUDED.supports_batch, max_batch_size=EXCLUDED.max_batch_size, semantics=EXCLUDED.semantics, accuracy=EXCLUDED.accuracy, reason=EXCLUDED.reason, updated_at=now()
  WHERE count_provider_capabilities.enabled IS DISTINCT FROM EXCLUDED.enabled
     OR count_provider_capabilities.runtime_status IS DISTINCT FROM EXCLUDED.runtime_status
     OR count_provider_capabilities.supports_range IS DISTINCT FROM EXCLUDED.supports_range
     OR count_provider_capabilities.range_verified IS DISTINCT FROM EXCLUDED.range_verified
     OR count_provider_capabilities.supports_batch IS DISTINCT FROM EXCLUDED.supports_batch
     OR count_provider_capabilities.max_batch_size IS DISTINCT FROM EXCLUDED.max_batch_size
     OR count_provider_capabilities.semantics IS DISTINCT FROM EXCLUDED.semantics
     OR count_provider_capabilities.accuracy IS DISTINCT FROM EXCLUDED.accuracy
     OR count_provider_capabilities.reason IS DISTINCT FROM EXCLUDED.reason;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'capabilities identical-values  -> % rows (expect 0)', n;
  INSERT INTO count_provider_capabilities (provider_id, chain_id, dataset, enabled, runtime_status, supports_range, range_verified, supports_batch, max_batch_size, semantics, accuracy, reason, updated_at)
  VALUES (r.provider_id, r.chain_id, r.dataset, NOT r.enabled, r.runtime_status, r.supports_range, r.range_verified, r.supports_batch, r.max_batch_size, r.semantics, r.accuracy, r.reason, now())
  ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET enabled=EXCLUDED.enabled, runtime_status=EXCLUDED.runtime_status, supports_range=EXCLUDED.supports_range, range_verified=EXCLUDED.range_verified, supports_batch=EXCLUDED.supports_batch, max_batch_size=EXCLUDED.max_batch_size, semantics=EXCLUDED.semantics, accuracy=EXCLUDED.accuracy, reason=EXCLUDED.reason, updated_at=now()
  WHERE count_provider_capabilities.enabled IS DISTINCT FROM EXCLUDED.enabled
     OR count_provider_capabilities.runtime_status IS DISTINCT FROM EXCLUDED.runtime_status
     OR count_provider_capabilities.supports_range IS DISTINCT FROM EXCLUDED.supports_range
     OR count_provider_capabilities.range_verified IS DISTINCT FROM EXCLUDED.range_verified
     OR count_provider_capabilities.supports_batch IS DISTINCT FROM EXCLUDED.supports_batch
     OR count_provider_capabilities.max_batch_size IS DISTINCT FROM EXCLUDED.max_batch_size
     OR count_provider_capabilities.semantics IS DISTINCT FROM EXCLUDED.semantics
     OR count_provider_capabilities.accuracy IS DISTINCT FROM EXCLUDED.accuracy
     OR count_provider_capabilities.reason IS DISTINCT FROM EXCLUDED.reason;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'capabilities changed-enabled   -> % rows (expect 1)', n;
  END IF;

  -- ============ 2) count_provider_runtime_profiles ============
  SELECT * INTO r FROM count_provider_runtime_profiles LIMIT 1;
  IF r IS NULL THEN RAISE NOTICE 'runtime_profiles: no row, skip'; ELSE
  INSERT INTO count_provider_runtime_profiles (provider_id, chain_id, dataset, state, active, queue_depth, safe_concurrency, hard_ceiling, success_rps, valid_count_rps, p50_ms, p95_ms, success_rate, rate_limit_rate, timeout_rate, cost_per_1k_counts, requests, success_count, last_error, updated_at)
  VALUES (r.provider_id, r.chain_id, r.dataset, r.state, r.active, r.queue_depth, r.safe_concurrency, r.hard_ceiling, r.success_rps, r.valid_count_rps, r.p50_ms, r.p95_ms, r.success_rate, r.rate_limit_rate, r.timeout_rate, r.cost_per_1k_counts, r.requests, r.success_count, r.last_error, now())
  ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET state=EXCLUDED.state, active=EXCLUDED.active, queue_depth=EXCLUDED.queue_depth, safe_concurrency=EXCLUDED.safe_concurrency, hard_ceiling=EXCLUDED.hard_ceiling, success_rps=EXCLUDED.success_rps, valid_count_rps=EXCLUDED.valid_count_rps, p50_ms=EXCLUDED.p50_ms, p95_ms=EXCLUDED.p95_ms, success_rate=EXCLUDED.success_rate, rate_limit_rate=EXCLUDED.rate_limit_rate, timeout_rate=EXCLUDED.timeout_rate, cost_per_1k_counts=EXCLUDED.cost_per_1k_counts, requests=EXCLUDED.requests, success_count=EXCLUDED.success_count, last_error=EXCLUDED.last_error, updated_at=now()
  WHERE count_provider_runtime_profiles.state IS DISTINCT FROM EXCLUDED.state
     OR count_provider_runtime_profiles.active IS DISTINCT FROM EXCLUDED.active
     OR count_provider_runtime_profiles.queue_depth IS DISTINCT FROM EXCLUDED.queue_depth
     OR count_provider_runtime_profiles.safe_concurrency IS DISTINCT FROM EXCLUDED.safe_concurrency
     OR count_provider_runtime_profiles.hard_ceiling IS DISTINCT FROM EXCLUDED.hard_ceiling
     OR count_provider_runtime_profiles.success_rps IS DISTINCT FROM EXCLUDED.success_rps
     OR count_provider_runtime_profiles.valid_count_rps IS DISTINCT FROM EXCLUDED.valid_count_rps
     OR count_provider_runtime_profiles.p50_ms IS DISTINCT FROM EXCLUDED.p50_ms
     OR count_provider_runtime_profiles.p95_ms IS DISTINCT FROM EXCLUDED.p95_ms
     OR count_provider_runtime_profiles.success_rate IS DISTINCT FROM EXCLUDED.success_rate
     OR count_provider_runtime_profiles.rate_limit_rate IS DISTINCT FROM EXCLUDED.rate_limit_rate
     OR count_provider_runtime_profiles.timeout_rate IS DISTINCT FROM EXCLUDED.timeout_rate
     OR count_provider_runtime_profiles.cost_per_1k_counts IS DISTINCT FROM EXCLUDED.cost_per_1k_counts
     OR count_provider_runtime_profiles.requests IS DISTINCT FROM EXCLUDED.requests
     OR count_provider_runtime_profiles.success_count IS DISTINCT FROM EXCLUDED.success_count
     OR count_provider_runtime_profiles.last_error IS DISTINCT FROM EXCLUDED.last_error;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'runtime_profiles identical    -> % rows (expect 0)', n;
  INSERT INTO count_provider_runtime_profiles (provider_id, chain_id, dataset, state, active, queue_depth, safe_concurrency, hard_ceiling, success_rps, valid_count_rps, p50_ms, p95_ms, success_rate, rate_limit_rate, timeout_rate, cost_per_1k_counts, requests, success_count, last_error, updated_at)
  VALUES (r.provider_id, r.chain_id, r.dataset, r.state, r.active, r.queue_depth, r.safe_concurrency, r.hard_ceiling, r.success_rps, r.valid_count_rps, r.p50_ms, r.p95_ms, r.success_rate, r.rate_limit_rate, r.timeout_rate, r.cost_per_1k_counts, r.requests+1, r.success_count, r.last_error, now())
  ON CONFLICT (provider_id, chain_id, dataset) DO UPDATE SET state=EXCLUDED.state, active=EXCLUDED.active, queue_depth=EXCLUDED.queue_depth, safe_concurrency=EXCLUDED.safe_concurrency, hard_ceiling=EXCLUDED.hard_ceiling, success_rps=EXCLUDED.success_rps, valid_count_rps=EXCLUDED.valid_count_rps, p50_ms=EXCLUDED.p50_ms, p95_ms=EXCLUDED.p95_ms, success_rate=EXCLUDED.success_rate, rate_limit_rate=EXCLUDED.rate_limit_rate, timeout_rate=EXCLUDED.timeout_rate, cost_per_1k_counts=EXCLUDED.cost_per_1k_counts, requests=EXCLUDED.requests, success_count=EXCLUDED.success_count, last_error=EXCLUDED.last_error, updated_at=now()
  WHERE count_provider_runtime_profiles.state IS DISTINCT FROM EXCLUDED.state
     OR count_provider_runtime_profiles.active IS DISTINCT FROM EXCLUDED.active
     OR count_provider_runtime_profiles.queue_depth IS DISTINCT FROM EXCLUDED.queue_depth
     OR count_provider_runtime_profiles.safe_concurrency IS DISTINCT FROM EXCLUDED.safe_concurrency
     OR count_provider_runtime_profiles.hard_ceiling IS DISTINCT FROM EXCLUDED.hard_ceiling
     OR count_provider_runtime_profiles.success_rps IS DISTINCT FROM EXCLUDED.success_rps
     OR count_provider_runtime_profiles.valid_count_rps IS DISTINCT FROM EXCLUDED.valid_count_rps
     OR count_provider_runtime_profiles.p50_ms IS DISTINCT FROM EXCLUDED.p50_ms
     OR count_provider_runtime_profiles.p95_ms IS DISTINCT FROM EXCLUDED.p95_ms
     OR count_provider_runtime_profiles.success_rate IS DISTINCT FROM EXCLUDED.success_rate
     OR count_provider_runtime_profiles.rate_limit_rate IS DISTINCT FROM EXCLUDED.rate_limit_rate
     OR count_provider_runtime_profiles.timeout_rate IS DISTINCT FROM EXCLUDED.timeout_rate
     OR count_provider_runtime_profiles.cost_per_1k_counts IS DISTINCT FROM EXCLUDED.cost_per_1k_counts
     OR count_provider_runtime_profiles.requests IS DISTINCT FROM EXCLUDED.requests
     OR count_provider_runtime_profiles.success_count IS DISTINCT FROM EXCLUDED.success_count
     OR count_provider_runtime_profiles.last_error IS DISTINCT FROM EXCLUDED.last_error;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'runtime_profiles requests+1   -> % rows (expect 1)', n;
  END IF;

  -- ============ 3) count_oracle_status ============
  SELECT * INTO r FROM count_oracle_status LIMIT 1;
  IF r IS NULL THEN RAISE NOTICE 'oracle_status: no row, skip'; ELSE
  INSERT INTO count_oracle_status (provider, circuit_state, requests, success_count, p50_ms, p95_ms, success_rate, cache_hit_rate, quota_exhausted, last_error, false_zero_count, prediction_error_p50, updated_at)
  VALUES (r.provider, r.circuit_state, r.requests, r.success_count, r.p50_ms, r.p95_ms, r.success_rate, r.cache_hit_rate, r.quota_exhausted, r.last_error, r.false_zero_count, r.prediction_error_p50, now())
  ON CONFLICT (provider) DO UPDATE SET circuit_state=EXCLUDED.circuit_state, requests=EXCLUDED.requests, success_count=EXCLUDED.success_count, p50_ms=EXCLUDED.p50_ms, p95_ms=EXCLUDED.p95_ms, success_rate=EXCLUDED.success_rate, cache_hit_rate=EXCLUDED.cache_hit_rate, quota_exhausted=EXCLUDED.quota_exhausted, last_error=EXCLUDED.last_error, false_zero_count=EXCLUDED.false_zero_count, prediction_error_p50=EXCLUDED.prediction_error_p50, updated_at=now()
  WHERE count_oracle_status.circuit_state IS DISTINCT FROM EXCLUDED.circuit_state
     OR count_oracle_status.requests IS DISTINCT FROM EXCLUDED.requests
     OR count_oracle_status.success_count IS DISTINCT FROM EXCLUDED.success_count
     OR count_oracle_status.p50_ms IS DISTINCT FROM EXCLUDED.p50_ms
     OR count_oracle_status.p95_ms IS DISTINCT FROM EXCLUDED.p95_ms
     OR count_oracle_status.success_rate IS DISTINCT FROM EXCLUDED.success_rate
     OR count_oracle_status.cache_hit_rate IS DISTINCT FROM EXCLUDED.cache_hit_rate
     OR count_oracle_status.quota_exhausted IS DISTINCT FROM EXCLUDED.quota_exhausted
     OR count_oracle_status.last_error IS DISTINCT FROM EXCLUDED.last_error
     OR count_oracle_status.false_zero_count IS DISTINCT FROM EXCLUDED.false_zero_count
     OR count_oracle_status.prediction_error_p50 IS DISTINCT FROM EXCLUDED.prediction_error_p50;
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'oracle_status identical        -> % rows (expect 0)', n;
  END IF;

  -- ============ 4) ankr_performance_control ============
  UPDATE ankr_performance_control SET requested_mode=requested_mode, effective_mode=effective_mode, target_concurrency=target_concurrency, active_concurrency=active_concurrency, mode_reason=mode_reason, mode_changed_at=mode_changed_at, updated_at=now()
  WHERE singleton=TRUE AND (requested_mode IS DISTINCT FROM requested_mode OR effective_mode IS DISTINCT FROM effective_mode OR target_concurrency IS DISTINCT FROM target_concurrency OR active_concurrency IS DISTINCT FROM active_concurrency OR mode_reason IS DISTINCT FROM mode_reason);
  GET DIAGNOSTICS n = ROW_COUNT;
  RAISE NOTICE 'perf_control identical         -> % rows (expect 0)', n;
END $$;
ROLLBACK;

SQLEOF
docker exec -i deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -v ON_ERROR_STOP=1 < /tmp/guardcheck.sql 2>&1 | grep -E 'NOTICE|ERROR|ROLLBACK|BEGIN' | head -20
echo "== confirm nothing was left modified (rows unchanged) =="
docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "SELECT 'capabilities rows='||count(*) FROM count_provider_capabilities;"
