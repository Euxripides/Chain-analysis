#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== provider_usage window stats (last 24h, UTC) ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT count(*) AS total_records, SUM(method_calls) AS logical_calls, SUM(CASE WHEN NOT cache_hit AND NOT singleflight_hit THEN 1 ELSE 0 END) AS provider_calls, SUM(CASE WHEN status != 'success' OR http_status >= 400 THEN 1 ELSE 0 END) AS errors, SUM(CASE WHEN retry_attempt > 0 THEN 1 ELSE 0 END) AS retries, SUM(CASE WHEN cache_hit THEN 1 ELSE 0 END) AS cache_hits, SUM(credits) AS credits, SUM(cost_usd) AS cost_usd FROM provider_usage WHERE created_at >= now() - interval '24 hours';"
echo "=== provider_usage schema ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "\d provider_usage" | head -40
