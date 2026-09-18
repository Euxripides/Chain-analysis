#!/usr/bin/env bash
# c1-scope-body-memory-gate-v1.sh -- P1-A memory/streaming evaluation of the 12 MiB
# request-body pre-buffer that scopeBodyMiddleware performs BEFORE route authz and CSRF.
# Measures real RSS growth under controlled concurrency, the 413 bound, the disconnect
# path, and readiness while the buffers are held. ASCII only.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
N=${N:-8}
EV=${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/c1-scope-body-memory-$TS}
API=http://127.0.0.1:8080
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" | tail -1; }
rss_kb(){ awk '/VmRSS/{print $2}' /proc/$1/status 2>/dev/null || echo 0; }

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
API_SHA=$(sha256sum /proc/$AP/exe | cut -d\  -f1)
echo "api_pid=$AP api_sha=$API_SHA concurrency=$N evidence=$EV"
RSS0=$(rss_kb "$AP")
echo "rss_baseline_kb=$RSS0"

FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1conv-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1conv -org-name "C1 session scope convergence acceptance" \
    -workspaces c1conv-a,c1conv-b \
    -identities investigator:c1conv-inv@fixture.local:c1conv-a+c1conv-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1CONV_A)
EMAIL=$(read_cred "$CRED" C1MEM_C1CONV_INV_EMAIL)
PW0=$(read_cred "$CRED" C1MEM_C1CONV_INV_PW)
JAR=$EV/jar.txt; rm -f "$JAR"
curl -s -c "$JAR" -o "$EV/login.json" -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW0\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF0=$(csrf_of "$JAR")
PW1="Pw-$TS-mem-31ac"
curl -s -b "$JAR" -c "$JAR" -o "$EV/pwchange.json" -X POST "$API/api/auth/v1/password/change" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF0" -d "{\"current_password\":\"$PW0\",\"new_password\":\"$PW1\"}"
rm -f "$JAR"
curl -s -c "$JAR" -o "$EV/login2.json" -X POST "$API/api/auth/v1/login" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -d "{\"identifier\":\"$EMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}"
CSRF=$(csrf_of "$JAR")
[ -n "$CSRF" ]; chk "$?" "0" "fixture session established (no secret is printed)"

# A 12 MiB body that is refused by the handler (wrong type for case_no) so the buffering
# path is exercised without creating a business object.
python3 - "$EV/body-12mib.json" 12582912 <<PY
import sys
p = sys.argv[1]; n = int(sys.argv[2])
head = bytes('{"case_no":123,"name":"MEM-", ' , "utf-8")
tail = bytes('"}', "utf-8")
pad = b"x" * (n - len(head) - len(tail))
open(p, "wb").write(head + pad + tail)
PY
python3 - "$EV/body-over.json" 12582913 <<PY
import sys
p = sys.argv[1]; n = int(sys.argv[2])
open(p, "wb").write(b'{"case_no":1,"name":"' + b"x" * (n - 20) + b'"}')
PY
ls -l "$EV"/body-*.json | awk '{print $5, $9}'
CASES0=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid;
SQL
)

echo "=== 1. controlled concurrency: $N simultaneous 12 MiB pre-buffered bodies ==="
PEAK=$RSS0
READY_FAIL=0
( while :; do
    C=$(rss_kb "$AP"); echo "$C" >> "$EV/rss-samples.txt"
    R=$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready" 2>/dev/null)
    [ "$R" = "200" ] || echo "$R" >> "$EV/ready-non200.txt"
    sleep 0.2
  done ) &
SAMPLER=$!
for i in $(seq 1 "$N"); do
  ( curl -s -b "$JAR" -o "$EV/burst-$i.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
      -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
      -H "X-CSRF-Token: $CSRF" --data-binary "@$EV/body-12mib.json" > "$EV/burst-$i-code.txt" 2>/dev/null ) &
done
wait $(jobs -p | grep -v "$SAMPLER") 2>/dev/null || true
sleep 1
kill "$SAMPLER" 2>/dev/null || true; wait "$SAMPLER" 2>/dev/null || true
PEAK=$(sort -n "$EV/rss-samples.txt" | tail -1)
RSS_AFTER=$(rss_kb "$AP")
DELTA_MB=$(python3 -c "print(round(($PEAK-$RSS0)/1024.0,1))")
echo "rss_baseline_kb=$RSS0 rss_peak_kb=$PEAK rss_after_kb=$RSS_AFTER peak_delta_mb=$DELTA_MB"
echo "distinct_status_codes=$(cat "$EV"/burst-*-code.txt | sort | uniq -c | tr '\n' ' ')"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "the API is still ready after the burst"
chk "$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid;
SQL
)" "$CASES0" "the burst created no business object (the handler refused every buffered body)"
if [ -f "$EV/ready-non200.txt" ]; then bad "readiness was not 200 during the burst: $(sort -u "$EV/ready-non200.txt" | tr '\n' ' ')"; else ok "readiness stayed 200 for every probe during the burst"; fi
echo "NOTE: measured peak RSS growth for $N concurrent 12 MiB buffered bodies = ${DELTA_MB} MiB"

echo "=== 2. body above the 12 MiB gate bound ==="
code=$(curl -s -b "$JAR" -o "$EV/over.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  --data-binary "@$EV/body-over.json")
echo "over_bound_status=$code body=$(head -c 160 "$EV/over.json")"
chk "$code" "413" "a body above the gate bound is refused with 413 before any handler runs"

echo "=== 3. client disconnect in the middle of a 12 MiB body ==="
RSS_PRE_DISC=$(rss_kb "$AP")
timeout -s KILL 2 curl -s -b "$JAR" -o "$EV/disc.json" -w '%{http_code}' -X POST "$API/api/v2/cases" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF" \
  --data-binary "@$EV/body-12mib.json" --limit-rate 2000k > "$EV/disc-code.txt" 2>/dev/null || true
sleep 2
chk "$(cat "$EV/disc-code.txt")" "" "the killed client received no status code (it really disconnected mid-body)"
chk "$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid;
SQL
)" "$CASES0" "the disconnected upload created no business object"
RSS_POST_DISC=$(rss_kb "$AP")
echo "rss_before_disconnect_kb=$RSS_PRE_DISC rss_after_disconnect_kb=$RSS_POST_DISC"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "the API is still ready after the disconnected upload"

echo "=== 4. identity and cleanup ==="
chk "$(systemctl --user show blockchain-api.service -p MainPID --value)" "$AP" "the API process did not change during this gate"
chk "$(sha256sum /proc/$AP/exe | cut -d\  -f1)" "$API_SHA" "the API binary did not change during this gate"
chk "$(ss -ltn | grep -cE ':(18080|18090|181xx) ')" "0" "no stray listener"
rm -f "$EV"/body-12mib.json "$EV"/body-over.json
echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "C1_SCOPE_BODY_MEMORY_GATE_PASS=1"; else echo "C1_SCOPE_BODY_MEMORY_GATE_PASS=0"; fi
