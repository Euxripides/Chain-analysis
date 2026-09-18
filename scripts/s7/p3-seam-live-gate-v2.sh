#!/usr/bin/env bash
# p3-seam-live-gate-v1.sh
#
# P3: the request-transaction seam measured on a real instance.
#
# The static half of P3 is scripts/s7/p3_seam_coverage.py. This gate measures what
# a source scan cannot prove:
#
#   1. a write made by a SERVICE (casework.CreateCase opens its own transaction
#      with s.pool.Begin, which therefore becomes a SAVEPOINT inside the request
#      transaction) really is all-or-nothing with the commit gate: the same call
#      that commits normally must leave ZERO rows when the caller workspace is
#      switched while the handler runs;
#   2. that request owns exactly ONE PostgreSQL backend, which is what proves the
#      nested Begin did not escape to a second connection;
#   3. the dedicated 8-connection gate pool queues instead of failing or
#      deadlocking when more gated writes arrive at once than it has connections;
#   4. the request transactions stay short and no backend is left idle in
#      transaction;
#   5. concurrent >256 KiB buffered responses plus one abandoned client leave no
#      spool file, no runaway RSS and a still-ready instance.
#
# WHY v2 EXISTS (the preserved v1 run is
# acceptance-evidence/dual-system-v1/sprint7/p3-seam-p3seam-r1-20260914T162932Z)
# ---------------------------------------------------------------------------
# v1 sections 1 and 2 passed (the service-layer write is all-or-nothing with the
# request: 201 normally, 409 with ZERO rows under a workspace switch, one deny
# audit row, one PostgreSQL backend for the whole request). Section 3 then HUNG:
# v1 ended the concurrent block with a bare "wait", and bash also waits for the
# child of the "exec > >(tee ...)" process substitution, which never exits while
# the script itself is running. The 12 requests had all finished and the sampler
# had written its 200 rows, but the script waited forever. v2 waits on the exact
# pids it started and never on a bare "wait".
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:-$R/backend/bin.new/api-auth007-deny-calib}"
LABEL="${LABEL:-p3seam}"
PORT="${PORT:-18190}"
HANDLER_MS="${HANDLER_MS:-3000}"
CONC="${CONC:-12}"
READY_TRIES="${READY_TRIES:-360}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/p3-seam-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0; NV=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
nv(){ NV=$((NV+1)); echo "NOT_VERIFIED $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
now_ms(){ date +%s%3N; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL port=$PORT handler_ms=$HANDLER_MS concurrency=$CONC"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
DISK0=$(df --output=avail -k / | tail -1 | tr -d ' ')

echo "=== fixtures (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P3 request transaction seam acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
PW1="Pw-$TS-p3seam-51ab"
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "fixture identity logs in with its initial password"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$PW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear*.json

login_ws(){
  local j=$1 ws=$2 out=$3
  rm -f "$j"
  curl -s -c "$j" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$PW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}"
}
RA=$EV/ra.jar; RB=$EV/rb.jar
chk "$(login_ws "$RA" "$WSA" "$EV/ra-login.json")" "200" "caller session RA logs in to workspace A"
chk "$(login_ws "$RB" "$WSB" "$EV/rb-login.json")" "200" "owner session RB logs in to workspace B"
SID_A=$(sess_id "$RA" user); SID_B=$(sess_id "$RB" user)
if [ -z "$SID_A" ] || [ "$SID_A" = "__ERR__" ] || [ -z "$SID_B" ] || [ "$SID_B" = "__ERR__" ]; then bad "session ids not resolvable"; exit 0; fi
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf); CSRF_B=$(csrf_of "$RB" cs_user_session_csrf)

cand_pid=""
cleanup(){
  if [ -n "$cand_pid" ] && kill -0 "$cand_pid" 2>/dev/null; then
    kill -TERM "$cand_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$cand_pid" 2>/dev/null || true
  fi
  cand_pid=""
}
trap cleanup EXIT

echo "=== candidate (calibration build, so the handler window can be held open) ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export SCOPE_CALIB_ENABLE=1
  export SCOPE_CALIB_ROUTE=/api/v2/cases
  export SCOPE_CALIB_PHASE=post
  export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
  nohup setsid "$BIN" >> "$EV/candidate.log" 2>&1 &
  echo $! > "$EV/candidate.pid" )
sleep 1
cand_pid=$(cat "$EV/candidate.pid")
ready=0; rc=""; waited=0
for _ in $(seq 1 "$READY_TRIES"); do
  rc=$(curl -s -o "$EV/candidate-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5; waited=$(( waited + 1 ))
done
chk "$ready" "1" "candidate ready on $PORT (http $rc after $(( waited / 2 ))s)"
if [ "$ready" != "1" ]; then
  echo "READINESS_ABORT=1 no section is started, so no section result can be a cascade of this race."
  echo "=== summary ==="; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; echo "GATE_FAIL=$FAIL"
  exit 0
fi
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running candidate is the binary under test"
RSS0=$(ps -o rss= -p "$cand_pid" | tr -d ' ')

case_rows(){ pgq <<SQL
SELECT count(*) FROM cases WHERE case_no='$1';
SQL
}
backends(){ pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
}

echo "=== 1. the service-layer write is all-or-nothing with the request (positive control) ==="
POS="P3SEAM-$TS-POS"
CODE=$(curl -s -b "$RA" -o "$EV/pos.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -d "{\"case_no\":\"$POS\",\"name\":\"$POS\"}")
echo "positive_case http=$CODE rows=$(case_rows "$POS")"
chk "$CODE" "201" "1: the service-layer create commits normally (201)"
chk "$(case_rows "$POS")" "1" "1: the service transaction really committed one case row"
chk "$(pgq <<SQL
SELECT workspace_id::text FROM cases WHERE case_no='$POS';
SQL
)" "$WSA" "1: the committed case carries the caller workspace"

echo "=== 2. the same service call with a workspace switch inside the handler (negative control) ==="
NEG="P3SEAM-$TS-NEG"
SEQ0=$(pgq <<SQL
SELECT coalesce(max(seq),0) FROM audit_events_v2;
SQL
)
( for _ in $(seq 1 90); do
    pgq <<SQL 2>/dev/null
SELECT 'B|' || pid || '|' || application_name || '|' || COALESCE(state,'-') || '|' || COALESCE(wait_event_type,'-')
FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
    sleep 0.05
  done ) > "$EV/backends-sample.log" 2>&1 &
SAMP=$!
curl -s -b "$RA" -o "$EV/neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" -d "{\"case_no\":\"$NEG\",\"name\":\"$NEG\"}" > "$EV/neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
M0=$(now_ms)
SWRES=$(curl -s -m 120 -b "$RA" -c "$RA" -o "$EV/switch.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_A" -d "{\"workspace_id\":\"$WSB\"}")
M1=$(now_ms); SWMS=$(( M1 - M0 ))
wait $NPID
wait $SAMP 2>/dev/null
NCODE=$(cat "$EV/neg-code.txt")
NERR=$(jget < "$EV/neg.json" "['error']['code']" 2>/dev/null || echo "-")
DENY=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE seq > $SEQ0 AND action='authorization.denied' AND route_template='/api/v2/cases';
SQL
)
BSAMP=$(grep -c '^B|' "$EV/backends-sample.log" 2>/dev/null || echo 0)
DISTINCT_PID=$(grep '^B|' "$EV/backends-sample.log" 2>/dev/null | awk -F'|' '{print $2}' | sort -u | wc -l)
echo "negative_case http=$NCODE error=$NERR rows=$(case_rows "$NEG") deny_rows=$DENY switch=$SWRES switch_ms=$SWMS backend_samples=$BSAMP distinct_gate_pids=$DISTINCT_PID"
chk "$SWRES" "200" "2: the workspace switch itself succeeded"
chk "$NCODE" "409" "2: the gated service write is refused under the stale scope"
chk "$NERR" "AUTHZ_WORKSPACE_SCOPE_STALE" "2: the refusal carries the stable scope code"
chk "$(case_rows "$NEG")" "0" "2: the service transaction left ZERO rows behind (its Begin really is a savepoint of the request transaction)"
chk "$DENY" "1" "2: exactly one detached deny audit row (exactly-once)"
chk "$DISTINCT_PID" "1" "2: the request owned exactly one PostgreSQL backend (the nested Begin did not escape to a second connection)"
if [ "$BSAMP" -ge 1 ]; then ok "2: the gate backend was sampled while the request was in flight ($BSAMP samples)"; else nv "2: no sample caught the gate backend (sampling only)"; fi
SWBACK=$(curl -s -m 60 -b "$RA" -c "$RA" -o "$EV/switch-back.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_A" -d "{\"workspace_id\":\"$WSA\"}")
chk "$SWBACK" "200" "2: the caller session was switched back to workspace A"

echo "=== 3. more gated writes at once than the dedicated gate pool has connections ($CONC) ==="
( for _ in $(seq 1 200); do
    pgq <<SQL 2>/dev/null
SELECT 'X|' || count(*) || '|' || COALESCE(max(extract(milliseconds from now()-xact_start))::bigint,0) || '|' || COALESCE(sum(CASE WHEN wait_event_type='Lock' THEN 1 ELSE 0 END),0)
FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
    sleep 0.05
  done ) > "$EV/xact-sample.log" 2>&1 &
XSAMP=$!
T0=$(now_ms)
i=0
CONCPIDS=""
while [ "$i" -lt "$CONC" ]; do
  i=$(( i + 1 ))
  curl -s -b "$RA" -o "$EV/conc-$i.json" -w '%{http_code}' -m 120 \
    -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF_A" -d "{\"case_no\":\"P3SEAM-$TS-C$i\",\"name\":\"P3SEAM-$TS-C$i\"}" > "$EV/conc-$i-code.txt" 2>/dev/null &
  CONCPIDS="$CONCPIDS $!"
done
for p in $CONCPIDS; do wait "$p" 2>/dev/null; done
T1=$(now_ms); TMS=$(( T1 - T0 ))
wait $XSAMP 2>/dev/null
CODES=$(cat "$EV"/conc-*-code.txt 2>/dev/null | sort | uniq -c | tr '\n' ' ')
N201=$(cat "$EV"/conc-*-code.txt 2>/dev/null | grep -c '^201$')
N000=$(cat "$EV"/conc-*-code.txt 2>/dev/null | grep -c '^000$')
N5XX=$(cat "$EV"/conc-*-code.txt 2>/dev/null | grep -c '^5')
ROWS=$(pgq <<SQL
SELECT count(*) FROM cases WHERE case_no LIKE 'P3SEAM-$TS-C%';
SQL
)
MAXBATCH=$(grep '^X|' "$EV/xact-sample.log" 2>/dev/null | awk -F'|' '{if ($2+0 > m) m=$2+0} END{print m+0}')
MAXXACT=$(grep '^X|' "$EV/xact-sample.log" 2>/dev/null | awk -F'|' '{if ($3+0 > m) m=$3+0} END{print m+0}')
LOCKWAIT=$(grep '^X|' "$EV/xact-sample.log" 2>/dev/null | awk -F'|' '{if ($4+0 > m) m=$4+0} END{print m+0}')
echo "concurrency codes: $CODES"
echo "concurrency total_ms=$TMS created_201=$N201 unreachable_000=$N000 five_xx=$N5XX rows=$ROWS max_gate_backends=$MAXBATCH max_xact_ms=$MAXXACT max_lock_waiters=$LOCKWAIT"
chk "$N000" "0" "3: every concurrent gated write was answered (no unreachable request)"
chk "$N5XX" "0" "3: no concurrent gated write failed with a 5xx"
chk "$ROWS" "$CONC" "3: every concurrent service write committed exactly one row"
if [ "$MAXBATCH" -ge 1 ]; then ok "3: the gate backends were sampled under load (peak $MAXBATCH concurrent)"; else nv "3: no sample caught the gate backends (sampling only)"; fi
if [ "$TMS" -lt 60000 ]; then ok "3: $CONC concurrent gated writes drained in $TMS ms instead of deadlocking"; else bad "3: $CONC concurrent gated writes took $TMS ms"; fi
echo "3: peak gate transaction age $MAXXACT ms, peak lock waiters $LOCKWAIT"

echo "=== 4. long transaction, idle in transaction and bloat evidence ==="
chk "$(backends)" "0" "4: no commit-gate backend is left behind"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction' AND datname='blockchain_platform';
SQL
)" "0" "4: no connection is left idle in transaction"
BLOAT=$(pgq <<SQL
SELECT coalesce(string_agg(relname || '=' || n_dead_tup || '/' || n_live_tup, ' '), '-')
FROM pg_stat_user_tables WHERE relname IN ('cases','sessions','investigation_canvases','audit_events_v2');
SQL
)
echo "bloat_dead_over_live=$BLOAT"

echo "=== 5. concurrent buffered responses plus one abandoned client ==="
CB="P3BUF-$TS"
CCODE=$(curl -s -b "$RB" -o "$EV/buf-case.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_B" -d "{\"case_no\":\"$CB\",\"name\":\"$CB\"}")
CASEID=$(jget < "$EV/buf-case.json" "['data']['id']")
chk "$CCODE" "201" "5: the buffer fixture case was created"
if [ -z "$CASEID" ] || [ "$CASEID" = "__ERR__" ]; then
  bad "5: the buffer fixture case id is not resolvable"
else
  SPOOL0=$(curl -s "$CALAPI/metrics" 2>/dev/null | awk '$1 ~ /^cs_scope_commit_gate_spooled_total/ {s+=$2} END{printf "%d", s+0}')
  python3 - "$EV/big.json" "$TS" <<'PY'
import sys
path, ts = sys.argv[1], sys.argv[2]
with open(path, "w") as f:
    f.write('{"name":"A7BIG-%s","groups":[' % ts)
    for i in range(320):
        if i:
            f.write(",")
        f.write('{"index":%d,"pad":"%s"}' % (i, "x" * 980))
    f.write("]}")
PY
  k=0
  BIGPIDS=""
  while [ "$k" -lt 4 ]; do
    k=$(( k + 1 ))
    python3 - "$EV/big.json" "$EV/big-$k.json" "$TS" "$k" <<'PY'
import sys
src, dst, ts, k = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4]
s = open(src).read().replace("A7BIG-" + ts, "A7BIG-" + ts + "-" + k)
open(dst, "w").write(s)
PY
    curl -s -b "$RB" -o "$EV/big-$k-body.bin" -w '%{http_code}' -m 180 \
      -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
      -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/big-$k.json" > "$EV/big-$k-code.txt" 2>/dev/null &
    BIGPIDS="$BIGPIDS $!"
  done
  curl -s -b "$RB" -o "$EV/abandoned-body.bin" -w '%{http_code}' -m 180 \
    -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/big.json" > "$EV/abandoned-code.txt" 2>/dev/null &
  APID=$!
  sleep 1
  kill -9 "$APID" 2>/dev/null || true
  for p in $BIGPIDS; do wait "$p" 2>/dev/null; done
  sleep 4
  BIGCODES=$(cat "$EV"/big-*-code.txt 2>/dev/null | sort | uniq -c | tr '\n' ' ')
  SPOOL1=$(curl -s "$CALAPI/metrics" 2>/dev/null | awk '$1 ~ /^cs_scope_commit_gate_spooled_total/ {s+=$2} END{printf "%d", s+0}')
  SPOOLFILES=$(ls -1 /tmp/scope-commit-gate-*.spool 2>/dev/null | wc -l)
  RSS1=$(ps -o rss= -p "$cand_pid" 2>/dev/null | tr -d ' ')
  echo "buffered_responses codes: $BIGCODES spool_counter=$SPOOL0->$SPOOL1 spool_files_left=$SPOOLFILES rss_kb=$RSS0->$RSS1"
  chk "$SPOOLFILES" "0" "5: no response spool file is left behind"
  chk "$(backends)" "0" "5: no commit-gate backend survived the abandoned client"
  chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "5: the candidate is still ready after the buffered load"
  if [ "$SPOOL1" -gt "$SPOOL0" ]; then ok "5: the buffered responses really spilled to disk ($(( SPOOL1 - SPOOL0 )) spills)"; else bad "5: no response spilled although every response exceeds the in-memory bound"; fi
  if [ -n "$RSS1" ] && [ "$RSS1" -lt $(( RSS0 + 262144 )) ]; then ok "5: RSS stayed bounded ($RSS0 -> $RSS1 kB)"; else bad "5: RSS grew past 256 MiB ($RSS0 -> $RSS1)"; fi
fi

echo "=== 8. final invariants ==="
cleanup
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(ls -1 /tmp/scope-commit-gate-*.spool 2>/dev/null | wc -l)" "0" "no spool file exists anywhere after the gate"
DISK1=$(df --output=avail -k / | tail -1 | tr -d ' ')
echo "disk_avail_kb=$DISK0->$DISK1"
if [ "$DISK1" -gt $(( DISK0 - 204800 )) ]; then ok "disk usage stayed bounded during the gate"; else bad "disk dropped by more than 200 MiB"; fi

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0
