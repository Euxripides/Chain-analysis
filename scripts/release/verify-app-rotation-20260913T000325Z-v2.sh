#!/usr/bin/env bash
# verify-app-rotation-20260913T000325Z-v2.sh   (verification-only)
# v1 of this verifier counted every 'password authentication failed' line after the
# restart boundary - including the ones produced by its OWN negative control. v2
# classifies every auth failure by timestamp window so that only UNEXPLAINED
# failures gate.
set -uo pipefail
cd /home/euripides/projects/blockchain-platform || exit 90
EVID=acceptance-evidence/dual-system-v1/sprint7/app-credential-rotation-20260913T000325Z
FAIL=0
NEW=$(cat "$EVID/new-app.pw")
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text(); m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
OLDB=$(python3 -c "
import re,pathlib
t=pathlib.Path('$EVID/deploy.env.pre-app-rotation.bak').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M); print(m.group(1))")

NC_T0=$(date -u +%s)
printf "SELECT 1;\n" | PGPASSWORD="$OLDB" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At > "$EVID/verify2-old-rejected.txt" 2>&1
OLD_RC=$?
NC_T1=$(date -u +%s)
NEWBIZ=$(printf "SET ROLE blockchain_app;\nSELECT count(*) FROM download_jobs;\n" | PGPASSWORD="$NEW" psql -h 127.0.0.1 -p 5433 -U blockchain_api -d blockchain_platform -At 2>/dev/null | tail -1)
SVC=$(systemctl --user is-active blockchain-api blockchain-worker blockchain-mcp | tr '\n' ',' | sed 's/,$//')
READY=$(curl -s http://127.0.0.1:8080/health/ready)
LIVE=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/health/live)
ANON=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8080/api/v1/download-jobs)
BACKENDS=$(printf "SELECT count(*)::text FROM pg_stat_activity WHERE usename='blockchain_api' AND backend_start > '2026-09-13 00:03:31+00';\n" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -At)
ENVNOW=$(python3 -c "
import re,pathlib,hashlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^DATABASE_URL=.*?://[^:]+:([^@]+)@',t,re.M)
print(hashlib.sha256(m.group(1).encode()).hexdigest()[:12])")
PARITY_OK=$(python3 -c "
import json;print(str(json.load(open('$EVID/content-parity.json'))['ok']).lower())")

docker logs --since 6h deploy-postgres-1 2>&1 | grep 'password authentication failed' > "$EVID/auth-failures-6h.txt"
python3 - "$EVID/auth-failures-6h.txt" "$NC_T0" "$NC_T1" > "$EVID/auth-failure-classification.txt" <<'PY'
import sys, re, datetime, calendar
path, nc0, nc1 = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
pat = re.compile(r'^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d+) UTC \[(\d+)\] FATAL:\s+password authentication failed for user "([^"]+)"')
# windows that are EXPECTED to contain deliberate negative controls
NC_WINDOWS = [(nc0 - 3, nc1 + 3, "this verifier's own old-credential rejection test")]
BODY_WINDOW = (calendar.timegm(datetime.datetime(2026,9,13,0,3,26).timetuple()),
               calendar.timegm(datetime.datetime(2026,9,13,0,3,32).timetuple()),
               "rotation body step 6 old-credential rejection test")
PRIOR_WINDOW = (calendar.timegm(datetime.datetime(2026,9,12,23,30,0).timetuple()),
                calendar.timegm(datetime.datetime(2026,9,12,23,50,0).timetuple()),
                "prior PG/CH/MinIO rotation verification negative controls (SEALED handover sec 1.3)")
WINDOWS = [NC_WINDOWS, BODY_WINDOW, PRIOR_WINDOW]
rows = []
unexplained = 0
for line in open(path):
    m = pat.match(line.strip())
    if not m:
        rows.append(("UNPARSED", line.strip()[:120], "?"))
        continue
    ts, pid, user = m.group(1), m.group(2), m.group(3)
    t = calendar.timegm(datetime.datetime.strptime(ts.split(".")[0], "%Y-%m-%d %H:%M:%S").timetuple())
    why = None
    for lo, hi, label in WINDOWS:
        if lo <= t <= hi:
            why = label
            break
    if why is None:
        unexplained += 1
        why = "UNEXPLAINED"
    rows.append((ts, "user=" + user + " pid=" + pid, why))
out = ["total_auth_failures_6h=%d" % len(rows), "unexplained=%d" % unexplained, ""]
for ts, who, why in rows:
    out.append("%s  %-28s  %s" % (ts, who, why))
txt = "\n".join(out)
print(txt)
open(sys.argv[0].replace(".py", ".out"), "w").close()
PY
cat "$EVID/auth-failure-classification.txt"
UNEXPLAINED=$(grep -m1 '^unexplained=' "$EVID/auth-failure-classification.txt" | cut -d= -f2)
TOTAL6H=$(grep -m1 '^total_auth_failures_6h=' "$EVID/auth-failure-classification.txt" | cut -d= -f2)

echo "old_rejected_rc=$OLD_RC"; sed -n '1,2p' "$EVID/verify2-old-rejected.txt"
echo "new_biz_rows=$NEWBIZ services=$SVC live=$LIVE anon=$ANON"
echo "health_ready=$READY"
echo "app_backends_after_restart=$BACKENDS env_now_app_pw_sha12=$ENVNOW"
echo "auth_failures_6h=$TOTAL6H unexplained=$UNEXPLAINED"
echo "content_parity_ok=$PARITY_OK"

[ "$OLD_RC" -ne 0 ] || FAIL=$((FAIL+1))
grep -q 'password authentication failed' "$EVID/verify2-old-rejected.txt" || FAIL=$((FAIL+1))
[ "$NEWBIZ" = "2424" ] || FAIL=$((FAIL+1))
[ "$SVC" = "active,active,active" ] || FAIL=$((FAIL+1))
[ "$LIVE" = "200" ] || FAIL=$((FAIL+1))
[ "$ANON" = "401" ] || FAIL=$((FAIL+1))
case "$READY" in *'"postgres":"ok"'*) ;; *) FAIL=$((FAIL+1));; esac
[ "$BACKENDS" -ge 1 ] || FAIL=$((FAIL+1))
[ "$UNEXPLAINED" -eq 0 ] || FAIL=$((FAIL+1))
[ "$ENVNOW" = "6d9a4d25e36b" ] || FAIL=$((FAIL+1))
[ "$PARITY_OK" = "true" ] || FAIL=$((FAIL+1))

cat > "$EVID/verify-rotation-v2.json" <<JSON
{
 "verification_only": true,
 "rotation_evidence_dir": "$EVID",
 "leaked_pw_sha12": "49da98fb5866",
 "new_pw_sha12": "6d9a4d25e36b",
 "old_credential_rejected_now": $([ "$OLD_RC" -ne 0 ] && echo true || echo false),
 "old_rejection_is_scram": $(grep -q 'password authentication failed' "$EVID/verify2-old-rejected.txt" && echo true || echo false),
 "new_credential_business_rows": "$NEWBIZ",
 "services": "$SVC",
 "health_live": "$LIVE",
 "anon_jobs": "$ANON",
 "app_backends_after_restart": "$BACKENDS",
 "auth_failures_last_6h": $TOTAL6H,
 "auth_failures_unexplained": $UNEXPLAINED,
 "auth_failure_classification_file": "auth-failure-classification.txt",
 "content_parity_ok": $PARITY_OK,
 "body_script_failed_recorded": 1,
 "note_failed": "The body script exited FAILED=1 only because its postgres-log gate did not exclude its own negative control. That recorded FAILED=1 is preserved and is NOT rewritten.",
 "note_verifier_v1": "verifier v1 repeated the same class of mistake (counted its own negative control); v2 attributes by timestamp window and is the authoritative verdict.",
 "verify_failed": $FAIL
}
JSON
cat "$EVID/verify-rotation-v2.json"
echo "VERIFY_V2_FAILED=$FAIL"
