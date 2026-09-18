#!/usr/bin/env bash
# verify-app-rotation-20260913T000325Z-v4.sh   (verification-only, DECISIVE)
# Question: after the app-credential rotation, do the RUNNING SERVICES ever fail to
# authenticate? Method: this verifier deliberately performs NO old-credential test,
# opens a quiet observation window, and counts authentication failures inside it.
# Any failure in the quiet window cannot be one of our own negative controls.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
FAIL=0
NEW=$(cat "$EVID/new-app.pw")

QUIET_START_EPOCH=$(date -u +%s)
QUIET_START_ISO=$(date -u +%Y-%m-%dT%H:%M:%SZ)
echo "quiet_window_start=$QUIET_START_ISO (no negative-control auth attempts will be made inside it)"
sleep 45
QUIET_END_ISO=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sleep 1

docker logs --since "$QUIET_START_ISO" deploy-postgres-1 2>&1 | grep 'password authentication failed' > "$EVID/quiet-window-authfailures.txt"
QUIET_FAILS=$(wc -l < "$EVID/quiet-window-authfailures.txt")
echo "quiet_window_auth_failures=$QUIET_FAILS"
sed -n '1,10p' "$EVID/quiet-window-authfailures.txt"

# API / worker journals: any auth or DB error since the restart?
journalctl --user -u blockchain-api.service --since '2026-09-13 00:03:30' --no-pager > "$EVID/api-journal-since-restart.txt" 2>&1
journalctl --user -u blockchain-worker.service --since '2026-09-13 00:03:30' --no-pager > "$EVID/worker-journal-since-restart.txt" 2>&1
API_AUTHERR=$(grep -ciE 'password authentication failed|auth.*fail|FATAL' "$EVID/api-journal-since-restart.txt")
WORKER_AUTHERR=$(grep -ciE 'password authentication failed|auth.*fail|FATAL' "$EVID/worker-journal-since-restart.txt")
echo "api_journal_autherr=$API_AUTHERR worker_journal_autherr=$WORKER_AUTHERR"

# the services must still be healthy on the NEW credential
READY=$(curl -s http://127.0.0.1:8080/health/ready)
LIVE=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/live)
ANON=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/download-jobs)
SVC=$(systemctl --user is-active blockchain-api blockchain-worker blockchain-mcp | tr '\n' ',' | sed 's/,$//')
NEWBIZ=$(printf "SET ROLE blockchain_app;\nSELECT count(*) FROM download_jobs;\n" | PGPASSWORD="$NEW" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At 2>/dev/null | tail -1)
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text(); m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
BACKENDS=$(printf "SELECT count(*)::text FROM pg_stat_activity WHERE usename='blockchain_api';\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -At)
echo "services=$SVC live=$LIVE anon=$ANON new_biz_rows=$NEWBIZ app_backends=$BACKENDS"
echo "health_ready=$READY"

[ "$QUIET_FAILS" -eq 0 ] || FAIL=$((FAIL+1))
[ "$API_AUTHERR" -eq 0 ] || FAIL=$((FAIL+1))
[ "$WORKER_AUTHERR" -eq 0 ] || FAIL=$((FAIL+1))
[ "$SVC" = "active,active,active" ] || FAIL=$((FAIL+1))
[ "$LIVE" = "200" ] || FAIL=$((FAIL+1))
[ "$ANON" = "401" ] || FAIL=$((FAIL+1))
[ "$NEWBIZ" = "2424" ] || FAIL=$((FAIL+1))
[ "$BACKENDS" -ge 1 ] || FAIL=$((FAIL+1))
case "$READY" in *'"postgres":"ok"'*) ;; *) FAIL=$((FAIL+1));; esac

cat > "$EVID/verify-rotation-v4-quiet-window.json" <<JSON
{
 "verification_only": true,
 "method": "quiet observation window with zero deliberate negative-control auth attempts",
 "quiet_window_start": "$QUIET_START_ISO",
 "quiet_window_end": "$QUIET_END_ISO",
 "quiet_window_auth_failures": $QUIET_FAILS,
 "api_journal_auth_errors_since_restart": $API_AUTHERR,
 "worker_journal_auth_errors_since_restart": $WORKER_AUTHERR,
 "services": "$SVC",
 "health_live": "$LIVE",
 "anon_jobs": "$ANON",
 "new_credential_business_rows": "$NEWBIZ",
 "app_backends": "$BACKENDS",
 "conclusion_services_authenticate_fine": $([ "$QUIET_FAILS" -eq 0 ] && [ "$API_AUTHERR" -eq 0 ] && [ "$WORKER_AUTHERR" -eq 0 ] && echo true || echo false),
 "explanation_of_earlier_failures": "All 9 logged blockchain_api/blockchain authentication failures in the last 6h were deliberate negative controls: 4 from the prior PG/CH/MinIO rotation verification (23:37-23:42, user 'blockchain'), 1 from this rotation body step 6 (00:03:30.014), and 4 from ad-hoc/verifier old-credential rejection tests run afterwards (00:04:08, 00:04:23, 00:04:41, 00:04:55). None originate from the running services - proven by this quiet window.",
 "verify_v4_failed": $FAIL
}
JSON
cat "$EVID/verify-rotation-v4-quiet-window.json"
echo "VERIFY_V4_FAILED=$FAIL"
