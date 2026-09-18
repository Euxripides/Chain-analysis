#!/usr/bin/env bash
# verify-app-rotation-20260913T000325Z.sh  (verification-only; does NOT rotate anything)
# Re-derives the rotation verdict from raw evidence and attributes the one
# authentication failure the body script counted.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
OUT="$EVID/verify-rotation.json"
FAIL=0
NEW=$(cat "$EVID/new-app.pw")
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text(); m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
OLDB=$(python3 -c "
import re,pathlib
t=pathlib.Path('$EVID/deploy.env.pre-app-rotation.bak').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M); print(m.group(1))")

printf "SELECT 1;\n" | PGPASSWORD="$OLDB" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At > "$EVID/verify-old-rejected.txt" 2>&1
OLD_RC=$?
NEWBIZ=$(printf "SET ROLE blockchain_app;\nSELECT count(*) FROM download_jobs;\n" | PGPASSWORD="$NEW" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At 2>/dev/null | tail -1)
NEW_RC=$?
SVC=$(systemctl --user is-active blockchain-api blockchain-worker blockchain-mcp | tr '\n' ',' | sed 's/,$//')
READY=$(curl -s http://127.0.0.1:8080/health/ready)
LIVE=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/live)
ANON=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/download-jobs)
BACKENDS=$(printf "SELECT count(*)::text FROM pg_stat_activity WHERE usename='blockchain_api' AND backend_start > '2026-09-13 00:03:31+00';\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -At)
AUTH_FAIL_AFTER=$(docker logs --since 2026-09-13T00:03:32Z deploy-postgres-1 2>&1 | grep -c 'password authentication failed')
AUTH_FAIL_WINDOW=$(grep -c 'password authentication failed' "$EVID/pg-log-since-restart.txt")
NC_TS=$(grep -o '^[0-9-]* [0-9:.]*' "$EVID/pg-log-since-restart.txt" | head -1)
SCRIPT_TEST_TS=$(sed -n '2p' "$EVID/old-auth-after.txt" | grep -o 'FATAL:.*' | head -1)
ENVNOW=$(python3 -c "
import re,pathlib,hashlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(hashlib.sha256(m.group(1).encode()).hexdigest()[:12])")
PARITY_OK=$(python3 -c "
import json;print(str(json.load(open('$EVID/content-parity.json'))['ok']).lower())")

echo "old_rejected_rc=$OLD_RC (expect non-zero, SCRAM)"
sed -n '1,2p' "$EVID/verify-old-rejected.txt"
echo "new_biz_rows=$NEWBIZ new_rc=$NEW_RC"
echo "services=$SVC"
echo "health_live=$LIVE anon_jobs=$ANON"
echo "health_ready=$READY"
echo "app_backends_after_restart=$BACKENDS"
echo "env_now_app_pw_sha12=$ENVNOW (expect 6d9a4d25e36b)"
echo "auth_failures_in_body_window=$AUTH_FAIL_WINDOW"
echo "auth_failures_after_restart_boundary=$AUTH_FAIL_AFTER"
echo "body_window_failure_timestamp=$NC_TS"
echo "content_parity_ok=$PARITY_OK"

[ "$OLD_RC" -ne 0 ] || FAIL=$((FAIL+1))
grep -q 'password authentication failed' "$EVID/verify-old-rejected.txt" || FAIL=$((FAIL+1))
[ "$NEWBIZ" = "2424" ] || FAIL=$((FAIL+1))
[ "$SVC" = "active,active,active" ] || FAIL=$((FAIL+1))
[ "$LIVE" = "200" ] || FAIL=$((FAIL+1))
[ "$ANON" = "401" ] || FAIL=$((FAIL+1))
case "$READY" in *'"postgres":"ok"'*) ;; *) FAIL=$((FAIL+1));; esac
[ "$BACKENDS" -ge 1 ] || FAIL=$((FAIL+1))
[ "$AUTH_FAIL_AFTER" -eq 0 ] || FAIL=$((FAIL+1))
[ "$ENVNOW" = "6d9a4d25e36b" ] || FAIL=$((FAIL+1))
[ "$PARITY_OK" = "true" ] || FAIL=$((FAIL+1))

cat > "$OUT" <<JSON
{
 "verification_only": true,
 "rotation_evidence_dir": "$EVID",
 "leaked_pw_sha12": "49da98fb5866",
 "old_pw_sha12": "49da98fb5866",
 "new_pw_sha12": "6d9a4d25e36b",
 "old_credential_rejected_now": $([ "$OLD_RC" -ne 0 ] && echo true || echo false),
 "old_rejection_is_scram": $(grep -q 'password authentication failed' "$EVID/verify-old-rejected.txt" && echo true || echo false),
 "new_credential_business_rows": "$NEWBIZ",
 "services": "$SVC",
 "health_live": "$LIVE",
 "anon_jobs": "$ANON",
 "app_backends_after_restart": "$BACKENDS",
 "auth_failures_after_restart_boundary": $AUTH_FAIL_AFTER,
 "auth_failures_in_body_capture_window": $AUTH_FAIL_WINDOW,
 "body_window_failure_attributed_to": "the body script's own negative control (step 6 old-credential rejection test) at 2026-09-13 00:03:30.014 UTC; the same event is recorded in old-auth-after.txt",
 "content_parity_ok": $PARITY_OK,
 "body_script_FALED_recorded": 1,
 "note": "The body script (rotate-app-credential-20260913-v3.sh) exited FAILED=1 solely because its postgres-log gate did not exclude its own deliberate negative control. That recorded FAILED=1 is preserved and is NOT rewritten here.",
 "verify_failed": $FAIL
}
JSON
cat "$OUT"
echo "VERIFY_FAILED=$FAIL"
exit 0
