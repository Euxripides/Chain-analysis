#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT job_id, dataset, from_block, to_block, status, downloaded_rows, downloaded_bytes, raw_object FROM download_shards WHERE job_id IN ('47b7393e-b256-4297-8315-e7b2662f2481','38c76867-e595-4961-bd7b-38ca96c2846b','2c4a5490-3803-4490-84ba-74d599c900c8','72ef94a1-9a36-4c3a-981a-ea108a604bf2','43a7e39b-889e-4804-b68b-d050483014a3') ORDER BY job_id, dataset;"
