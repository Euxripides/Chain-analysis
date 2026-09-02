#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== test-window provider usage (2026-09-02 12:50Z .. now) ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT COALESCE(dataset,'-') AS dataset, api_type, count(*) AS requests, SUM(method_calls) AS logical_calls, SUM(credits) AS credits, ROUND(SUM(estimated_cost_usd)::numeric, 6) AS cost_usd FROM provider_usage WHERE request_time >= '2026-09-02 12:50:00+00' GROUP BY dataset, api_type ORDER BY requests DESC;"
echo "=== totals ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT count(*) AS requests, SUM(credits) AS credits, ROUND(SUM(estimated_cost_usd)::numeric, 6) AS cost_usd, SUM(CASE WHEN status='error' THEN 1 ELSE 0 END) AS errors FROM provider_usage WHERE request_time >= '2026-09-02 12:50:00+00';"
