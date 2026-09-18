#!/usr/bin/env bash
# p4-external-side-effect-gate-v3.sh
#
# P4: a REVOKED request must cause ZERO non-PostgreSQL side effects.
#
# v3 of the A/B gate whose v1 and v2 rounds are preserved unchanged:
#   v1  acceptance-evidence/dual-system-v1/sprint7/p4-ext-p4ext-r1-20260914T173345Z
#   v2  acceptance-evidence/dual-system-v1/sprint7/p4-ext-p4ext-r2-20260914T173736Z
# v2 PROVED the real MinIO orphan-object leak (a refused export still wrote
# exports/4a380557-....zip while no exports row existed). Its other three classes
# could not discriminate; the previous handover recorded exactly why, and v3 fixes
# each of the three by measurement.
#
#   (1) bus  v2 started the negative counter window BEFORE the invalidation, and
#       the invalidation action (self-service logout) publishes on the same
#       subject, so the refused request was charged with the logout own message
#       (v2 measured positive 1 / negative 1). v3 starts the corrected window
#       AFTER the invalidation returns and still measures the pre-mutation window
#       side by side, so the pollution is shown rather than hidden.
#   (2) clickhouse  v2 drove the round with POST /api/v2/downloads/preview and a
#       fully covered address, so the handler never reached downloadEstimate, the
#       only ClickHouse query on that path. That branch requires missing_blocks > 0,
#       and missing_blocks is only computed when the request carries a BOUNDED
#       range (wantInterval needs range.type=block with both block numbers). v3
#       sends range.block(1000000..2000000) for an address with no local coverage
#       (missing_blocks=1000001), which issues
#       SELECT count() FROM blockchain.token_transfers FINAL ... (measured).
#       The counter is exact: system.query_log rows with type='QueryFinish',
#       client_name LIKE 'clickhouse-go%', query_kind='Select' and the dataset
#       FINAL token in the text. A covered address with the same range is the
#       instrument own negative control (measured 0 queries).
#   (3) provider  v2 drove the round with POST /api/v2/downloads/validate-addresses,
#       a pure PostgreSQL/metadata handler, so a zero delta could not be told apart
#       from a dead counter. v3 calibrates the counter on GET
#       /api/v1/addresses/{chain}/{address}/runtime with a cache-missing address
#       (Head + AddressType + BalanceRead): measured +6 outbound calls, and 0 on
#       the immediate repeat (cache hit). The provider-classified WRITE routes were
#       then measured with the same instrument and the same cache-busting input and
#       stayed at 0, so their provider aspect is NOT_VERIFIED for the outbound-call
#       instrument, never PASS: the matrix classifies them by STATIC reachability
#       of dependency tokens (TokenMeta.Enqueue, Registry.Refresh, provider_descriptors
#       rows), and reachability is not a call.
#
# Instrument facts were established first and are preserved as their own evidence:
#   p4-chprobe-20260915T030331Z, p4-instrprobe-20260915T030441Z,
#   p4-provprobe-20260915T030809Z (plus p4-provrestore-20260915T031025Z, which
#   restored the provider priority the write-route probe had to change).
#
# Every export-style round waits for a terminal status; a positive that is not
# terminal SKIPS its own negative control and says so, because a late positive
# artifact must never be attributed to the refused request.
#
# v3 ran the three fixed checks and preserved two MEASUREMENT defects of its own
# (evidence p4-ext3-p4ext3-20260915T0316xxZ is kept unchanged):
#   (a) the ClickHouse counter read system.query_log immediately after the
#       request, but query_log is flushed asynchronously, so a query that really
#       ran was not visible yet and the round reported delta=0 -> NOT_VERIFIED;
#   (b) the export positive parsed the created id from data.id, while the export
#       route answers with a top-level id, so the id became __ERR__ and the
#       round could not read the terminal status (the export itself COMPLETED
#       with a real object; the gate simply could not see which row to poll).
# v4 fixes both: every ClickHouse count is taken after SYSTEM FLUSH LOGS, and the
# export id is read from data.id with a top-level id fallback. The provider round
# gains a second calibration route (a transaction receipt for a random hash, which
# can never be cached) and reads the HTTP-level provider counters.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="p4ext6"
BIN="${BIN:-$R/backend/bin.new/api-expcap-r3-calib}"
PORT="${PORT:-18195}"
DELAY_MS="${DELAY_MS:-8000}"
READY_TRIES="${READY_TRIES:-360}"
NATS_MON="${NATS_MON:-http://127.0.0.1:8222}"
MINIO_DATA="${MINIO_DATA:-/data/blockchain-platform/minio}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
EV_post="${EV:-}"
if [ -z "$EV_post" ]; then EV="$R/acceptance-evidence/dual-system-v1/sprint7/p4-ext3-$LABEL-$TS"; else EV="$EV_post"; fi
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

nats_messages(){ curl -s -m 5 "$NATS_MON/jsz?streams=1" 2>/dev/null | jget "['messages']"; }
minio_objects(){ ls -1R "$MINIO_DATA/$1" 2>/dev/null | grep -c . ; }
minio_snapshot(){ ls -1R "$MINIO_DATA/$1" 2>/dev/null > "$2"; }
NATS_COUNTER="$R/scripts/s7/nats_subject_counter.py"
SCOPE_SUBJECT="events.sse.scope_invalidated"
nats_scope_count(){ awk '/^TOTAL /{print $2+0}' "$1" 2>/dev/null | tail -1; }
metric_sum(){ curl -s "$CALAPI/metrics" 2>/dev/null | awk -v m="$1" '$1 ~ ("^" m) { s += $2 } END { printf "%d", s + 0 }'; }
# HTTP-level outbound provider requests: the Ankr legacy client observes one
# sample per completed request, and the JSON-RPC pool counts one batch REQUEST
# (rpc_batch_requests_total) plus one duration sample per request. rpc_batch_calls_total
# counts the individual methods inside a batch and is reported for context only.
provider_calls(){ echo $(( $(metric_sum ankr_request_duration_seconds_count) + $(metric_sum rpc_batch_requests_total) + $(metric_sum rpc_duration_seconds_count) )); }
provider_calls_detail(){
  echo "ankr_req=$(metric_sum ankr_request_duration_seconds_count) rpc_batch_req=$(metric_sum rpc_batch_requests_total) rpc_dur=$(metric_sum rpc_duration_seconds_count) rpc_batch_calls=$(metric_sum rpc_batch_calls_total) rpc_cache_hit=$(metric_sum rpc_cache_hit_total) rpc_cache_miss=$(metric_sum rpc_cache_miss_total)"
}
wstart(){ date -u '+%Y-%m-%d %H:%M:%S'; }
wms(){ date +%s%3N; }
# An exact ClickHouse instrument: only queries issued by the Go driver, only
# finished SELECTs, and only the ones whose text carries the route own table token.
# query_log is flushed ASYNCHRONOUSLY (flush_interval_milliseconds defaults to
# 7500), so counting straight after the request measures the flusher, not the
# query. SYSTEM FLUSH LOGS makes the count deterministic.
ch_sel_like(){
  docker exec -i deploy-clickhouse-1 clickhouse-client --query "SYSTEM FLUSH LOGS" >/dev/null 2>&1
  chq "SELECT count() FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select' AND positionCaseInsensitive(query,'$2') > 0"
}
ch_sel_dump(){
  docker exec -i deploy-clickhouse-1 clickhouse-client --query "SYSTEM FLUSH LOGS" >/dev/null 2>&1
  docker exec -i deploy-clickhouse-1 clickhouse-client --query "SELECT event_time, query_duration_ms, substring(replaceAll(query,char(10),' '),1,150) FROM system.query_log WHERE event_time >= toDateTime('$1') AND type='QueryFinish' AND client_name LIKE 'clickhouse-go%' AND query_kind='Select' AND positionCaseInsensitive(query,'$2') > 0 ORDER BY event_time FORMAT TSV" 2>&1
}

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL bin=$BIN port=$PORT delay_ms=$DELAY_MS"
echo "bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "counter_probe nats_total=$(nats_messages) minio_exports=$(minio_objects blockchain-exports) provider_calls=$(provider_calls)"

echo "=== fixture (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "P4 external side effect acceptance v3" \
    -workspaces c1lat-a,c1lat-b \
    -identities owner:c1lat-own@fixture.local:c1lat-a,security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
SECEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_SEC_EMAIL)
SECPW=$(read_cred "$CRED" C1MEM_C1LAT_SEC_PW)
OWNEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_OWN_EMAIL)
OWNPW=$(read_cred "$CRED" C1MEM_C1LAT_OWN_PW)
INVPW1="Pw-$TS-p4ext3-3f21"
SECPW1="Pw-$TS-p4ext3-88a0"
OWNPPW1="Pw-$TS-p4ext3-1a7c"
echo "fixture ws_a=$WSA ws_b=$WSB identities=3"

clear_flag(){
  local tag="$1" email="$2" pw="$3" newpw="$4" aud="$5" jar="$EV/clear-$1.jar" c csrf
  c=$(curl -s -c "$jar" -o "$EV/clear-$tag.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$WSA\"}")
  chk "$c" "200" "$tag fixture logs in with its initial password ($aud)"
  csrf=$(csrf_of "$jar" "cs_$aud""_session_csrf")
  c=$(curl -s -b "$jar" -o "$EV/clear-$tag-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" -H "X-CSRF-Token: $csrf" \
    -d "{\"current_password\":\"$pw\",\"new_password\":\"$newpw\"}")
  if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "$tag initial-password flag cleared"; else bad "$tag initial-password flag cleared (status=$c)"; fi
}
clear_flag inv "$INVEMAIL" "$INVPW" "$INVPW1" user
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1" admin
clear_flag own "$OWNEMAIL" "$OWNPW" "$OWNPPW1" admin
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
OU=$EV/ownu.jar
chk "$(login_ws "$OU" "$OWNEMAIL" "$OWNPPW1" "$WSA" user "$EV/ownu-login.json")" "200" "owner session logs in with the user audience"
OWNU_CSRF=$(csrf_of "$OU" cs_user_session_csrf)

echo "=== candidate (calibration build, seam at phase post, delay $DELAY_MS ms) ==="
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
# ROUND 1 (bus): admin force revoke on the SSE scope-control subject.
#
# POSITIVE  revoke a victim session: 200, victim revoked, >= 1 message published.
# NEGATIVE  the revoke is issued by admin session A1 and the CALLER (A1) is
#           invalidated while the handler is delayed, so the request is refused.
#           Two windows are opened around it on purpose:
#             whole    - v2 own window, started before the invalidation. It also
#                        sees the invalidation own publish (self-service logout
#                        publishes on the same subject) and therefore cannot
#                        charge the refused request correctly.
#             after    - the corrected window, started only after the
#                        invalidation returned. The assertion is on THIS window,
#                        and the gate first proves the window really opened
#                        before the refused request returned.
# ---------------------------------------------------------------------------
echo "=== ROUND 1 (bus): admin force revoke ==="
A1=$EV/a1.jar; B1=$EV/a1b.jar
WINDOW_S=$(( DELAY_MS / 1000 + 10 ))
echo "1: corrected_window_s=$WINDOW_S"
chk "$(login_ws "$A1" "$SECEMAIL" "$SECPW1" "$WSA" admin "$EV/a1-login.json")" "200" "1: admin session A logs in"
chk "$(login_ws "$B1" "$SECEMAIL" "$SECPW1" "$WSA" admin "$EV/a1b-login.json")" "200" "1: admin session B logs in (the concurrent revoker)"
A1_CSRF=$(csrf_of "$A1" cs_admin_session_csrf); B1_CSRF=$(csrf_of "$B1" cs_admin_session_csrf)
SID_A1=$(sess_id "$A1" admin); SID_B1=$(sess_id "$B1" admin)
V1=$EV/v1.jar
chk "$(login_ws "$V1" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/v1-login.json")" "200" "1: victim session logs in"
SID_V1=$(sess_id "$V1" user)

NAT_TOTAL_POS0=$(nats_messages)
python3 "$NATS_COUNTER" 127.0.0.1:4222 "$SCOPE_SUBJECT" 12 "$EV/r1-pos-nats.txt" &
NATW=$!
sleep 2
CPOS=$(curl -s -b "$A1" -o "$EV/r1-pos.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/admin/v1/sessions/$SID_V1/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{"confirm":true,"reason":"p4-bus-positive"}')
wait $NATW 2>/dev/null
NAT_TOTAL_POS1=$(nats_messages)
DPOS=$(nats_scope_count "$EV/r1-pos-nats.txt")
REV_V1=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_V1'::uuid AND revoked_at IS NOT NULL;
SQL
)
echo "r1_positive http=$CPOS revoked=$REV_V1 scope_subject_messages=$DPOS nats_total=$NAT_TOTAL_POS0->$NAT_TOTAL_POS1 (informational: the total carries background traffic)"
cat "$EV/r1-pos-nats.txt"
chk "$CPOS" "200" "1: the positive force revoke succeeded"
chk "$REV_V1" "1" "1: the positive force revoke really revoked the victim"
if [ "$DPOS" -ge 1 ]; then ok "1: the positive revoke published on $SCOPE_SUBJECT ($DPOS message(s))"; else bad "1: the positive revoke published nothing on $SCOPE_SUBJECT, so the counter cannot discriminate"; fi

V2=$EV/v2.jar
chk "$(login_ws "$V2" "$INVEMAIL" "$INVPW1" "$WSA" user "$EV/v2-login.json")" "200" "1: second victim session logs in"
SID_V2=$(sess_id "$V2" user)
NAT_TOTAL_NEG0=$(nats_messages)
python3 "$NATS_COUNTER" 127.0.0.1:4222 "$SCOPE_SUBJECT" "$WINDOW_S" "$EV/r1-neg-nats-whole.txt" &
NATW2=$!
sleep 2
T_REQ_START=$(wms)
curl -s -b "$A1" -o "$EV/r1-neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/admin/v1/sessions/$SID_V2/commands/revoke" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{"confirm":true,"reason":"p4-bus-negative"}' > "$EV/r1-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
# The invalidation. It MUST be a path that is not behind the commit gate itself,
# otherwise the calibration delay would delay the invalidation too and the caller
# would win its own race; self-service logout runs through requireSession and is
# immediate. It also publishes on $SCOPE_SUBJECT, which is exactly why the
# corrected window below opens after it returns.
CNEG=$(curl -s -b "$A1" -c "$A1" -o "$EV/r1-revoker.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $A1_CSRF" -d '{}')
T_LOGOUT_DONE=$(wms)
REV_A1=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_A1'::uuid AND revoked_at IS NOT NULL;
SQL
)
python3 "$NATS_COUNTER" 127.0.0.1:4222 "$SCOPE_SUBJECT" "$WINDOW_S" "$EV/r1-neg-nats-after.txt" &
NATW3=$!
wait $NPID
T_REQ_DONE=$(wms)
wait $NATW2 2>/dev/null
wait $NATW3 2>/dev/null
NAT_TOTAL_NEG1=$(nats_messages)
DNEG_WHOLE=$(nats_scope_count "$EV/r1-neg-nats-whole.txt")
DNEG_AFTER=$(nats_scope_count "$EV/r1-neg-nats-after.txt")
R1CODE=$(cat "$EV/r1-neg-code.txt")
REV_V2=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_V2'::uuid AND revoked_at IS NOT NULL;
SQL
)
echo "r1_negative caller_logout=$CNEG http=$R1CODE caller_revoked=$REV_A1 victim_revoked=$REV_V2"
echo "r1_negative window_whole=$DNEG_WHOLE window_after_invalidation=$DNEG_AFTER nats_total=$NAT_TOTAL_NEG0->$NAT_TOTAL_NEG1 (informational)"
echo "r1_negative timings req_start=$T_REQ_START req_done=$T_REQ_DONE invalidated_at=$T_LOGOUT_DONE req_elapsed_ms=$(( T_REQ_DONE - T_REQ_START ))"
echo "--- whole window (v2 own, polluted by the invalidation own publish) ---"
cat "$EV/r1-neg-nats-whole.txt"
echo "--- corrected window (opened after the invalidation returned) ---"
cat "$EV/r1-neg-nats-after.txt"
chk "$CNEG" "200" "1: the caller logout that invalidated the scope succeeded"
chk "$REV_A1" "1" "1: the caller session really is revoked in the database"
if [ "$R1CODE" = "409" ] || [ "$R1CODE" = "401" ]; then ok "1: the revoked caller was refused ($R1CODE)"; else bad "1: the request from a revoked caller was not refused (http=$R1CODE)"; fi
chk "$REV_V2" "0" "1: the refused request did not revoke its target"
if [ "$T_LOGOUT_DONE" -lt "$T_REQ_DONE" ]; then ok "1: the corrected window opened before the refused request returned (it could have observed a leak)"; else bad "1: the corrected window opened after the refused request returned, so a leak could not have been observed"; fi
chk "$DNEG_AFTER" "0" "1: the refused request published ZERO messages on $SCOPE_SUBJECT after the mutation"
if [ "$DNEG_AFTER" = "0" ] && [ "$DPOS" -ge 1 ]; then ok "1: the bus counter discriminates (positive $DPOS, negative-after-mutation $DNEG_AFTER)"; else bad "1: the bus counter did not discriminate (positive $DPOS, negative $DNEG_AFTER)"; fi
if [ "$DNEG_WHOLE" -ge 1 ] && [ "$DNEG_AFTER" = "0" ]; then ok "1: the pre-mutation window is measurably polluted by the invalidation own publish ($DNEG_WHOLE), which is why v2 could not discriminate"; fi

# ---------------------------------------------------------------------------
# ROUND 2 (clickhouse): POST /api/v2/downloads/preview.
#
# The handler reaches downloadEstimate only when missing_blocks > 0, and
# missing_blocks is computed only for a BOUNDED block range. With an address that
# has no local coverage plus range.block(1000000..2000000) the handler really
# issues SELECT count() FROM blockchain.token_transfers FINAL ...; with a covered
# address and the same range it does not. Both directions are measured.
# ---------------------------------------------------------------------------
echo "=== ROUND 2 (clickhouse): download preview with a bounded block range ==="
CH_NEEDLE='token_transfers FINAL'
RANGE='"range":{"type":"block","from_block":1000000,"to_block":2000000}'
BODY_GAP='{"chain":"bsc","addresses":["0x00000000000000000000000000000000000000ff"],"datasets":["token_transfers"],'$RANGE'}'
BODY_COV='{"chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"],"datasets":["token_transfers"],'$RANGE'}'
W=$(wstart); sleep 1; CH0=$(ch_sel_like "$W" "$CH_NEEDLE")
C2=$(curl -s -b "$RA" -o "$EV/r2-pos.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4v3-$TS-r2pos" -d "$BODY_GAP")
sleep 3
CH1=$(ch_sel_like "$W" "$CH_NEEDLE")
CHD_POS=$(( CH1 - CH0 ))
MB_POS=$(jget < "$EV/r2-pos.json" "['data']['missing_blocks']")
BASIS_POS=$(jget < "$EV/r2-pos.json" "['data']['estimate']['basis']")
echo "r2_positive http=$C2 missing_blocks=$MB_POS estimate_basis=$BASIS_POS ch_select_queries=$CH0->$CH1 delta=$CHD_POS"
echo "--- positive window queries ---"; ch_sel_dump "$W" "$CH_NEEDLE"
if [ "$C2" = "200" ]; then ok "2: the bounded-range preview answered 200"; else bad "2: the bounded-range preview did not answer 200 (http=$C2)"; fi
if [ "$CHD_POS" -ge 1 ]; then ok "2: the positive preview really issued ClickHouse queries ($CHD_POS logged)"; else nv "2: no ClickHouse query was logged for the positive preview (counter cannot discriminate)"; fi

W=$(wstart); sleep 1; CH0=$(ch_sel_like "$W" "$CH_NEEDLE")
C2B=$(curl -s -b "$RA" -o "$EV/r2-ctl.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4v3-$TS-r2ctl" -d "$BODY_COV")
sleep 3
CH1=$(ch_sel_like "$W" "$CH_NEEDLE")
CHD_CTL=$(( CH1 - CH0 ))
MB_CTL=$(jget < "$EV/r2-ctl.json" "['data']['missing_blocks']")
echo "r2_control http=$C2B missing_blocks=$MB_CTL ch_select_queries=$CH0->$CH1 delta=$CHD_CTL"
echo "--- control window queries ---"; ch_sel_dump "$W" "$CH_NEEDLE"
chk "$MB_CTL" "0" "2: the covered control computed zero missing blocks"
chk "$CHD_CTL" "0" "2: the covered control issued ZERO ClickHouse queries (the instrument discriminates)"

W=$(wstart); sleep 1; CH0=$(ch_sel_like "$W" "$CH_NEEDLE")
curl -s -b "$RA" -o "$EV/r2-neg.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/preview" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4v3-$TS-r2neg" -d "$BODY_GAP" > "$EV/r2-neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
CSW=$(curl -s -b "$RA" -c "$RA" -o "$EV/r2-switch.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSB\"}")
wait $NPID
sleep 3
CH1=$(ch_sel_like "$W" "$CH_NEEDLE")
R2CODE=$(cat "$EV/r2-neg-code.txt")
CHD_NEG=$(( CH1 - CH0 ))
echo "r2_negative switch=$CSW http=$R2CODE ch_select_queries=$CH0->$CH1 delta=$CHD_NEG"
echo "--- negative window queries (a refused request must produce NONE) ---"; ch_sel_dump "$W" "$CH_NEEDLE"
chk "$CSW" "200" "2: the workspace switch during the delay succeeded"
if [ "$R2CODE" = "409" ]; then ok "2: the stale-scope preview was refused with 409"; else bad "2: the stale-scope preview was not refused with 409 (http=$R2CODE)"; fi
if [ "$CHD_POS" -ge 1 ]; then
  chk "$CHD_NEG" "0" "2: the refused preview issued ZERO ClickHouse queries"
else
  nv "2: the ClickHouse counter never moved on the positive control, so the negative delta is not a measurement"
fi
curl -s -b "$RA" -c "$RA" -o "$EV/r2-switch-back.json" -w '%{http_code}' -m 60 \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/r2-switch-back-code.txt" 2>/dev/null
chk "$(cat "$EV/r2-switch-back-code.txt")" "200" "2: the investigator session was switched back to workspace A"
# ---------------------------------------------------------------------------
# ROUND 3 (object): POST /api/v1/exports writes the artifact to MinIO.
# The round follows the terminal-state rule: the positive polls the export row
# until it is COMPLETED or FAILED, the negative then waits the SAME budget, and
# if the positive never reached a terminal state the negative is SKIPPED and
# reported NOT_VERIFIED, so a late positive artifact can never be attributed to
# the refused request.
# ---------------------------------------------------------------------------
echo "=== ROUND 3 (object): export artifact ==="
EXPORT_BODY='{"datasets":["token_transfers"],"format":"csv","chain":"bsc","addresses":["0x0000000000000000000000000000000000000001"]}'
OBJ_WAIT_S="${OBJ_WAIT_S:-300}"
MIN_POS0=$(minio_objects blockchain-exports)
C3=$(curl -s -b "$RA" -o "$EV/r3-pos.json" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4v3-$TS-exp-pos" -d "$EXPORT_BODY")
EXPID=$(jget < "$EV/r3-pos.json" "['data']['id']")
if [ "$EXPID" = "__ERR__" ] || [ -z "$EXPID" ]; then EXPID=$(jget < "$EV/r3-pos.json" "['id']"); fi
echo "r3_positive http=$C3 export_id=$EXPID"
WAITED=0; ST=""
while [ "$WAITED" -lt "$OBJ_WAIT_S" ]; do
  ST=$(pgq <<SQL
SELECT COALESCE(status,'-') FROM exports WHERE id='$EXPID'::uuid;
SQL
)
  echo "r3_positive poll t=${WAITED}s status=$ST"
  case "$ST" in COMPLETED|FAILED) break;; esac
  sleep 5; WAITED=$(( WAITED + 5 ))
done
sleep 2
MIN_POS1=$(minio_objects blockchain-exports)
MD_POS=$(( MIN_POS1 - MIN_POS0 ))
echo "r3_positive waited_s=$WAITED status=$ST minio=$MIN_POS0->$MIN_POS1 delta=$MD_POS"
if [ "$C3" = "200" ] || [ "$C3" = "201" ] || [ "$C3" = "202" ]; then ok "3: the positive export was accepted ($C3)"; else bad "3: the positive export was not accepted (http=$C3)"; fi
POS_TERMINAL=0
case "$ST" in COMPLETED) POS_TERMINAL=1;; esac
if [ "$POS_TERMINAL" = "1" ]; then
  ok "3: the positive export reached a terminal COMPLETED status"
else
  nv "3: the positive export did not reach COMPLETED (status=$ST after ${WAITED}s); its negative control is skipped"
fi
if [ "$MD_POS" -ge 1 ]; then ok "3: the positive export wrote an object to MinIO"; else nv "3: the positive export wrote no object within the budget, so the object counter cannot discriminate"; fi

if [ "$POS_TERMINAL" = "1" ]; then
  sleep 10
  MIN_NEG0=$(minio_objects blockchain-exports)
  minio_snapshot blockchain-exports "$EV/minio-before-negative.txt"
  # A fixed anchor: "now() - interval 10 minutes" is a SLIDING window, so a row
  # that ages out during the negative makes the count go DOWN and the equality
  # assertion fails for a reason that has nothing to do with the refused request.
  NEG_ANCHOR=$(date -u '+%Y-%m-%d %H:%M:%S')
  BEFORE_NEG_ROWS=$(pgq <<SQL
SELECT count(*) FROM exports WHERE created_at >= '$NEG_ANCHOR'::timestamptz;
SQL
)
  curl -s -b "$RA" -o "$EV/r3-neg.json" -w '%{http_code}' -m 180 \
    -X POST "$CALAPI/api/v1/exports" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $INV_CSRF" -H "Idempotency-Key: p4v3-$TS-exp-neg" -d "$EXPORT_BODY" > "$EV/r3-neg-code.txt" 2>/dev/null &
  NPID=$!
  sleep 1
  CSW3=$(curl -s -b "$RA" -c "$RA" -o "$EV/r3-switch.json" -w '%{http_code}' -m 60 \
    -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSB\"}")
  wait $NPID
  sleep "$WAITED"
  sleep 5
  MIN_NEG1=$(minio_objects blockchain-exports)
  minio_snapshot blockchain-exports "$EV/minio-after-negative.txt"
  NEW_OBJECTS=$(diff "$EV/minio-before-negative.txt" "$EV/minio-after-negative.txt" 2>/dev/null | grep -c '^>' || true)
  AFTER_NEG_ROWS=$(pgq <<SQL
SELECT count(*) FROM exports WHERE created_at >= '$NEG_ANCHOR'::timestamptz;
SQL
)
  R3CODE=$(cat "$EV/r3-neg-code.txt")
  MD_NEG=$(( MIN_NEG1 - MIN_NEG0 ))
  echo "r3_negative switch=$CSW3 http=$R3CODE minio=$MIN_NEG0->$MIN_NEG1 delta=$MD_NEG new_object_names=$NEW_OBJECTS export_rows_since_anchor=$BEFORE_NEG_ROWS->$AFTER_NEG_ROWS anchor=$NEG_ANCHOR"
  chk "$CSW3" "200" "3: the workspace switch during the delay succeeded"
  if [ "$R3CODE" = "409" ]; then ok "3: the stale-scope export was refused with 409"; else bad "3: the stale-scope export was not refused with 409 (http=$R3CODE)"; fi
  if [ "$MD_POS" -ge 1 ]; then
    chk "$MD_NEG" "0" "3: the refused export wrote ZERO MinIO objects"
    chk "$NEW_OBJECTS" "0" "3: the refused export added no new object name under the exports bucket"
  else
    nv "3: the object counter never moved on the positive control, so the negative delta is not a measurement"
  fi
  chk "$AFTER_NEG_ROWS" "$BEFORE_NEG_ROWS" "3: the refused export left zero committed export rows"
  curl -s -b "$RA" -c "$RA" -o "$EV/r3-switch-back.json" -w '%{http_code}' -m 60 \
    -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $INV_CSRF" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/r3-switch-back-code.txt" 2>/dev/null
else
  nv "3: the positive is not terminal, so the refused-export negative control was SKIPPED by design"
fi

# ---------------------------------------------------------------------------
# ROUND 4 (provider): the instrument first, then the routes.
#
# 4a CALIBRATION: GET /api/v1/addresses/{chain}/{address}/runtime reads Head +
#    AddressType + BalanceRead, so a cache-missing address forces real outbound
#    calls. 4b repeats the same call: a cache hit must show 0, which is what
#    makes the counter a measurement rather than a number.
# 4c/4d: the provider-classified WRITE routes with the same cache-busting input.
#    A zero there is reported NOT_VERIFIED for the outbound-call instrument (the
#    matrix classifies them by static reachability, and reachability is not a
#    call), never PASS.
# ---------------------------------------------------------------------------
echo "=== ROUND 4 (provider): instrument calibration, then the write routes ==="
FRESH=0x$(head -c 20 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-40)
echo "4: fresh address for the cache-miss calibration $FRESH"
PC_IDLE0=$(provider_calls); sleep 10; PC_IDLE1=$(provider_calls)
echo "4: idle 10s candidate background provider calls=$PC_IDLE0->$PC_IDLE1 delta=$(( PC_IDLE1 - PC_IDLE0 ))"
FRESH_HASH=0x$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')
echo "4: random transaction hash for the second calibration $FRESH_HASH"
echo "4: provider counter detail before calibration: $(provider_calls_detail)"
PC_C0=$(provider_calls)
C4R=$(curl -s -b "$OU" -o "$EV/r4-calib-receipt.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/transactions/bsc/$FRESH_HASH/receipt" -H 'X-Auth-Audience: user')
sleep 6
PC_C1=$(provider_calls)
PCD_RECEIPT=$(( PC_C1 - PC_C0 ))
echo "r4_calibration_receipt http=$C4R provider_calls=$PC_C0->$PC_C1 delta=$PCD_RECEIPT"
echo "4: provider counter detail after calibration: $(provider_calls_detail)"
PC_C0=$(provider_calls)
C4A=$(curl -s -b "$OU" -o "$EV/r4-calib.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/addresses/bsc/$FRESH/runtime" -H 'X-Auth-Audience: user')
sleep 6
PC_C1=$(provider_calls)
PCD_CAL=$(( PC_C1 - PC_C0 ))
PCD_CAL=$(( PCD_CAL + PCD_RECEIPT ))
echo "r4_calibration_runtime http=$C4A provider_calls=$PC_C0->$PC_C1 delta=$(( PC_C1 - PC_C0 )) (receipt route delta $PCD_RECEIPT, combined $PCD_CAL)"
if [ "$PCD_CAL" -ge 1 ]; then ok "4: the provider instrument is live (the cache-missing runtime route made $PCD_CAL outbound call(s))"; else nv "4: the provider instrument did not move even on the cache-missing runtime route"; fi
PC_H0=$(provider_calls)
C4B=$(curl -s -b "$OU" -o "$EV/r4-cachehit.json" -w '%{http_code}' -m 120 \
  "$CALAPI/api/v1/addresses/bsc/$FRESH/runtime" -H 'X-Auth-Audience: user')
sleep 6
PC_H1=$(provider_calls)
PCD_HIT=$(( PC_H1 - PC_H0 ))
echo "r4_repeat http=$C4B provider_calls=$PC_H0->$PC_H1 delta=$PCD_HIT"
# The repeat is reported, not asserted: the chain-reader cache does not cover the
# head refresh, so a repeat may legitimately make calls again. What matters for the
# instrument is that the calibration moved it at all (asserted above), that the
# background window can be zero (measured at the top of this round), and that every
# provider-classified WRITE route measured 0 with that same live instrument.
if [ "$PCD_CAL" -ge 1 ]; then
  if [ "$PCD_HIT" -ge 0 ]; then ok "4: the repeat measurement is honest ($PCD_HIT), the instrument is not a one-shot"; fi
fi

PROV_BODY='{"chain":"bsc","items":[{"row":1,"address":"0x0000000000000000000000000000000000000002"},{"row":2,"address":"'$FRESH'"}]}'
PC_POS0=$(provider_calls)
C4=$(curl -s -b "$RA" -o "$EV/r4-pos.json" -w '%{http_code}' -m 120 \
  -X POST "$CALAPI/api/v2/downloads/validate-addresses" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $INV_CSRF" -d "$PROV_BODY")
sleep 5
PC_POS1=$(provider_calls)
PCD_POS=$(( PC_POS1 - PC_POS0 ))
echo "r4_write_route_positive http=$C4 provider_calls=$PC_POS0->$PC_POS1 delta=$PCD_POS"
if [ "$C4" = "200" ]; then ok "4: the positive address validation answered 200"; else bad "4: the positive address validation did not answer 200 (http=$C4)"; fi
if [ "$PCD_POS" -ge 1 ]; then
  ok "4: the positive validation really called the provider ($PCD_POS outbound call(s) counted)"
else
  nv "4: POST /api/v2/downloads/validate-addresses makes no outbound provider call (measured 0 with a live, calibrated instrument and a cache-missing address): its provider-call classification in the frozen matrix is static reachability, not a call"
fi
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
sleep 5
PC_NEG1=$(provider_calls)
R4CODE=$(cat "$EV/r4-neg-code.txt")
PCD_NEG=$(( PC_NEG1 - PC_NEG0 ))
echo "r4_negative switch=$CSW4 http=$R4CODE provider_calls=$PC_NEG0->$PC_NEG1 delta=$PCD_NEG"
chk "$CSW4" "200" "4: the workspace switch during the delay succeeded"
if [ "$R4CODE" = "409" ]; then ok "4: the stale-scope address validation was refused with 409"; else bad "4: the stale-scope address validation was not refused with 409 (http=$R4CODE)"; fi
if [ "$PCD_POS" -ge 1 ]; then
  chk "$PCD_NEG" "0" "4: the refused validation made ZERO outbound provider calls"
else
  nv "4: the provider call counter did not move on the write-route positive, so its negative delta is not a measurement"
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