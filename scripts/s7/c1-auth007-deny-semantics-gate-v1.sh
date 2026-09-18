#!/usr/bin/env bash
# c1-auth007-deny-semantics-gate-v1.sh
#
# AUTH-007 / P1-1 refusal-semantics gate.
#
# Defect measured here (proven by the frozen probe c1-auth007-revokeall-probe-v2.sh,
# evidence c1-auth007-revokeall2-s3-20260914T104439Z: PASS=46 FAIL=3): under real
# concurrency on POST /api/auth/v1/sessions/revoke-all, 5 of 16 requests were answered
# 403 AUTHZ_CSRF_FAILED although their session had simply been revoked by a sibling
# request. The refusal reason was wrong, the refusal left no audit row at all, and the
# client was told to refresh a CSRF token that could never become valid again.
#
# Invariants asserted (identical for every candidate; the frozen pre-fix candidate
# api-auth007-s3 is expected to VIOLATE them, which is this gate negative control):
#   * every concurrent request on the self-service mutation routes ends 200 or 401;
#     0 x 403, 0 x 5xx, 0 x 000;
#   * no PostgreSQL deadlock was broken, no blocking ring was sampled, every request
#     inside the AUTH-007 5 s budget;
#   * every session of the identity ends revoked, and the gated case writes commit
#     exactly when they answered 200 (business side effects match accepted writes);
#   * per request id: exactly ONE deny audit row for a refusal, carrying the stable
#     error code, the deny reason, the route and a trace id; ZERO for an accepted write;
#   * a LIVE session with a wrong or missing CSRF token is still 403 (the fix must not
#     weaken CSRF), and a dead session with a wrong token is 401 (session first);
#   * the real admin identity, the frozen production API binary and its readiness are
#     untouched by this gate.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is ever printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
LABEL="${LABEL:-deny}"
EXPECT="${EXPECT:-new}"
PORT="${PORT:-18171}"
ROUNDS="${ROUNDS:-4}"
CONC="${CONC:-8}"
GATED_ROUNDS="${GATED_ROUNDS:-3}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
SAMPLES="${SAMPLES:-40}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F"|" -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk '$6=="cs_user_session_csrf"{print $7}' "$1" | tail -1; }
rid_bound(){ grep -q "X-Request-Id: $2" "$1"; }
now_ms(){ date +%s%3N; }
cnt_code(){ awk -F"|" -v c="$2" '{ if ($3==c) n++ } END { print n+0 }' "$1"; }
cnt_bad(){ awk -F"|" '{ if ($3=="000" || $3 ~ /^5/) n++ } END { print n+0 }' "$1"; }
cnt_csrf(){ awk -F"|" '{ if ($3=="403") n++ } END { print n+0 }' "$1"; }
max_ms(){ awk -F"|" '{ if ($4+0>m) m=$4+0 } END { print m+0 }' "$1"; }
rows(){ awk -F"|" '{ if ($3!="000") n++ } END { print n+0 }' "$1"; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d" " -f1)"
echo "label=$LABEL expect=$EXPECT rounds=$ROUNDS conc=$CONC gated_rounds=$GATED_ROUNDS port=$PORT"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d" " -f1)"
echo "evidence=$EV"

echo "=== 0. runtime identity, candidate start, production untouched ==="
AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d" " -f1)" "$API_EXPECT_SHA" "production API binary is unchanged before the gate starts"
ADMIN_ID=$(pgq <<SQL
SELECT id::text FROM identities WHERE email='admin@local';
SQL
)
ADMIN_MCP=$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)
chk "$ADMIN_MCP" "true" "real admin still carries must_change_password=true (never touched)"
GATE_START=$(pgq <<SQL
SELECT now()::text;
SQL
)
echo "admin_identity=$ADMIN_ID gate_start=$GATE_START"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"

calib_pid=""
cleanup(){
  if [ -n "$calib_pid" ] && kill -0 "$calib_pid" 2>/dev/null; then
    kill -TERM "$calib_pid" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$calib_pid" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$calib_pid" 2>/dev/null || true
    sleep 1
  fi
  calib_pid=""
}
trap cleanup EXIT
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  unset SCOPE_CALIB_ENABLE
  nohup setsid "$BIN" >> "$EV/calib.log" 2>&1 &
  echo $! > "$EV/calib.pid" )
sleep 1
calib_pid=$(cat "$EV/calib.pid")
ready=0; rc=""
for _ in $(seq 1 180); do
  rc=$(curl -s -o "$EV/calib-ready.json" -w '%{http_code}' "$CALAPI/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate instance ready on $PORT (http $rc)"
chk "$(sha256sum /proc/$calib_pid/exe 2>/dev/null | cut -d" " -f1)" "$(sha256sum "$BIN" | cut -d" " -f1)" "running candidate is the binary under test"

echo "=== 1. fixture (dedicated c1lat organisation, two member workspaces) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 deny semantics acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
IID=$(pgq <<SQL
SELECT id::text FROM identities WHERE email='$INVEMAIL';
SQL
)
echo "ws_a=$WSA ws_b=$WSB fixture_identity=$IID"
if [ -z "$IID" ] || [ "$IID" = "__ERR__" ]; then bad "fixture identity not resolvable"; exit 0; fi
chk "$(pgq <<SQL
SELECT count(*) FROM workspace_memberships WHERE identity_id='$IID'::uuid AND status='active';
SQL
)" "2" "fixture identity really holds two active memberships"

PW1="Pw-$TS-c1deny-7e14"
jar=$EV/clear.jar
c=$(curl -s -c "$jar" -o "$EV/clear.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/login" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
  -d "$(printf '{"identifier":"%s","password":"%s","audience":"user","workspace_id":"%s"}' "$INVEMAIL" "$INVPW" "$WSA")")
chk "$c" "200" "fixture identity logs in with its initial password"
csrf=$(csrf_of "$jar")
c=$(curl -s -b "$jar" -o "$EV/clear-pw.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/password/change" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-CSRF-Token: $csrf" \
  -d "$(printf '{"current_password":"%s","new_password":"%s"}' "$INVPW" "$PW1")")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "initial-password flag cleared through the real endpoint"; else bad "initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear.json "$EV"/clear-pw.json

login_devices(){
  local tag=$1 n=$2 i j lc
  : > "$EV/$tag-jars.txt"
  for i in $(seq 1 "$n"); do
    j=$EV/$tag-s$i.jar
    rm -f "$j"
    lc=$(curl -s -c "$j" -o "$EV/$tag-s$i-login.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/login" \
      -H "Content-Type: application/json" -H "X-Auth-Audience: user" -A "c1-deny-$tag-dev$i" \
      -d "$(printf '{"identifier":"%s","password":"%s","audience":"user","workspace_id":"%s"}' "$INVEMAIL" "$PW1" "$WSA")")
    if [ "$lc" != "200" ]; then bad "$tag: device $i login (status=$lc)"; fi
    echo "$j" >> "$EV/$tag-jars.txt"
  done
}
live_sessions(){ pgq <<SQL
SELECT count(*) FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
}
flush_sessions(){
  local jar=$1 ck c
  ck=$(csrf_of "$jar")
  c=$(curl -s -b "$jar" -o /dev/null -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" \
    -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-CSRF-Token: $ck" -d '{"keep_current":false}')
  echo "flush_sessions status=$c live_after=$(live_sessions)"
}
sample_pg(){ pgq <<SQL 2>/dev/null
SELECT 'S|' || pid || '|' || state || '|' || coalesce(wait_event_type,'') || '/' || coalesce(wait_event,'') || '|' || array_to_string(pg_blocking_pids(pid), ',')
FROM pg_stat_activity
WHERE cardinality(pg_blocking_pids(pid)) > 0 OR application_name LIKE 'bpgate:%';
SELECT 'R|' || a.pid || '|' || b.pid
FROM pg_stat_activity a JOIN pg_stat_activity b ON b.pid = ANY(pg_blocking_pids(a.pid))
WHERE a.pid = ANY(pg_blocking_pids(b.pid));
SQL
}
deadlocks(){ pgq <<SQL
SELECT deadlocks FROM pg_stat_database WHERE datname='blockchain_platform';
SQL
}
audit_of_rids(){ pgq <<SQL
SELECT request_id || '|' || count(*) || '|' || coalesce(min(error_code),'') || '|' || coalesce(min(deny_reason),'') || '|' || coalesce(min(route_template),'') || '|' || count(*) filter (where coalesce(trace_id,'') = '') || '|' || coalesce(min(http_status),0)
FROM audit_events_v2
WHERE action='authorization.denied' AND request_id = ANY(string_to_array('$1', ','))
GROUP BY request_id;
SQL
}
start_sampler(){ ( for _ in $(seq 1 "$SAMPLES"); do sample_pg; sleep 0.05; done ) > "$1" 2>&1 & echo $!; }

DEAD0=$(deadlocks)
RINGS_TOTAL=0

echo "=== 2. G1: $ROUNDS rounds x $CONC concurrent revoke-all from $CONC devices of one identity ==="
for round in $(seq 1 "$ROUNDS"); do
  tag=g1r$round
  login_devices "$tag" "$CONC"
  chk "$(live_sessions)" "$CONC" "G1 round $round: exactly $CONC live sessions exist before the concurrent revoke"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  D0=$(deadlocks)
  SAMPID=$(start_sampler "$EV/$tag-pg-samples.log")
  : > "$EV/$tag-codes.txt"
  REQPIDS=""
  idx=0
  for j in $(cat "$EV/$tag-jars.txt"); do
    idx=$(( idx + 1 ))
    ck=$(csrf_of "$j")
    rid="c1deny-$TS-$tag-s$idx"
    ( T0=$(now_ms)
      code=$(curl -s -b "$j" -o "$EV/$tag-s$idx-body.json" -D "$EV/$tag-s$idx.h" -w '%{http_code}' -m 120 \
        -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" -H "Content-Type: application/json" \
        -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" \
        -d '{"keep_current":false}' )
      T1=$(now_ms)
      echo "$rid|$idx|$code|$(( T1 - T0 ))" >> "$EV/$tag-codes.txt" ) &
    REQPIDS="$REQPIDS $!"
  done
  for p in $REQPIDS; do wait "$p" 2>/dev/null; done
  wait "$SAMPID" 2>/dev/null
  D1=$(deadlocks)
  REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND revoked_at IS NOT NULL;
SQL
)
  TOTAL_SIDS=$(printf '%s' "$SIDS" | tr ',' ' ' | wc -w)
  EVENTS=$(pgq <<SQL
SELECT count(*) FROM login_events WHERE identity_id='$IID'::uuid AND occurred_at > now() - interval '10 minutes';
SQL
)
  N200=$(cnt_code "$EV/$tag-codes.txt" 200)
  N401=$(cnt_code "$EV/$tag-codes.txt" 401)
  N403=$(cnt_csrf "$EV/$tag-codes.txt")
  NBAD=$(cnt_bad "$EV/$tag-codes.txt")
  NROWS=$(rows "$EV/$tag-codes.txt")
  MMAX=$(max_ms "$EV/$tag-codes.txt")
  RINGS=$(grep -c "^R|" "$EV/$tag-pg-samples.log")
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  DEAD_DELTA=$(( D1 - D0 ))
  echo "G1 round=$round n200=$N200 n401=$N401 n403=$N403 nbad=$NBAD rows=$NROWS max_ms=$MMAX deadlock_delta=$DEAD_DELTA rings=$RINGS revoked=$REVOKED/$TOTAL_SIDS audit_login_events=$EVENTS"
  chk "$NROWS" "$CONC" "G1 round $round: all $CONC responses were received (no client-side loss)"
  chk "$N403" "0" "G1 round $round: no request was refused as a CSRF failure"
  chk "$NBAD" "0" "G1 round $round: no 000 and no 5xx"
  chk "$(( N200 + N401 ))" "$CONC" "G1 round $round: every request ended 200 or 401 (deterministic refusal set)"
  if [ "$N200" -ge 1 ]; then ok "G1 round $round: at least one concurrent revoke-all succeeded"; else bad "G1 round $round: no revoke-all succeeded"; fi
  if [ "$MMAX" -le "$THRESHOLD_MS" ]; then ok "G1 round $round: slowest revoke-all $MMAX ms is within $THRESHOLD_MS ms"; else bad "G1 round $round: slowest revoke-all $MMAX ms exceeds $THRESHOLD_MS ms"; fi
  chk "$DEAD_DELTA" "0" "G1 round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "G1 round $round: no blocking ring in the live sample"
  chk "$REVOKED" "$TOTAL_SIDS" "G1 round $round: every session of the identity ended revoked"
  if [ "$EVENTS" -ge "$N200" ]; then ok "G1 round $round: forced-logout audit rows ($EVENTS) cover every accepted request ($N200)"; else bad "G1 round $round: audit rows $EVENTS < accepted requests $N200"; fi

  RIDLIST=$(awk -F"|" '{print $1}' "$EV/$tag-codes.txt" | paste -sd, -)
  AUD=$(audit_of_rids "$RIDLIST")
  ABAD=0; BOUND=0
  while IFS="|" read -r rid idx code ms; do
    [ -n "$rid" ] || continue
    if rid_bound "$EV/$tag-s$idx.h" "$rid"; then BOUND=$(( BOUND + 1 )); fi
    row=$(printf '%s\n' "$AUD" | grep "^$rid|" || true)
    if [ "$code" = "200" ]; then
      if [ -n "$row" ]; then bad "G1 round $round s$idx: an accepted request left a deny audit row ($row)"; ABAD=$(( ABAD + 1 )); fi
    else
      cnt=$(printf '%s' "$row" | cut -d"|" -f2)
      ec=$(printf '%s' "$row" | cut -d"|" -f3)
      dr=$(printf '%s' "$row" | cut -d"|" -f4)
      rt=$(printf '%s' "$row" | cut -d"|" -f5)
      nt=$(printf '%s' "$row" | cut -d"|" -f6)
      st=$(printf '%s' "$row" | cut -d"|" -f7)
      if [ "$cnt" = "1" ] && [ "$ec" = "AUTHN_SESSION_MISSING" ] && [ "$dr" = "session_missing" ] && [ "$nt" = "0" ] && [ "$st" = "401" ]; then
        printf '%s' "$rt" | grep -q "revoke-all" || { bad "G1 round $round s$idx: deny audit row lost the route ($rt)"; ABAD=$(( ABAD + 1 )); }
      else
        bad "G1 round $round s$idx: deny audit row is wrong (rows=$cnt code=$ec reason=$dr missing_trace=$nt status=$st)"
        ABAD=$(( ABAD + 1 ))
      fi
    fi
  done < "$EV/$tag-codes.txt"
  chk "$BOUND" "$CONC" "G1 round $round: every response carried the request id the client sent (audit correlation is real)"
  if [ "$ABAD" -eq 0 ]; then ok "G1 round $round: every refusal carries exactly one deny row with reason session_missing, and no accepted write carries any"; fi
done

echo "=== 3. G2: self logout racing revoke-all across devices of one identity ==="
for round in $(seq 1 2); do
  tag=g2r$round
  login_devices "$tag" "$CONC"
  chk "$(live_sessions)" "$CONC" "G2 round $round: exactly $CONC live sessions before the mixed logout/revoke burst"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  D0=$(deadlocks)
  SAMPID=$(start_sampler "$EV/$tag-pg-samples.log")
  : > "$EV/$tag-codes.txt"
  REQPIDS=""
  idx=0
  for j in $(cat "$EV/$tag-jars.txt"); do
    idx=$(( idx + 1 ))
    ck=$(csrf_of "$j")
    rid="c1deny-$TS-$tag-s$idx"
    if [ $(( idx % 2 )) -eq 0 ]; then
      route="logout"; body='{}'
    else
      route="sessions/revoke-all"; body='{"keep_current":false}'
    fi
    ( T0=$(now_ms)
      code=$(curl -s -b "$j" -o "$EV/$tag-s$idx-body.json" -D "$EV/$tag-s$idx.h" -w '%{http_code}' -m 120 \
        -X POST "$CALAPI/api/auth/v1/$route" -H "Content-Type: application/json" \
        -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" \
        -d "$body" )
      T1=$(now_ms)
      echo "$rid|$idx|$code|$(( T1 - T0 ))|$route" >> "$EV/$tag-codes.txt" ) &
    REQPIDS="$REQPIDS $!"
  done
  for p in $REQPIDS; do wait "$p" 2>/dev/null; done
  wait "$SAMPID" 2>/dev/null
  D1=$(deadlocks)
  REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND revoked_at IS NOT NULL;
SQL
)
  TOTAL_SIDS=$(printf '%s' "$SIDS" | tr ',' ' ' | wc -w)
  N200=$(cnt_code "$EV/$tag-codes.txt" 200)
  N401=$(cnt_code "$EV/$tag-codes.txt" 401)
  N403=$(cnt_csrf "$EV/$tag-codes.txt")
  NBAD=$(cnt_bad "$EV/$tag-codes.txt")
  NROWS=$(rows "$EV/$tag-codes.txt")
  MMAX=$(max_ms "$EV/$tag-codes.txt")
  RINGS=$(grep -c "^R|" "$EV/$tag-pg-samples.log")
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  DEAD_DELTA=$(( D1 - D0 ))
  echo "G2 round=$round n200=$N200 n401=$N401 n403=$N403 nbad=$NBAD rows=$NROWS max_ms=$MMAX deadlock_delta=$DEAD_DELTA rings=$RINGS revoked=$REVOKED/$TOTAL_SIDS"
  chk "$NROWS" "$CONC" "G2 round $round: all $CONC responses were received"
  chk "$N403" "0" "G2 round $round: no logout/revoke request was refused as a CSRF failure"
  chk "$NBAD" "0" "G2 round $round: no 000 and no 5xx"
  chk "$(( N200 + N401 ))" "$CONC" "G2 round $round: every request ended 200 or 401"
  if [ "$MMAX" -le "$THRESHOLD_MS" ]; then ok "G2 round $round: slowest request $MMAX ms is within $THRESHOLD_MS ms"; else bad "G2 round $round: slowest request $MMAX ms exceeds $THRESHOLD_MS ms"; fi
  chk "$DEAD_DELTA" "0" "G2 round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "G2 round $round: no blocking ring in the live sample"
  chk "$REVOKED" "$TOTAL_SIDS" "G2 round $round: every session of the identity ended revoked"
  RIDLIST=$(awk -F"|" '{print $1}' "$EV/$tag-codes.txt" | paste -sd, -)
  AUD=$(audit_of_rids "$RIDLIST")
  ABAD=0
  while IFS="|" read -r rid idx code ms route; do
    [ -n "$rid" ] || continue
    row=$(printf '%s\n' "$AUD" | grep "^$rid|" || true)
    if [ "$code" = "200" ]; then
      if [ -n "$row" ]; then bad "G2 round $round s$idx: accepted $route left a deny audit row ($row)"; ABAD=$(( ABAD + 1 )); fi
    else
      dr=$(printf '%s' "$row" | cut -d"|" -f4)
      cnt=$(printf '%s' "$row" | cut -d"|" -f2)
      if [ "$cnt" = "1" ] && [ "$dr" = "session_missing" ]; then
        :
      else
        bad "G2 round $round s$idx: refused $route has a wrong deny audit row (rows=$cnt reason=$dr)"
        ABAD=$(( ABAD + 1 ))
      fi
    fi
  done < "$EV/$tag-codes.txt"
  if [ "$ABAD" -eq 0 ]; then ok "G2 round $round: logout and revoke-all refusals are audited exactly once with reason session_missing"; fi
done

echo "=== 4. G3: workspace switch racing revoke-all on a two-workspace session ==="
for round in $(seq 1 2); do
  tag=g3r$round
  login_devices "$tag" "$CONC"
  chk "$(live_sessions)" "$CONC" "G3 round $round: exactly $CONC live sessions before the switch/revoke burst"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  D0=$(deadlocks)
  SAMPID=$(start_sampler "$EV/$tag-pg-samples.log")
  : > "$EV/$tag-codes.txt"
  REQPIDS=""
  idx=0
  for j in $(cat "$EV/$tag-jars.txt"); do
    idx=$(( idx + 1 ))
    ck=$(csrf_of "$j")
    rid="c1deny-$TS-$tag-s$idx"
    if [ $(( idx % 2 )) -eq 0 ]; then
      route="sessions/revoke-all"; body='{"keep_current":false}'
    else
      route="session/workspace"; body=$(printf '{"workspace_id":"%s"}' "$WSB")
    fi
    ( T0=$(now_ms)
      code=$(curl -s -b "$j" -o "$EV/$tag-s$idx-body.json" -D "$EV/$tag-s$idx.h" -w '%{http_code}' -m 120 \
        -X POST "$CALAPI/api/auth/v1/$route" -H "Content-Type: application/json" \
        -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" \
        -d "$body" )
      T1=$(now_ms)
      echo "$rid|$idx|$code|$(( T1 - T0 ))|$route" >> "$EV/$tag-codes.txt" ) &
    REQPIDS="$REQPIDS $!"
  done
  for p in $REQPIDS; do wait "$p" 2>/dev/null; done
  wait "$SAMPID" 2>/dev/null
  D1=$(deadlocks)
  NS200=$(awk -F"|" '{ if ($3=="200" && $5=="session/workspace") n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  NSOTHER=$(awk -F"|" '{ if ($5=="session/workspace" && $3 !~ /^(200|401|409|000)$/) n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  NS409=$(awk -F"|" '{ if ($5=="session/workspace" && $3=="409") n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  NS401=$(awk -F"|" '{ if ($5=="session/workspace" && $3=="401") n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  MOVED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND workspace_id='$WSB'::uuid;
SQL
)
  N403=$(cnt_csrf "$EV/$tag-codes.txt")
  NBAD=$(cnt_bad "$EV/$tag-codes.txt")
  NROWS=$(rows "$EV/$tag-codes.txt")
  N4XX=$(awk -F"|" '{ if ($3 ~ /^4/ && $3 != "403") n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  MMAX=$(max_ms "$EV/$tag-codes.txt")
  RINGS=$(grep -c "^R|" "$EV/$tag-pg-samples.log")
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  DEAD_DELTA=$(( D1 - D0 ))
  echo "G3 round=$round switch200=$NS200 switch401=$NS401 switch409=$NS409 other_switch_codes=$NSOTHER moved_to_b=$MOVED n403=$N403 nbad=$NBAD rows=$NROWS max_ms=$MMAX deadlock_delta=$DEAD_DELTA rings=$RINGS"
  chk "$NSOTHER" "0" "G3 round $round: every workspace switch ended 200, 401 or 409 (a CAS conflict is fail-closed, never a CSRF claim)"
  chk "$NROWS" "$CONC" "G3 round $round: all $CONC responses were received"
  chk "$N403" "0" "G3 round $round: neither a switch nor a revoke was refused as a CSRF failure"
  chk "$NBAD" "0" "G3 round $round: no 000 and no 5xx"
  chk "$MOVED" "$NS200" "G3 round $round: sessions that reported a successful switch really carry workspace B"
  if [ "$MMAX" -le "$THRESHOLD_MS" ]; then ok "G3 round $round: slowest request $MMAX ms is within $THRESHOLD_MS ms"; else bad "G3 round $round: slowest request $MMAX ms exceeds $THRESHOLD_MS ms"; fi
  chk "$DEAD_DELTA" "0" "G3 round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "G3 round $round: no blocking ring in the live sample"
  chk "$(live_sessions)" "0" "G3 round $round: every session of the identity ended revoked or moved, none left live"
done
echo "--- G3 positive control: a switch with no concurrent revoke must succeed ---"
tag=g3ctl
login_devices "$tag" 1
SIDC=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
ckc=$(csrf_of "$EV/$tag-s1.jar")
ridc="c1deny-$TS-g3ctl"
codec=$(curl -s -b "$EV/$tag-s1.jar" -o "$EV/g3ctl-switch.json" -D "$EV/g3ctl-switch.h" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/session/workspace" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $ridc" -H "X-CSRF-Token: $ckc" \
  -d "$(printf '{"workspace_id":"%s"}' "$WSB")")
chk "$codec" "200" "G3 positive control: switching to the other member workspace succeeds when nothing races it"
chk "$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDC', ',')::uuid[]) AND workspace_id='$WSB'::uuid;
SQL
)" "1" "G3 positive control: the session row really moved to workspace B"
chk "$(live_sessions)" "1" "G3 positive control: the session stayed live across the switch"
flush_sessions "$EV/g3ctl-s1.jar"

echo "=== 5. G4: target-session revoke racing identity-wide revoke-all ==="
for round in $(seq 1 2); do
  tag=g4r$round
  login_devices "$tag" "$CONC"
  chk "$(live_sessions)" "$CONC" "G4 round $round: exactly $CONC live sessions before the target/identity revoke burst"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  TARGET=$(printf '%s' "$SIDS" | cut -d, -f1)
  D0=$(deadlocks)
  SAMPID=$(start_sampler "$EV/$tag-pg-samples.log")
  : > "$EV/$tag-codes.txt"
  REQPIDS=""
  idx=0
  for j in $(cat "$EV/$tag-jars.txt"); do
    idx=$(( idx + 1 ))
    ck=$(csrf_of "$j")
    rid="c1deny-$TS-$tag-s$idx"
    if [ "$idx" = "1" ]; then
      method="DELETE"; route="sessions/$TARGET"; body=""
    else
      method="POST"; route="sessions/revoke-all"; body='{"keep_current":false}'
    fi
    ( T0=$(now_ms)
      if [ -n "$body" ]; then
        code=$(curl -s -b "$j" -o "$EV/$tag-s$idx-body.json" -D "$EV/$tag-s$idx.h" -w '%{http_code}' -m 120 \
          -X "$method" "$CALAPI/api/auth/v1/$route" -H "Content-Type: application/json" \
          -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" -d "$body" )
      else
        code=$(curl -s -b "$j" -o "$EV/$tag-s$idx-body.json" -D "$EV/$tag-s$idx.h" -w '%{http_code}' -m 120 \
          -X "$method" "$CALAPI/api/auth/v1/$route" \
          -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" )
      fi
      T1=$(now_ms)
      echo "$rid|$idx|$code|$(( T1 - T0 ))|$method $route" >> "$EV/$tag-codes.txt" ) &
    REQPIDS="$REQPIDS $!"
  done
  for p in $REQPIDS; do wait "$p" 2>/dev/null; done
  wait "$SAMPID" 2>/dev/null
  D1=$(deadlocks)
  TARGET_REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$TARGET'::uuid AND revoked_at IS NOT NULL;
SQL
)
  REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND revoked_at IS NOT NULL;
SQL
)
  TOTAL_SIDS=$(printf '%s' "$SIDS" | tr ',' ' ' | wc -w)
  N403=$(cnt_csrf "$EV/$tag-codes.txt")
  NBAD=$(cnt_bad "$EV/$tag-codes.txt")
  NROWS=$(rows "$EV/$tag-codes.txt")
  MMAX=$(max_ms "$EV/$tag-codes.txt")
  RINGS=$(grep -c "^R|" "$EV/$tag-pg-samples.log")
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  DEAD_DELTA=$(( D1 - D0 ))
  echo "G4 round=$round target_revoked=$TARGET_REVOKED revoked=$REVOKED/$TOTAL_SIDS n403=$N403 nbad=$NBAD rows=$NROWS max_ms=$MMAX deadlock_delta=$DEAD_DELTA rings=$RINGS"
  chk "$NROWS" "$CONC" "G4 round $round: all $CONC responses were received"
  chk "$N403" "0" "G4 round $round: no targeted or identity-wide revoke was refused as a CSRF failure"
  chk "$NBAD" "0" "G4 round $round: no 000 and no 5xx"
  chk "$TARGET_REVOKED" "1" "G4 round $round: the targeted session really ended revoked"
  chk "$REVOKED" "$TOTAL_SIDS" "G4 round $round: every session of the identity ended revoked"
  if [ "$MMAX" -le "$THRESHOLD_MS" ]; then ok "G4 round $round: slowest request $MMAX ms is within $THRESHOLD_MS ms"; else bad "G4 round $round: slowest request $MMAX ms exceeds $THRESHOLD_MS ms"; fi
  chk "$DEAD_DELTA" "0" "G4 round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "G4 round $round: no blocking ring in the live sample"
done

echo "=== 6. G5: require/requireAny gated write (POST /api/v2/cases) racing identity-wide revoke ==="
for round in $(seq 1 "$GATED_ROUNDS"); do
  tag=g5r$round
  login_devices "$tag" "$CONC"
  chk "$(live_sessions)" "$CONC" "G5 round $round: exactly $CONC live sessions before the gated-write burst"
  SIDS=$(pgq <<SQL
SELECT string_agg(id::text, ',') FROM sessions WHERE identity_id='$IID'::uuid AND revoked_at IS NULL;
SQL
)
  BEFORE=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND name LIKE 'DENY-$TS-$tag%';
SQL
)
  D0=$(deadlocks)
  SAMPID=$(start_sampler "$EV/$tag-pg-samples.log")
  : > "$EV/$tag-codes.txt"
  REQPIDS=""
  idx=0
  for j in $(cat "$EV/$tag-jars.txt"); do
    idx=$(( idx + 1 ))
    ck=$(csrf_of "$j")
    rid="c1deny-$TS-$tag-w$idx"
    ( T0=$(now_ms)
      code=$(curl -s -b "$j" -o "$EV/$tag-w$idx-body.json" -D "$EV/$tag-w$idx.h" -w '%{http_code}' -m 120 \
        -X POST "$CALAPI/api/v2/cases" -H "Content-Type: application/json" -H "X-Auth-Audience: user" \
        -H "X-Request-Id: $rid" -H "X-CSRF-Token: $ck" \
        -d "$(printf '{"case_no":"DENY-%s-%s-w%s","name":"DENY-%s-%s-w%s","description":"gated write racing identity-wide revoke"}' "$TS" "$tag" "$idx" "$TS" "$tag" "$idx")" )
      T1=$(now_ms)
      echo "$rid|$idx|$code|$(( T1 - T0 ))|cases" >> "$EV/$tag-codes.txt" ) &
    REQPIDS="$REQPIDS $!"
  done
  j1=$(head -1 "$EV/$tag-jars.txt")
  ck1=$(csrf_of "$j1")
  ridv="c1deny-$TS-$tag-revoker"
  ( T0=$(now_ms)
    code=$(curl -s -b "$j1" -o "$EV/$tag-revoker-body.json" -D "$EV/$tag-revoker.h" -w '%{http_code}' -m 120 \
      -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" -H "Content-Type: application/json" \
      -H "X-Auth-Audience: user" -H "X-Request-Id: $ridv" -H "X-CSRF-Token: $ck1" \
      -d '{"keep_current":false}' )
    T1=$(now_ms)
    echo "$ridv|0|$code|$(( T1 - T0 ))|revoker" >> "$EV/$tag-codes.txt" ) &
  REQPIDS="$REQPIDS $!"
  for p in $REQPIDS; do wait "$p" 2>/dev/null; done
  wait "$SAMPID" 2>/dev/null
  sleep 2
  D1=$(deadlocks)
  AFTER=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND name LIKE 'DENY-$TS-$tag%';
SQL
)
  CREATED=$(( AFTER - BEFORE ))
  N200=$(cnt_code "$EV/$tag-codes.txt" 200)
  NOK=$(awk -F"|" '{ if ($5=="cases" && $3 ~ /^2/) n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  NREF=$(awk -F"|" '{ if ($5=="cases" && $3 !~ /^2/) n++ } END { print n+0 }' "$EV/$tag-codes.txt")
  N403=$(cnt_csrf "$EV/$tag-codes.txt")
  NBAD=$(cnt_bad "$EV/$tag-codes.txt")
  NROWS=$(rows "$EV/$tag-codes.txt")
  MMAX=$(max_ms "$EV/$tag-codes.txt")
  RINGS=$(grep -c "^R|" "$EV/$tag-pg-samples.log")
  RINGS_TOTAL=$(( RINGS_TOTAL + RINGS ))
  DEAD_DELTA=$(( D1 - D0 ))
  REVOKED=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id = ANY(string_to_array('$SIDS', ',')::uuid[]) AND revoked_at IS NOT NULL;
SQL
)
  TOTAL_SIDS=$(printf '%s' "$SIDS" | tr ',' ' ' | wc -w)
  echo "G5 round=$round created=$CREATED accepted2xx=$NOK refused=$NREF revoker200=$N200 n403=$N403 nbad=$NBAD rows=$NROWS max_ms=$MMAX revoked=$REVOKED/$TOTAL_SIDS deadlock_delta=$DEAD_DELTA rings=$RINGS"
  chk "$NROWS" "$(( CONC + 1 ))" "G5 round $round: all gated writes and the revoker answered"
  chk "$N403" "0" "G5 round $round: no gated write was refused as a CSRF failure while its session was revoked"
  chk "$NBAD" "0" "G5 round $round: no 000 and no 5xx"
  chk "$CREATED" "$NOK" "G5 round $round: committed cases equal the accepted (2xx) gated writes (no partial side effect)"
  chk "$(( NOK + NREF ))" "$CONC" "G5 round $round: every gated write answered with a real status"
  if [ "$NREF" -eq "$CONC" ]; then echo "NOTE G5 round $round: the identity-wide revoke won the race before every gated write reached its commit"; fi
  if [ "$MMAX" -le "$THRESHOLD_MS" ]; then ok "G5 round $round: slowest request $MMAX ms is within $THRESHOLD_MS ms"; else bad "G5 round $round: slowest request $MMAX ms exceeds $THRESHOLD_MS ms"; fi
  chk "$DEAD_DELTA" "0" "G5 round $round: PostgreSQL had to break no deadlock"
  chk "$RINGS" "0" "G5 round $round: no blocking ring in the live sample"
  chk "$REVOKED" "$TOTAL_SIDS" "G5 round $round: the identity-wide revoke really revoked every session"
  RIDLIST=$(awk -F"|" '{print $1}' "$EV/$tag-codes.txt" | paste -sd, -)
  AUD=$(audit_of_rids "$RIDLIST")
  NROWS2=$(printf '%s\n' "$AUD" | grep -c .)
  CBD=$(printf '%s' "$AUD" | grep -c "csrf_failed" || true)
  chk "$CBD" "0" "G5 round $round: no refusal in this round was audited as a CSRF failure"
  DUP=0
  while IFS="|" read -r rid idx code ms route; do
    [ -n "$rid" ] || continue
    row=$(printf '%s\n' "$AUD" | grep "^$rid|" || true)
    cnt=$(printf '%s' "$row" | cut -d"|" -f2)
    if [ -n "$row" ] && [ "$cnt" != "1" ]; then bad "G5 round $round $route rid=$rid has $cnt deny rows, expected at most one"; DUP=$(( DUP + 1 )); fi
    case "$code" in
      2*) accepted=1 ;;
      *) accepted=0 ;;
    esac
    if [ "$accepted" = "0" ] && [ "$code" != "000" ]; then
      if [ -z "$row" ]; then bad "G5 round $round $route rid=$rid was refused with $code but has no deny audit row"; DUP=$(( DUP + 1 )); fi
      case "$row" in
        *session_missing*|*session_scope_changed*|*session_scope_unverifiable*) : ;;
        *) bad "G5 round $round $route rid=$rid refused with $code but the deny reason is outside the documented scope-refusal set ($row)"; DUP=$(( DUP + 1 )) ;;
      esac
    fi
    if [ "$accepted" = "1" ] && [ -n "$row" ]; then bad "G5 round $round accepted write rid=$rid still left a deny row ($row)"; DUP=$(( DUP + 1 )); fi
  done < "$EV/$tag-codes.txt"
  echo "G5 round=$round audit_rows_for_round=$NROWS2"
  if [ "$DUP" -eq 0 ]; then ok "G5 round $round: deny audit rows are exactly once per refused request and absent for accepted writes"; fi
done
echo "--- G5 positive control: a gated write with no concurrent revoke must commit ---"
tag=g5ctl
login_devices "$tag" 1
BEFORE=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND name LIKE 'DENY-$TS-$tag%';
SQL
)
ckc=$(csrf_of "$EV/$tag-s1.jar")
ridc="c1deny-$TS-g5ctl"
codec=$(curl -s -b "$EV/$tag-s1.jar" -o "$EV/g5ctl.json" -D "$EV/g5ctl.h" -w '%{http_code}' -X POST "$CALAPI/api/v2/cases" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $ridc" -H "X-CSRF-Token: $ckc" \
  -d "$(printf '{"case_no":"DENY-%s-%s","name":"DENY-%s-%s","description":"gated write positive control"}' "$TS" "$tag" "$TS" "$tag")")
AFTER=$(pgq <<SQL
SELECT count(*) FROM cases WHERE workspace_id='$WSA'::uuid AND name LIKE 'DENY-$TS-$tag%';
SQL
)
echo "G5 positive control code=$codec created=$(( AFTER - BEFORE ))"
chk "$codec" "201" "G5 positive control: an unraced gated write commits with 201"
chk "$(( AFTER - BEFORE ))" "1" "G5 positive control: exactly one case row was committed"
flush_sessions "$EV/g5ctl-s1.jar"

echo "=== 7. CSRF must not be weakened (positive controls) ==="
tag=g6
login_devices "$tag" 2
LIVE_G6=$(live_sessions)
echo "g6_live_sessions_before_controls=$LIVE_G6"
jar=$EV/$tag-s1.jar
csrf=$(csrf_of "$jar")
wrong="0000000000000000000000000000000000000000000000000000000000000000"
rid="c1deny-$TS-g6-wrong"
code=$(curl -s -b "$jar" -o "$EV/g6-wrong.json" -D "$EV/g6-wrong.h" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $wrong" -d '{"keep_current":false}')
chk "$code" "403" "a live session with a wrong CSRF token is still 403 (CSRF not relaxed)"
grep -q "AUTHZ_CSRF_FAILED" "$EV/g6-wrong.json" && ok "the wrong-token refusal carries AUTHZ_CSRF_FAILED" || bad "the wrong-token refusal lost its stable code"
ROW=$(audit_of_rids "$rid")
echo "g6_wrong_token_audit=$ROW"
printf '%s' "$ROW" | grep -q "AUTHZ_CSRF_FAILED" && ok "the wrong-token refusal is audited with AUTHZ_CSRF_FAILED" || bad "the wrong-token refusal has no accurate audit row"
printf '%s' "$ROW" | grep -q "csrf_failed" && ok "the wrong-token refusal is audited with reason csrf_failed" || bad "the wrong-token refusal audit lost reason csrf_failed"
chk "$(live_sessions)" "$LIVE_G6" "the wrong-token refusal revoked nothing (the live session count is unchanged)"

rid="c1deny-$TS-g6-notoken"
code=$(curl -s -b "$jar" -o "$EV/g6-notoken.json" -D "$EV/g6-notoken.h" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -d '{"keep_current":false}')
chk "$code" "403" "a live session with no CSRF header is still 403"

rid="c1deny-$TS-g6-ok"
code=$(curl -s -b "$jar" -o "$EV/g6-ok.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $csrf" -d '{"keep_current":false}')
chk "$code" "200" "the same live session with its real token still succeeds (no false refusal)"
chk "$(live_sessions)" "0" "the accepted revoke-all really revoked every session of the identity (keep_current=false)"
jar2=$EV/$tag-s2.jar
csrf2=$(csrf_of "$jar2")
rid="c1deny-$TS-g6-dead"
code=$(curl -s -b "$jar2" -o "$EV/g6-dead.json" -w '%{http_code}' -X POST "$CALAPI/api/auth/v1/sessions/revoke-all" \
  -H "Content-Type: application/json" -H "X-Auth-Audience: user" -H "X-Request-Id: $rid" -H "X-CSRF-Token: $wrong" -d '{"keep_current":false}')
chk "$code" "401" "a dead session is refused 401 whatever token it presents (session is judged first)"
ROW=$(audit_of_rids "$rid")
echo "g6_dead_audit=$ROW"
printf '%s' "$ROW" | grep -q "AUTHN_SESSION_MISSING" && ok "the dead-session refusal is audited with AUTHN_SESSION_MISSING" || bad "the dead-session refusal audit lost the session-missing code"
if [ "$csrf2" = "$wrong" ]; then bad "calibration setup: the second device unexpectedly shares the wrong token"; fi

echo "=== 8. final invariants ==="
DEAD1=$(deadlocks)
echo "deadlocks_total_delta=$(( DEAD1 - DEAD0 )) rings_total=$RINGS_TOTAL"
chk "$(( DEAD1 - DEAD0 ))" "0" "no deadlock was detected across all rounds"
chk "$RINGS_TOTAL" "0" "no blocking ring was observed across all rounds"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "candidate instance is still ready"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
)" "0" "no commit-gate backend is left behind"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(pgq <<SQL
SELECT count(*) FROM sessions WHERE identity_id='$ADMIN_ID'::uuid AND revoked_at > '$GATE_START'::timestamptz;
SQL
)" "0" "no real admin session was revoked by this gate"
cleanup
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d" " -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"

echo "=== summary ==="
echo "expect=$EXPECT PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
if [ "$EXPECT" = "old" ]; then
  if [ "$FAIL" -gt 0 ]; then echo "NEGATIVE_CONTROL_DETECTED=1"; else echo "NEGATIVE_CONTROL_DETECTED=0"; fi
fi
exit 0
