#!/usr/bin/env bash
set +e
echo "=== api.log WARN/ERROR counts (since last restart) ==="
grep -c '"WARN"' /tmp/api.log; grep -c '"ERROR"' /tmp/api.log
echo "=== worker.log recent WARN/ERROR msg categories (last 30) ==="
grep -E '"(WARN|ERROR)"' /tmp/worker.log | tail -30 | grep -oE '"msg":"[^"]*"' | sort | uniq -c | sort -rn
echo "=== gate job states ==="
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -c "SELECT job_no, status FROM download_jobs WHERE created_at > now() - interval '2 hours' ORDER BY created_at;"
