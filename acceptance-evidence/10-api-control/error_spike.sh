#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== recent provider_usage errors ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT request_time, method, status, http_status, error_class, api_type, api_error FROM provider_usage WHERE status='error' ORDER BY request_time DESC LIMIT 15;"
