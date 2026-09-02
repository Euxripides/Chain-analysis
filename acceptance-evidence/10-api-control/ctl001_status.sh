#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, count(*) FROM provider_usage WHERE request_time >= now() - interval '24 hours' GROUP BY status;"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT status, http_status, api_error, error_class, count(*) FROM provider_usage WHERE request_time >= now() - interval '24 hours' AND (status != 'success' OR http_status >= 400) GROUP BY status, http_status, api_error, error_class ORDER BY count(*) DESC LIMIT 15;"
