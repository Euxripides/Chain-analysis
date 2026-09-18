set -uo pipefail
cd /home/euripides/projects/blockchain-platform
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
echo "=== mtimes of old-credential rejection evidence files (UTC) ==="
for f in old-auth-after.txt verify-old-rejected.txt verify2-old-rejected.txt verify3-old-rejected.txt old-auth-precheck.txt; do
  if [ -f "$EVID/$f" ]; then stat -c '%y  %n' "$EVID/$f"; fi
done
echo "=== any other files in EVID created 00:04:00-00:05:10 UTC ==="
find "$EVID" -type f -newermt '2026-09-13 07:04:00' ! -newermt '2026-09-13 07:05:10' -printf '%TY-%Tm-%Td %TH:%TM:%TS %p\n' 2>/dev/null | sort
echo "=== pg log_line_prefix / log_connections settings ==="
docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -At -c "SHOW log_line_prefix; SHOW log_connections; SHOW log_disconnections;"
echo "=== raw auth-failure lines with full detail (any IP/appname?) ==="
docker logs --since 6h deploy-postgres-1 2>&1 | grep -A1 'password authentication failed' | head -20
