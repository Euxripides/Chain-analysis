#!/usr/bin/env bash
set -uo pipefail
PSQL() { docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1 "$@"; }
echo "=== .s7jobscope-creds.env KEYS ==="
sed -E 's/=.*/=<redacted>/' /home/euripides/.s7jobscope-creds.env
echo "=== cases in fixture org ==="
PSQL -v org="${S7JS_ORG_ID:-}" <<'SQL'
SELECT id, name, organization_id, workspace_id FROM cases ORDER BY created_at DESC LIMIT 12;
SQL
echo "=== tags ==="
PSQL <<'SQL'
SELECT id, name, organization_id, workspace_id FROM tags ORDER BY created_at DESC LIMIT 12;
SQL
echo "=== exports recent ==="
PSQL <<'SQL'
SELECT id, dataset, format, status, rows, organization_id, workspace_id FROM exports ORDER BY created_at DESC LIMIT 8;
SQL
echo "=== download_subjects of the 3 s7js-a jobs ==="
PSQL <<'SQL'
SELECT j.job_no, j.status, s.subject, s.status FROM download_jobs j JOIN download_subjects s ON s.job_id=j.id
WHERE j.workspace_id='8b41836b-6d53-45e4-9a8a-3248a6e24e28' ORDER BY j.created_at DESC LIMIT 10;
SQL
echo "=== ch count for chosen subject ==="
docker exec -i deploy-clickhouse-1 clickhouse-client -q "SELECT count() FROM blockchain.transactions WHERE chain='bsc' AND (from_address='0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649' OR to_address='0x9139b65d2a9f8d9c8e29e9dbbb0a888b1b48c649')" 2>&1
echo "=== done ==="
