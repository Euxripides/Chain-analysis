#!/usr/bin/env bash
set -uo pipefail
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tA -v ON_ERROR_STOP=1; }
pgq <<'SQL'
SELECT table_name||' :: '||string_agg(column_name, ',' ORDER BY ordinal_position)
FROM information_schema.columns
WHERE table_name IN ('address_case_links','address_tag_links','download_subjects','workspace_addresses','exports')
GROUP BY table_name ORDER BY table_name;
SQL
echo "=== investigator default ws ==="
sed -E 's/=.*/=<redacted>/' /home/euripides/.s7jobscope-creds.env | head -3
