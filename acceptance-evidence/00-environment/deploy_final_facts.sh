#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== deployment facts ==="
ps -o pid,lstart,cmd -p 1804248 1804250
ls -la /home/euripides/projects/blockchain-platform/backend/bin/api /home/euripides/projects/blockchain-platform/backend/bin/worker
echo "=== source fix markers ==="
grep -c 'two-sided range span' /home/euripides/projects/blockchain-platform/backend/internal/download/runner.go 2>/dev/null
strings /home/euripides/projects/blockchain-platform/backend/bin/worker | grep -c 'refusing silent 0-row completion'
echo "=== health ==="
curl -sS http://127.0.0.1:8080/health/ready; echo ""
echo "=== states ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT kill_switch, manual_mode FROM api_control_settings WHERE id=1;"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -t -c "SELECT count(*) FROM download_jobs WHERE status IN ('RUNNING','PAUSED','QUEUED');"
echo "=== control state ==="
curl -sS http://127.0.0.1:8080/api/v1/api-control/control/states | head -c 200
