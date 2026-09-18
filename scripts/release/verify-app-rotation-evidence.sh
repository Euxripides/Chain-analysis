set -uo pipefail
cd /home/euripides/projects/blockchain-platform
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
echo "=== EVID listing ==="
ls -la "$EVID" | head -30
echo
echo "=== all 'password authentication failed' lines captured since restart boundary ==="
grep -n 'password authentication failed' "$EVID/pg-log-since-restart.txt" || echo "(none)"
echo
echo "=== full timestamped context around that line ==="
grep -n -B2 -A2 'password authentication failed' "$EVID/pg-log-since-restart.txt" | head -40
echo
echo "=== auth failures in the LAST 30 MINUTES, bucketed by minute+user (whole container log) ==="
docker logs --since 30m deploy-postgres-1 2>&1 | grep 'password authentication failed' | \
  sed -E 's/^([0-9-]+T[0-9]{2}:[0-9]{2}).*failed for user "([^"]+)".*/\1 user=\2/' | sort | uniq -c
echo
echo "=== auth failures AFTER the api restart completed (00:03:32Z onward) ==="
docker logs --since 2026-09-13T00:03:32Z deploy-postgres-1 2>&1 | grep -c 'password authentication failed'
echo
echo "=== current: old credential must be rejected, new must work (verification-only) ==="
NEWFILE="$EVID/new-app.pw"
NEW=$(cat "$NEWFILE")
echo "new_pw_len=$(printf '%s' "$NEW" | wc -c) new_pw_sha12=$(printf '%s' "$NEW" | sha256sum | cut -c1-12)"
OLD=$(python3 -c "
import re,pathlib
t=pathlib.Path('$EVID/deploy.env.pre-app-rotation.bak').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M); print(m.group(1))")
echo "old_pw_sha12_from_backup=$(printf '%s' "$OLD" | sha256sum | cut -c1-12)"
printf "SELECT 1;\n" | PGPASSWORD="$OLD" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At 2>&1 | head -2
echo "old_rc=$?"
printf "SET ROLE blockchain_app;\nSELECT 'new_biz_ok',count(*) FROM download_jobs;\n" | PGPASSWORD="$NEW" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At -F'|' 2>&1 | head -3
echo
echo "=== live runtime now ==="
systemctl --user is-active blockchain-api blockchain-worker blockchain-mcp
curl -s http://127.0.0.1:8080/health/ready; echo
printf "SELECT count(*)::text, min(backend_start)::text FROM pg_stat_activity WHERE usename='blockchain_api';\n" | PGPASSWORD=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text(); m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())") psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -At -F'|'
