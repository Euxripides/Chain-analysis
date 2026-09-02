#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== jobs for PEER3 0x6d7d33ab ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT j.job_no, j.status, j.from_block, j.to_block, j.datasets, j.created_at FROM download_jobs j JOIN download_subjects s ON s.job_id=j.id WHERE s.subject='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' ORDER BY j.created_at;"
echo "=== shards of those jobs ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT j.job_no, sh.dataset, sh.from_block, sh.to_block, sh.status, sh.downloaded_rows, sh.page_no FROM download_shards sh JOIN download_jobs j ON j.id=sh.job_id JOIN download_subjects s ON s.id=sh.subject_id WHERE s.subject='0x6d7d33ab0d492acc7122e0918df54e2dcf35fb28' ORDER BY j.created_at;"
echo "=== jobs for to-side 0xb3f18f75 ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT j.job_no, j.status, j.from_block, j.to_block, j.datasets, j.created_at FROM download_jobs j JOIN download_subjects s ON s.job_id=j.id WHERE s.subject='0xb3f18f75b4dd5da1263577dcf68089b5a0c8711c' ORDER BY j.created_at DESC LIMIT 8;"
echo "=== coverage for b3f18f75 ==="
docker exec deploy-postgres-1 psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "SELECT dataset, from_block, to_block, row_count FROM dataset_coverage WHERE subject='0xb3f18f75b4dd5da1263577dcf68089b5a0c8711c' ORDER BY dataset, from_block;"
