#!/usr/bin/env bash
set -uo pipefail
cd /home/euripides/projects/blockchain-platform
echo "=== containers ==="
docker ps --format '{{.Names}}|{{.Image}}|{{.Status}}'
echo "=== membership of blockchain_api ==="
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)$',t,re.M); print(m.group(1).strip())")
printf "SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname='blockchain_api';\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At 2>&1
echo "--- inherited privileges sample (top 12 tables) ---"
printf "SELECT table_name, string_agg(privilege_type,',' ORDER BY privilege_type) FROM information_schema.role_table_grants WHERE grantee IN ('blockchain_api') OR grantee IN (SELECT r.rolname FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE u.rolname='blockchain_api') GROUP BY 1 ORDER BY 1 LIMIT 12;\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -F'|' 2>&1
unset PGP
echo "=== PRE business baseline ==="
PG=$(docker ps --format '{{.Names}}' | grep -iE 'postgres|^pg' | head -1)
echo "pgcontainer=$PG"
CH=$(docker ps --format '{{.Names}}' | grep -iE 'clickhouse' | head -1)
echo "chcontainer=$CH"
docker exec "$PG" psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT status,count(*) FROM download_jobs GROUP BY status ORDER BY status;" 2>&1
docker exec "$PG" psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT 'audit_max_seq', coalesce(max(seq),0)::text FROM audit_log;" 2>&1
docker exec "$PG" psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT 'api_conns',count(*)::text FROM pg_stat_activity WHERE usename='blockchain_api';" 2>&1
echo "--- job runtime tables ---"
docker exec "$PG" psql -U blockchain -d blockchain_platform -At -c "SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND (table_name ~ 'shard|cursor|checkpoint|outbox|inbox|job') ORDER BY 1;" 2>&1
echo "--- active job rows ---"
docker exec "$PG" psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT 'jobs_total',count(*)::text FROM download_jobs UNION ALL SELECT 'jobs_finished',count(*)::text FROM download_jobs WHERE finished_at IS NOT NULL;" 2>&1
