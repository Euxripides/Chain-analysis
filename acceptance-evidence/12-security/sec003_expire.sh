#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "UPDATE export_tickets SET expires_at = now() - interval '1 minute' WHERE id='c255abff-780c-4051-ba5b-71cc399dd6bd';"
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT id, expires_at FROM export_tickets WHERE id='c255abff-780c-4051-ba5b-71cc399dd6bd';"
