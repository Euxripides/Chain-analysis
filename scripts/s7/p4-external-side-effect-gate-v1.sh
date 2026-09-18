#!/usr/bin/env bash
# p4-external-side-effect-gate-v1.sh
#
# P4: a REVOKED request must cause ZERO non-PostgreSQL side effects.
#
# The commit gate wraps a handler in one PostgreSQL transaction and rolls it back
# when the caller scope went stale while the handler was running. PostgreSQL rolls
# back. NATS, MinIO and ClickHouse do not. The matrix
# (scripts/s7/p4_external_matrix_v1.py) says 44 gated write routes reach an
# external dependency and 18 of them have a direct side effect with no outbox and
# no scope re-check. This gate measures the consequence instead of arguing about
# it, category by category, with a real counter for each class:
#
#   bus        NATS JetStream message count (monitoring endpoint /jsz?streams=1)
#   object     MinIO object count on the bind-mounted data directory
#   clickhouse ClickHouse system.query_log row count
#
# Every round is an A/B pair on the SAME route with the SAME delay:
#   POSITIVE   the request runs with a valid scope              -> side effect must happen
#   NEGATIVE   the caller scope is invalidated during the delay -> 409, and the side
#              effect count must stay exactly where it was
#
# The delay is the calibration seam at phase "post": the authorization decision has
# been taken, the handler has not run yet. A request that is going to be rolled back
# therefore still runs its handler -- which is exactly the window in which a direct
# side effect leaks.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-p4ext}"
BIN="${BIN:-$R/backend/bin.new/api-auth007-deny-calib}"
PORT="${PORT:-18190}"
DELAY_MS="${DELAY_MS:-3000}"
READY_TRIES="${READY_TRIES:-360}"
NATS_MON="${NATS_MON:-http://127.0.0.1:8222}"
MINIO_DATA="${MINIO_DATA:-/data/blockchain-platform/minio}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/p4-ext-$LABEL-$TS; fi
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
chq(){ docker exec -i deploy-clickhouse-1 clickhouse-client --query "$1" 2>/dev/null | tr -d '[:space:]'; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
now_ms(){ date +%s%3N; }

nats_messages(){ curl -s -m 5 "$NATS_MON/jsz?streams=1" 2>/dev/null | jget "['messages']"; }
minio_objects(){ ls -1R "$MINIO_DATA/$1" 2>/dev/null | grep -c . ; }
minio_snapshot(){ ls -1R "$MINIO_DATA/$1" 2>/dev/null > "$2"; }
ch_rows(){ chq "SELECT count() FROM system.query_log WHERE event_time > now() - INTERVAL $1 SECOND"; }
# The candidate own Prometheus counters: a provider round needs a counter for the
# OUTBOUND call, not for a database effect, so it reads the two cumulative series
# the provider adapters increment per request.
metric_sum(){ curl -s "$CALAPI/metrics" 2>/dev/null | awk -v m="$1" '$1 ~ ("^" m) { s += $2 } END { printf "%d", s + 0 }'; }
provider_calls(){ echo $(( $(metric_sum ankr_request_duration_seconds_count) + $(metric_sum rpc_batch_calls_total) )); }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL bin=$BIN port=$PORT delay_ms=$DELAY_MS"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "counter_probe nats=$(nats_messages) minio_audit=$(minio_objects blockchain-audit) minio_exports=$(minio_objects blockchain-exports) ch_rows_60s=$(ch_rows 60)"

echo "=== fixture (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 external side effect acceptance" \
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
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
INVPW1="Pw-$TS-p4ext-3f21"
SECPW1="Pw-$TS-p4ext-88a0"

clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" aud="${5:-user}" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$WSA\"}")
  chk "$c" "200" "$tag fixture logs in with its initial password"
  csrf=$(csrf_of "$jar" "cs_${aud}_session_csrf")
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "$tag initial-password flag cleared"; else bad "$tag initial-password flag cleared (status=$c)"; fi
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1" user
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1" admin
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json

login_ws(){
  local j=$1 email=$2 pw=$3 ws=$4 aud=$5 out=$6
  rm -f "$j"
  curl -s -c "$j" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$ws\"}"
}
RA=$EV/inv.jar
chk "$(login_ws "$RA" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/inv-login.json")" "200" "investigator session logs in to workspace A"
INV_CSRF=$(csrf_of "$RA" cs_user_session_csrf)
SID_INV=$(sess_id "$RA" user)
echo "inv_session=$SID_INV ws_a=$WSA ws_b=$WSB"

echo "=== candidate (calibration build, seam at phase post) ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
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
( cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export SCOPE_CALIB_ENABLE=1
  export SCOPE_CALIB_PHASE=post
  export SCOPE_CALIB_DELAY_MS="$DELAY_MS"
  unset SCOPE_CALIB_ROUTE
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
  echo "READINESS_ABORT=1 no round is started, so no round result can be a cascade of this race."
  echo "=== summary ==="; echo "PASS=$PASS FAIL=$FAIL NV=$NV"; echo "GATE_FAIL=$FAIL"
  exit 0
fi
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running candidate is the binary under test"

# ---------------------------------------------------------------------------
# ROUND 1: bus. The admin force revoke publishes the SSE scope-control message.
# Positive: revoke a victim session -> 200 and exactly one message.
# Negative: the revoke is issued and the CALLER admin session is revoked during the
# delay by a second admin session, so the dedicated handler re-verifies a revoked
# actor and refuses. The victim must stay alive and no message may be published.
# ---------------------------------------------------------------------------
echo "=== ROUND 1 (bus): admin force revoke ==="
A1=$EV/a1.jar; B1=$EV/a1b.jar
chk "$(login_ws "$A1" "$SECEMAIL" "$SECPW1" "$WSA" admin "$EV/a1-login.json")" "200" "1: admin session A logs in"
chk "$(login_ws "$B1" "$SECEMAIL" "$SECPW1" "$WSA" admin "$EV/a1b-login.json")" "200" "1: admin session B logs in (the concurrent revoker)"
A1_CSRF=$(csrf_of "$A1" cs_admin_session_csrf); B1_CSRF=$(csrf_of "$B1" cs_admin_session_csrf)
SID_A1=$(sess_id "$A1" admin); SID_B1=$(sess_id "$B1" admin)
V1=$EV/v1.jar
chk "$(login_ws "$V1" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/v1-login.json")" "200" "1: victim session logs in"
SID_V1=$(sess_id "$V1" user)

NAT_POS0=$(nats_messages)
CPOS=$(curl -s -b "$A1" -o "$EV/r1-pos.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/admin/v1/sessions/$SID_V1/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{"confirm":true,"reason":"p4-bus-positive"}')
sleep 1
NAT_POS1=$(nats_messages)
REV_V1=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_V1'::uuid AND revoked_at IS NOT NULL;
SQL
)
echo "r1_positive http=$CPOS revoked=$REV_V1 nats=$NAT_POS0->$NAT_POS1 delta=$(( NAT_POS1 - NAT_POS0 ))"
chk "$CPOS" "200" "1: the positive force revoke succeeded"
chk "$REV_V1" "1" "1: the positive force revoke really revoked the victim"
DPOS=$(( NAT_POS1 - NAT_POS0 ))
if [ "$DPOS" -ge 1 ]; then ok "1: the positive revoke published to the bus ($DPOS message(s))"; else bad "1: the positive revoke published nothing, so the bus counter cannot discriminate"; fi

V2=$EV/v2.jar
chk "$(login_ws "$V2" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/v2-login.json")" "200" "1: second victim session logs in"
SID_V2=$(sess_id "$V2" user)
NAT_NEG0=$(nats_messages)
curl -s -b "$A1" -o "$EV/r1-neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/admin/v1/sessions/$SID_V2/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{"confirm":true,"reason":"p4-bus-negative"}' > "$EV/r1-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
# Invalidate the CALLER during the handler delay. It has to be a path that is NOT
# behind require/requireAny, otherwise the calibration delay would delay the
# invalidation too and the caller would win its own race: the self-service logout
# runs through requireSession and is therefore immediate.
CNEG=$(curl -s -b "$A1" -c "$A1" -o "$EV/r1-revoker.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{}')
REV_A1=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_A1'::uuid AND revoked_at IS NOT NULL;
SQL
)
wait $NPID
sleep 1
NAT_NEG1=$(nats_messages)
R1CODE=$(cat "$EV/r1-neg-code.txt")
REV_V2=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_V2'::uuid AND revoked_at IS NOT NULL;
SQL
)
DNEG=$(( NAT_NEG1 - NAT_NEG0 ))
echo "r1_negative caller_logout=$CNEG http=$R1CODE caller_revoked=$REV_A1 victim_revoked=$REV_V2 nats=$NAT_NEG0->$NAT_NEG1 delta=$DNEG"
chk "$CNEG" "200" "1: the caller logout that invalidated the scope succeeded"
chk "$REV_A1" "1" "1: the caller session really is revoked in the database"
if [ "$R1CODE" = "409" ] || [ "$R1CODE" = "401" ]; then ok "1: the revoked caller was refused ($R1CODE)"; else bad "1: the request from a revoked caller was not refused (http=$R1CODE)"; fi
chk "$REV_V2" "0" "1: the refused request did not revoke its target"
chk "$DNEG" "0" "1: the refused request published ZERO bus messages"
if [ "$DNEG" = "0" ] && [ "$DPOS" -ge 1 ]; then ok "1: the bus counter discriminates (positive $DPOS, negative 0)"; else bad "1: the bus counter did not discriminate (positive $DPOS, negative $DNEG)"; fi

# ---------------------------------------------------------------------------
# ROUND 2: clickhouse. POST /api/v2/downloads/preview queries ClickHouse.
# ---------------------------------------------------------------------------
echo "=== ROUND 2 (clickhouse): download preview ==="
PREVIEW_BODY='{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["transfers"]}'
CH_POS0=$(ch_rows 10)
C2=$(curl -s -b "$RA" -o "$EV/r2-pos.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4-$TS-pos" -d "$PREVIEW_BODY")
sleep 2
CH_POS1=$(ch_rows 10)
echo "r2_positive http=$C2 ch_rows=$CH_POS0->$CH_POS1 delta=$(( CH_POS1 - CH_POS0 ))"
if [ "$C2" = "200" ]; then ok "2: the preview answered 200"; else bad "2: the preview did not answer 200 (http=$C2)"; fi
CHD_POS=$(( CH_POS1 - CH_POS0 ))
if [ "$CHD_POS" -ge 1 ]; then ok "2: the positive preview issued ClickHouse queries ($CHD_POS logged)"; else nv "2: no ClickHouse query was logged for the positive preview (counter cannot discriminate)"; fi

INV_SID_NOW=$(sess_id "$RA" user)
CH_NEG0=$(ch_rows 10)
curl -s -b "$RA" -o "$EV/r2-neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4-$TS-neg" -d "$PREVIEW_BODY" > "$EV/r2-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CSW=$(curl -s -b "$RA" -c "$RA" -o "$EV/r2-switch.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSB\"}")
wait $NPID
sleep 2
CH_NEG1=$(ch_rows 10)
R2CODE=$(cat "$EV/r2-neg-code.txt")
CHD_NEG=$(( CH_NEG1 - CH_NEG0 ))
echo "r2_negative switch=$CSW http=$R2CODE ch_rows=$CH_NEG0->$CH_NEG1 delta=$CHD_NEG"
chk "$CSW" "200" "2: the workspace switch during the delay succeeded"
if [ "$R2CODE" = "409" ]; then ok "2: the stale-scope preview was refused with 409"; else bad "2: the stale-scope preview was not refused with 409 (http=$R2CODE)"; fi
if [ "$CHD_POS" -ge 1 ]; then
  chk "$CHD_NEG" "0" "2: the refused preview issued ZERO ClickHouse queries"
else
  nv "2: the ClickHouse counter never moved on the positive control, so the negative delta is not a measurement"
fi
# switch the investigator back for later rounds
curl -s -b "$RA" -c "$RA" -o "$EV/r2-switch-back.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/r2-switch-back-code.txt" 2>/dev/null
chk "$(cat "$EV/r2-switch-back-code.txt")" "200" "2: the investigator session was switched back to workspace A"

# ---------------------------------------------------------------------------
# ROUND 3: object. POST /api/v1/exports writes the export artifact to MinIO.
# ---------------------------------------------------------------------------
echo "=== ROUND 3 (object): export artifact ==="
EXPORT_BODY='{"dataset":"transfers","format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"]}'
MIN_POS0=$(minio_objects blockchain-exports)
C3=$(curl -s -b "$RA" -o "$EV/r3-pos.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4-$TS-exp-pos" -d "$EXPORT_BODY")
sleep 6
MIN_POS1=$(minio_objects blockchain-exports)
ROWS_POS=$(pgq <<SQL
SELECT count(*) FROM exports WHERE created_at > now() - interval '3 minutes';
SQL
)
echo "r3_positive http=$C3 minio=$MIN_POS0->$MIN_POS1 delta=$(( MIN_POS1 - MIN_POS0 )) export_rows_3m=$ROWS_POS"
if [ "$C3" = "200" ] || [ "$C3" = "201" ] || [ "$C3" = "202" ]; then ok "3: the positive export was accepted ($C3)"; else bad "3: the positive export was not accepted (http=$C3)"; fi
MD_POS=$(( MIN_POS1 - MIN_POS0 ))
if [ "$MD_POS" -ge 1 ]; then ok "3: the positive export wrote an object to MinIO"; else nv "3: the positive export wrote no object within the wait, so the object counter cannot discriminate"; fi

# Settle first: the positive export may still be finishing asynchronously, and an
# object it writes during the negative window would otherwise be read as a leak.
sleep 8
MIN_NEG0=$(minio_objects blockchain-exports)
minio_snapshot blockchain-exports "$EV/minio-before-negative.txt"
BEFORE_NEG_ROWS=$(pgq <<SQL
SELECT count(*) FROM exports WHERE created_at > now() - interval '3 minutes';
SQL
)
curl -s -b "$RA" -o "$EV/r3-neg.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4-$TS-exp-neg" -d "$EXPORT_BODY" > "$EV/r3-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CSW3=$(curl -s -b "$RA" -c "$RA" -o "$EV/r3-switch.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSB\"}")
wait $NPID
sleep 8
MIN_NEG1=$(minio_objects blockchain-exports)
minio_snapshot blockchain-exports "$EV/minio-after-negative.txt"
NEW_OBJECTS=$(diff "$EV/minio-before-negative.txt" "$EV/minio-after-negative.txt" 2>/dev/null | grep -c '^>' || true)
AFTER_NEG_ROWS=$(pgq <<SQL
SELECT count(*) FROM exports WHERE created_at > now() - interval '3 minutes';
SQL
)
R3CODE=$(cat "$EV/r3-neg-code.txt")
MD_NEG=$(( MIN_NEG1 - MIN_NEG0 ))
echo "r3_negative switch=$CSW3 http=$R3CODE minio=$MIN_NEG0->$MIN_NEG1 delta=$MD_NEG new_object_names=$NEW_OBJECTS export_rows_3m=$BEFORE_NEG_ROWS->$AFTER_NEG_ROWS"
chk "$CSW3" "200" "3: the workspace switch during the delay succeeded"
if [ "$R3CODE" = "409" ]; then ok "3: the stale-scope export was refused with 409"; else bad "3: the stale-scope export was not refused with 409 (http=$R3CODE)"; fi
if [ "$MD_POS" -ge 1 ]; then
  chk "$MD_NEG" "0" "3: the refused export wrote ZERO MinIO objects"
  chk "$NEW_OBJECTS" "0" "3: the refused export added no new object name under the exports bucket"
else
  nv "3: the object counter never moved on the positive control, so the negative delta is not a measurement"
fi
curl -s -b "$RA" -c "$RA" -o "$EV/r3-switch-back.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/r3-switch-back-code.txt" 2>/dev/null

# ---------------------------------------------------------------------------
# ROUND 4: provider. POST /api/v2/downloads/validate-addresses resolves address
# types through the chain provider. There is no independent counter for a third
# party call unless the provider endpoint is redirected, which would stop the
# round from being a real-provider measurement, so this round reports the two
# observable facts it does have: the status of the positive and of the refused
# request, and the provider-derived rows the refused request would have created.
# ---------------------------------------------------------------------------
echo "=== ROUND 4 (provider): address type resolution ==="
PROV_BODY='{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000002","0x0000000000000000000000000000000000000003"]}'
PC_POS0=$(provider_calls)
C4=$(curl -s -b "$RA" -o "$EV/r4-pos.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/validate-addresses" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -d "$PROV_BODY")
sleep 3
PC_POS1=$(provider_calls)
echo "r4_positive http=$C4 provider_calls=$PC_POS0->$PC_POS1 delta=$(( PC_POS1 - PC_POS0 ))"
if [ "$C4" = "200" ]; then ok "4: the positive address validation answered 200"; else bad "4: the positive address validation did not answer 200 (http=$C4)"; fi
PCD_POS=$(( PC_POS1 - PC_POS0 ))
if [ "$PCD_POS" -ge 1 ]; then ok "4: the positive validation really called the provider ($PCD_POS outbound call(s) counted)"; else nv "4: the provider counters did not move on the positive control, so the negative delta is not a measurement"; fi
PC_NEG0=$(provider_calls)
curl -s -b "$RA" -o "$EV/r4-neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/validate-addresses" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -d "$PROV_BODY" > "$EV/r4-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CSW4=$(curl -s -b "$RA" -c "$RA" -o "$EV/r4-switch.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSB\"}")
wait $NPID
sleep 3
PC_NEG1=$(provider_calls)
R4CODE=$(cat "$EV/r4-neg-code.txt")
PCD_NEG=$(( PC_NEG1 - PC_NEG0 ))
echo "r4_negative switch=$CSW4 http=$R4CODE provider_calls=$PC_NEG0->$PC_NEG1 delta=$PCD_NEG"
chk "$CSW4" "200" "4: the workspace switch during the delay succeeded"
if [ "$R4CODE" = "409" ]; then ok "4: the stale-scope address validation was refused with 409"; else bad "4: the stale-scope address validation was not refused with 409 (http=$R4CODE)"; fi
if [ "$PCD_POS" -ge 1 ]; then
  chk "$PCD_NEG" "0" "4: the refused validation made ZERO outbound provider calls"
else
  nv "4: the provider call counter never moved on the positive control, so the negative delta is not a measurement"
fi
curl -s -b "$RA" -c "$RA" -o "$EV/r4-switch-back.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/r4-switch-back-code.txt" 2>/dev/null

echo "=== 8. final invariants ==="
cleanup
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "the candidate port is released"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
)" "0" "no commit-gate backend is left behind"
chk "$(ls -1 /tmp/scope-commit-gate-*.spool 2>/dev/null | wc -l)" "0" "no spool file exists anywhere after the gate"
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL NV=$NV"
echo "GATE_FAIL=$FAIL"
exit 0
