#!/usr/bin/env bash
set -uo pipefail
cd /home/euripides/projects/blockchain-platform
PW=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(m.group(1))")
echo -n "pwlen="; printf '%s' "$PW" | wc -c
echo "=== files (repo-wide) containing CURRENT app password: PATHS ONLY ==="
grep -rlF --binary-files=without-match --exclude-dir=.git -- "$PW" . 2>/dev/null | sort
echo "=== perms of matches under deploy/ ==="
for f in $(grep -rlF --binary-files=without-match -- "$PW" deploy/ 2>/dev/null | sort); do stat -c '%a %U:%G %n' "$f"; done
echo "=== files under HOME outside repo ==="
grep -rlF --binary-files=without-match --exclude-dir=projects --exclude-dir=.cache --exclude-dir=.nvm --exclude-dir=.local -- "$PW" "$HOME" 2>/dev/null | sort | head -20
unset PW
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)$',t,re.M); print(m.group(1).strip())")
echo "=== pg role attributes ==="
printf "SELECT rolname,rolcanlogin,rolsuper,rolbypassrls,rolconnlimit,rolcreatedb,rolcreaterole,coalesce(rolvaliduntil::text,'none') FROM pg_roles WHERE rolname IN ('blockchain','blockchain_api') ORDER BY rolname;\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -F'|' 2>&1
echo "=== table grants + memberships for blockchain_api ==="
printf "SELECT (SELECT count(*) FROM information_schema.role_table_grants WHERE grantee='blockchain_api') AS table_grants, (SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE r.rolname='blockchain_api') AS memberships, (SELECT count(*) FROM information_schema.role_usage_grants WHERE grantee='blockchain_api') AS usage_grants;\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -F'|' 2>&1
unset PGP
echo "=== PRE business baseline (via docker exec, no password on argv) ==="
docker exec blockchain-postgres psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT status,count(*) FROM download_jobs GROUP BY status ORDER BY status;" 2>&1
docker exec blockchain-postgres psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT 'audit_max_seq', coalesce(max(seq),0)::text FROM audit_log;" 2>&1
docker exec blockchain-postgres psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT 'api_sessions',count(*) FROM pg_stat_activity WHERE usename='blockchain_api';" 2>&1
docker exec blockchain-postgres psql -U blockchain -d blockchain_platform -At -F'|' -c "SELECT table_name FROM information_schema.tables WHERE table_schema='public' AND (table_name ~ 'shard|cursor|checkpoint|outbox|inbox' OR table_name LIKE 'job%') ORDER BY 1;" 2>&1
echo "=== user-console / admin / api health ==="
curl -s -o /dev/null -w 'healthz=%{http_code}\n' http://127.0.0.1:8080/health/live
curl -s -o /dev/null -w 'anon_jobs=%{http_code}\n' http://127.0.0.1:8080/api/v1/download-jobs
