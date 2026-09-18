#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P" || exit 90
PSQL() { docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1 "$@"; }
echo "=== job status distribution ==="
PSQL <<'SQL'
SELECT status, count(*) FROM download_jobs GROUP BY status ORDER BY 2 DESC;
SQL
echo "=== completed jobs sample (org/ws) ==="
PSQL <<'SQL'
SELECT j.id, j.job_no, j.chain, j.status, j.organization_id, j.workspace_id,
       (SELECT count(*) FROM download_job_subjects s WHERE s.job_id=j.id) AS subjects
FROM download_jobs j WHERE j.status='COMPLETED' ORDER BY j.created_at DESC LIMIT 8;
SQL
echo "=== subjects of the most recent COMPLETED jobs ==="
PSQL <<'SQL'
SELECT s.job_id, s.subject, s.chain FROM download_job_subjects s
WHERE s.job_id IN (SELECT id FROM download_jobs WHERE status='COMPLETED' ORDER BY created_at DESC LIMIT 5)
LIMIT 20;
SQL
echo "=== clickhouse databases/tables ==="
docker exec -i deploy-clickhouse-1 clickhouse-client --query "SHOW DATABASES" 2>&1 | head
echo "=== token_transfers total + top addresses ==="
docker exec -i deploy-clickhouse-1 clickhouse-client -q "SELECT count() FROM blockchain.token_transfers" 2>&1 | head -3
docker exec -i deploy-clickhouse-1 clickhouse-client -q "SELECT chain, from_address, count() c FROM blockchain.token_transfers GROUP BY chain, from_address ORDER BY c DESC LIMIT 5 FORMAT TSV" 2>&1 | head -8
echo "=== transactions top ==="
docker exec -i deploy-clickhouse-1 clickhouse-client -q "SELECT chain, from_address, count() c FROM blockchain.transactions GROUP BY chain, from_address ORDER BY c DESC LIMIT 5 FORMAT TSV" 2>&1 | head -8
echo "=== done ==="
