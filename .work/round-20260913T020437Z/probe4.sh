#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P" || exit 90
PSQL() { docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1 "$@"; }
CH() { docker exec -i deploy-clickhouse-1 clickhouse-client -q "$1" 2>&1; }
echo "=== pg tables like subject/export/case/tag/ws ==="
PSQL <<'SQL'
SELECT tablename FROM pg_tables WHERE schemaname='public' AND (tablename LIKE '%subject%' OR tablename LIKE '%export%' OR tablename LIKE '%case%' OR tablename LIKE '%tag%' OR tablename LIKE '%workspace%' OR tablename LIKE '%member%') ORDER BY 1;
SQL
echo "=== find address with moderate tx count on bsc ==="
CH "SELECT from_address, count() c FROM blockchain.transactions WHERE chain='bsc' GROUP BY from_address HAVING c BETWEEN 50 AND 400 ORDER BY c DESC LIMIT 5 FORMAT TSV"
echo "=== job_options for exports (V2) source ==="
grep -n "jobs?status\|jobOptions\|/api/v2/jobs" E:\Code\远程虚拟机\harness\blockchain-platform/frontend/apps/user-console/src/pages/ExportsPage.tsx | head -20
echo "=== ensure api client ==="
grep -n "ensure" E:\Code\远程虚拟机\harness\blockchain-platform/frontend/src/api/client.ts | head -10
echo "=== V2 export create payload ==="
sed -n '300,360p' E:\Code\远程虚拟机\harness\blockchain-platform/frontend/apps/user-console/src/pages/ExportsPage.tsx
echo "=== v2 exports handler mount ==="
grep -rn "v2.Route\|r.Route(\"/api/v2\|/exports" E:\Code\远程虚拟机\harness\blockchain-platform/backend/internal/api/userv2.go | head -20
echo "=== done ==="
