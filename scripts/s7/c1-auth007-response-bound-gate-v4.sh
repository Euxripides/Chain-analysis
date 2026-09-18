#!/usr/bin/env bash
# c1-auth007-response-bound-gate-v2.sh
#
# P1-2 negative calibration v2 for the commit-gate response bound.
#
# WHY v2 EXISTS (the v1 defect this script repairs, proven from raw evidence)
# ---------------------------------------------------------------------------
# scripts/s7/c1-auth007-response-bound-gate-v1.sh (frozen, sha fb3776df...) ran the
# positive control and the negative control with the SAME payload, i.e. the same
# canvas "name", against the SAME parent case. investigation_canvases carries
#
#     investigation_canvases_case_id_name_key :: UNIQUE (case_id, name)
#
# so the negative control INSERT raised a unique violation. That SQL error aborted
# the request transaction, every later statement of that transaction then failed
# with SQLSTATE 25P02, and the gate release re-read therefore reported
# reason=session_scope_unverifiable instead of comparing the scope token. The
# resulting 409 was a coincidence of an unrelated constraint, not a scope
# decision. Both candidates were measured through that same accident, so the v1
# run proved nothing about the response bound. The deny audit rows written by the
# v1 rounds carry the proof:
#
#     detail.gate_error = "ERROR: current transaction is aborted, commands ignored
#                          until end of transaction block (SQLSTATE 25P02)"
#
# v2 repairs exactly what that accident required:
#   * the parent case lives in the TARGET workspace B,
#   * the caller session starts in workspace A and is switched A -> B while the
#     calibration delay is running, so the handler passes its object check against
#     the live scope and really produces a > 256 KiB body,
#   * every round uses a DIFFERENT canvas name, so no round can be decided by the
#     unique index.
#
# EXPECT=old  the frozen pre-fix candidate (backend/bin.new/api-calib-auth007) must
#             EXPOSE the defect: a 2xx status and more than 256 KiB handed to the
#             caller for a write the database rolled back, plus the pre-fix
#             writer's own "exceeded the retractable buffer" marker server side.
# EXPECT=new  the fixed candidate (backend/bin.new/api-calib-auth007-s3) must give
#             ONE consistent result: 409, a small body, zero business rows, exactly
#             one deny row, zero success audit rows, zero bytes the caller cannot
#             un-see and no spool file left behind.
#
# ASCII only. No credential, cookie, csrf token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
BIN="${BIN:?set BIN}"
EXPECT="${EXPECT:-new}"
LABEL="${LABEL:-respbound2-$EXPECT}"
PORT="${PORT:-18153}"
PORT_HARD="${PORT_HARD:-18154}"
HANDLER_MS="${HANDLER_MS:-8000}"
SWITCH_AT_MS="${SWITCH_AT_MS:-1000}"
THRESHOLD_MS="${THRESHOLD_MS:-5000}"
BOUND_BYTES="${BOUND_BYTES:-262144}"
HARD_LOW_BYTES="${HARD_LOW_BYTES:-262144}"
HARD_ROUND="${HARD_ROUND:-1}"
HARD_GROUPS="${HARD_GROUPS:-10000}"
HARD_PAD="${HARD_PAD:-900}"
READY_TRIES="${READY_TRIES:-180}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
CALAPI="http://127.0.0.1:$PORT"
HARDAPI="http://127.0.0.1:$PORT_HARD"
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/gate-console.log") 2>&1

PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got=$1 want=$2)"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1" 2>/dev/null || echo "__ERR__"; }
csrf_of(){ awk -v c="$2" '$6==c{print $7}' "$1" | tail -1; }
sess_id(){ curl -s -b "$1" -H "X-Auth-Audience: $2" "$API/api/auth/v1/session" | jget "['data']['session_id']"; }
now_ms(){ date +%s%3N; }
spool_count(){ ls -1 /tmp/scope-commit-gate-*.spool 2>/dev/null | wc -l; }
marker_count(){ local n; n=$(grep -ac "$2" "$EV/$1" 2>/dev/null); if [ -z "$n" ]; then n=0; fi; echo "$n"; }
onestep(){ awk -v a="$1" -v b="$2" 'BEGIN{ if (b - a < 0.05) exit 0; exit 1 }'; }
# A Prometheus counter is deterministic evidence that a code path ran; a 250 ms
# poll for a file that lives for the duration of one 65 KiB write is not.
metric_val(){ curl -s "$1/metrics" 2>/dev/null | awk -v m="$2" '$1 ~ ("^" m) { s += $2 } END { printf "%d", s + 0 }'; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL expect=$EXPECT port=$PORT port_hard=$PORT_HARD handler_ms=$HANDLER_MS switch_at_ms=$SWITCH_AT_MS"
echo "bound_bytes=$BOUND_BYTES hard_low_bytes=$HARD_LOW_BYTES hard_round=$HARD_ROUND hard_groups=$HARD_GROUPS hard_pad=$HARD_PAD"
echo "bin=$BIN bin_sha256=$(sha256sum "$BIN" | cut -d\  -f1)"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
DISK0=$(df --output=avail -k / | tail -1 | tr -d ' ')
echo "disk_avail_kb_before=$DISK0"

echo "=== fixtures (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "C1 commit gate response bound acceptance" \
    -workspaces c1lat-a,c1lat-b \
    -identities security_admin:c1lat-sec@fixture.local:c1lat-a,investigator:c1lat-inv@fixture.local:c1lat-a+c1lat-b \
    -out "$CRED" ) > "$EV/fixture.log" 2>&1
chmod 600 "$CRED"
read_cred(){ grep -E "^$2=" "$1" 2>/dev/null | head -1 | cut -d= -f2-; }
WSA=$(read_cred "$CRED" C1MEM_WS_C1LAT_A)
WSB=$(read_cred "$CRED" C1MEM_WS_C1LAT_B)
INVEMAIL=$(read_cred "$CRED" C1MEM_C1LAT_INV_EMAIL)
INVPW=$(read_cred "$CRED" C1MEM_C1LAT_INV_PW)
INVPW1="Pw-${TS}-c1aeres-91d7"
jar="$EV/clear-inv.jar"
c=$(curl -s -c "$jar" -o "$EV/clear-inv.json" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW\",\"audience\":\"user\",\"workspace_id\":\"$WSA\"}")
chk "$c" "200" "investigator fixture logs in with its initial password"
csrf=$(csrf_of "$jar" cs_user_session_csrf)
c=$(curl -s -b "$jar" -o "$EV/clear-inv-pw.json" -w '%{http_code}' -X POST "$API/api/auth/v1/password/change" \
  -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $csrf" \
  -d "{\"current_password\":\"$INVPW\",\"new_password\":\"$INVPW1\"}")
if [ "$c" = "200" ] || [ "$c" = "204" ]; then ok "investigator initial-password flag cleared"; else bad "investigator initial-password flag cleared (status=$c)"; fi
rm -f "$jar" "$EV"/clear-inv*.json

# Two sessions of the SAME identity, bound to the SAME two workspaces:
#   RA starts in workspace A  -- the caller of every scope-race round
#   RB lives in workspace B   -- owns the parent case and the clean positive control
login_ws(){
  local jar=$1 ws=$2 out=$3
  rm -f "$jar"
  curl -s -c "$jar" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -d "{\"identifier\":\"$INVEMAIL\",\"password\":\"$INVPW1\",\"audience\":\"user\",\"workspace_id\":\"$ws\"}"
}
RA="$EV/ra.jar"; RB="$EV/rb.jar"
chk "$(login_ws "$RA" "$WSA" "$EV/ra-login.json")" "200" "caller session RA logs in to workspace A"
chk "$(login_ws "$RB" "$WSB" "$EV/rb-login.json")" "200" "owner session RB logs in to workspace B"
SID_A=$(sess_id "$RA" user)
SID_B=$(sess_id "$RB" user)
if [ -z "$SID_A" ] || [ -z "$SID_B" ] || [ "$SID_A" = "__ERR__" ] || [ "$SID_B" = "__ERR__" ]; then bad "session ids not resolvable"; exit 0; fi
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf)
CSRF_B=$(csrf_of "$RB" cs_user_session_csrf)
chk "$(pgq <<SQL
SELECT workspace_id::text FROM sessions WHERE id='$SID_A'::uuid;
SQL
)" "$WSA" "RA really is scoped to workspace A in the database"
chk "$(pgq <<SQL
SELECT workspace_id::text FROM sessions WHERE id='$SID_B'::uuid;
SQL
)" "$WSB" "RB really is scoped to workspace B in the database"

echo "=== payloads (one DISTINCT canvas name per round: the v1 unique-index trap) ==="
gen_payload(){
  python3 - "$1" "$2" "$3" "$4" "$5" <<'PY'
import sys
path, name, groups, padlen = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
ch = sys.argv[5] if len(sys.argv) > 5 else "x"
with open(path, "w") as f:
    f.write('{"name":"%s","groups":[' % name)
    for i in range(groups):
        if i:
            f.write(",")
        f.write('{"index":%d,"pad":"%s"}' % (i, ch * padlen))
    f.write("]}")
PY
}
gen_payload "$EV/payload-pos.json" "A7POS-$TS" 320 980 x
gen_payload "$EV/payload-neg.json" "A7NEG-$TS" 320 980 x
gen_payload "$EV/payload-dis.json" "A7DIS-$TS" 320 980 x
gen_payload "$EV/payload-hold.json" "A7HOLD-$TS" 320 980 x
POS_BYTES=$(wc -c < "$EV/payload-pos.json")
NEG_BYTES=$(wc -c < "$EV/payload-neg.json")
echo "payload_pos_bytes=$POS_BYTES payload_neg_bytes=$NEG_BYTES"
if [ "$POS_BYTES" -gt "$BOUND_BYTES" ]; then ok "the calibration payload is larger than the retractable bound"; else bad "the calibration payload must exceed the bound (got $POS_BYTES)"; fi

calib_pid=""; hard_pid=""
cleanup(){
  local v p
  for v in calib_pid hard_pid; do
    eval "p=\$$v"
    if [ -n "$p" ] && kill -0 "$p" 2>/dev/null; then
      kill -TERM "$p" 2>/dev/null || true
      for _ in $(seq 1 20); do kill -0 "$p" 2>/dev/null || break; sleep 0.5; done
      kill -KILL "$p" 2>/dev/null || true
      sleep 1
    fi
    eval "$v=''"
  done
}
trap cleanup EXIT

start_calib(){
  local var=$1 port=$2 extra=$3 logname=$4 p ready rc
  chk "$(ss -ltn 2>/dev/null | grep -c ":$port ")" "0" "port $port is free before start"
  ( cd "$R/backend"
    set -a; . "$R/deploy/.env"; set +a
    export API_HTTP_PORT="$port"
    export SCOPE_CALIB_ENABLE=1
    export SCOPE_CALIB_DELAY_MS="$HANDLER_MS"
    export SCOPE_CALIB_PHASE=post
    unset SCOPE_CALIB_ROUTE
    export SCOPE_COMMIT_GATE_SPOOL_DIR=/tmp
    if [ "$extra" != "-" ]; then . "$extra"; fi
    nohup setsid "$BIN" >> "$EV/$logname" 2>&1 &
    echo $! > "$EV/$logname.pid" )
  sleep 1
  eval "$var=$(cat "$EV/$logname.pid")"
  eval "p=\$$var"
  ready=0; rc=""
  for _ in $(seq 1 "$READY_TRIES"); do
    rc=$(curl -s -o "$EV/$logname-ready.json" -w '%{http_code}' "http://127.0.0.1:$port/health/ready" 2>/dev/null)
    if [ "$rc" = "200" ]; then ready=1; break; fi
    sleep 0.5
  done
  chk "$ready" "1" "calibration instance $logname ready on $port (http $rc)"
  chk "$(sha256sum /proc/$p/exe 2>/dev/null | cut -d\  -f1)" "$(sha256sum "$BIN" | cut -d\  -f1)" "running calibration process $logname is the binary under test"
}

stop_calib(){
  local var=$1 p
  eval "p=\$$var"
  if [ -n "$p" ] && kill -0 "$p" 2>/dev/null; then
    kill -TERM "$p" 2>/dev/null || true
    for _ in $(seq 1 20); do kill -0 "$p" 2>/dev/null || break; sleep 0.5; done
    kill -KILL "$p" 2>/dev/null || true
  fi
  eval "$var=''"
  sleep 1
}

# hold_release runs one external transaction that takes FOR UPDATE on the row the
# gate release re-reads, and keeps it until pg_sleep returns. The multi-statement
# -c string is one simple query, so every statement shares one transaction and the
# lock really is held for the whole sleep.
hold_release(){
  local log="$1" sid="$2" ms="$3"
  ( docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1 \
      -c "BEGIN; SELECT 1 FROM sessions WHERE id='$sid'::uuid FOR UPDATE; SELECT pg_sleep($(( ms / 1000 ))); ROLLBACK;" ) > "$log" 2>&1 &
  echo $!
}

start_calib calib_pid "$PORT" - calib.log
MAIN_RSS0=$(ps -o rss= -p "$calib_pid" | tr -d ' ')
echo "calib_rss_kb_before=$MAIN_RSS0"
chk "$(spool_count)" "0" "no response spool file exists before the gate"

echo "=== the parent case, created by RB INSIDE workspace B (v2 requirement) ==="
CNAME="A7CASE-$TS"
CCODE=$(curl -s -b "$RB" -o "$EV/case.json" -w '%{http_code}' -m 90 \
  -X POST "$CALAPI/api/v2/cases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_B" -d "{\"case_no\":\"$CNAME\",\"name\":\"$CNAME\"}")
chk "$CCODE" "201" "the parent case was created in workspace B (delayed gated write, committed)"
CASEID=$(jget < "$EV/case.json" "['data']['id']")
echo "case_id=$CASEID"
if [ -z "$CASEID" ] || [ "$CASEID" = "__ERR__" ]; then bad "case id not resolvable"; exit 0; fi
chk "$(pgq <<SQL
SELECT workspace_id::text FROM cases WHERE id='$CASEID'::uuid;
SQL
)" "$WSB" "the parent case really lives in the target workspace B"

canvas_rows(){ pgq <<SQL
SELECT count(*) FROM investigation_canvases WHERE case_id='$CASEID'::uuid AND name='$1';
SQL
}
success_audit(){ pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='canvas.create' AND result='success' AND session_id='$1'::uuid
  AND occurred_at > now() - interval '5 minutes';
SQL
}

echo "=== round 1: positive control from RB (session in B, case in B, NO scope change) ==="
PCODE=$(curl -s -b "$RB" -o "$EV/pos-body.bin" -w '%{http_code} %{size_download} %{time_starttransfer} %{time_total}' -m 180 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/payload-pos.json")
PSIZE=$(wc -c < "$EV/pos-body.bin")
PH=$(printf '%s' "$PCODE" | awk '{print $1}')
PTT=$(printf '%s' "$PCODE" | awk '{print $3}')
PTOT=$(printf '%s' "$PCODE" | awk '{print $4}')
echo "positive_control http=$PH downloaded_bytes=$PSIZE starttransfer=$PTT total=$PTOT"
if [ "$PH" = "201" ] || [ "$PH" = "200" ]; then ok "positive control committed ($PH)"; else bad "positive control must commit (got $PH)"; fi
if [ "$PSIZE" -gt "$BOUND_BYTES" ]; then ok "positive control delivered more than the retractable bound ($PSIZE > $BOUND_BYTES)"; else bad "positive control must deliver more than the bound (got $PSIZE)"; fi
chk "$(canvas_rows "A7POS-$TS")" "1" "positive control committed exactly one canvas row"
chk "$(spool_count)" "0" "no spool file survives the committed positive control"

echo "=== round 2: the same handler with a workspace switch inside the calibration delay ==="
BEFORE_NEG=$(canvas_rows "A7NEG-$TS")
BEFORE_DENY=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/v1/cases/%/canvases'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
SEQ0=$(pgq <<SQL
SELECT coalesce(max(seq),0) FROM audit_events_v2;
SQL
)
SPOOLMET0=$(metric_val "$CALAPI" cs_scope_commit_gate_spooled_total)
TOOBIGMET0=$(metric_val "$CALAPI" cs_scope_commit_gate_response_too_large_total)
echo "spool_metric_before=$SPOOLMET0 too_large_metric_before=$TOOBIGMET0"
# The spool file exists only from the tail write to the commit decision, so the
# sampler has to be able to observe a window that lives for single digit
# milliseconds. v2 sampled it with a bash glob guarded by "test -e" and then
# forked /usr/bin/stat; the fork lost the race against the unlink that happens
# microseconds later, the sample file ended up holding nothing but a stat error,
# and v2 then asserted 0600 and zero client bytes against that empty evidence.
# Both of those v2 FAILs are measurement defects. This sampler stats the path
# directly (no prior existence test), keeps probing after a failed stat instead
# of burning its single chance, records mode and size from the stat that
# actually succeeded, treats a client body file that does not exist yet as the
# zero bytes it really is, and records EVERY observation.
#
# v4 additionally stops depending on that window: round 2b blocks the release
# re-read on an external row lock, which keeps the spool file on disk for
# seconds instead of microseconds, and the 0600 / zero-byte assertions run only
# there. The measured reason is in the v3 round: even a microsecond-granularity
# Python sampler saw the file exactly once and lost the stat to the unlink.
SPOOLDIR="${SCOPE_COMMIT_GATE_SPOOL_DIR:-/tmp}"
SPOOL_WINDOW_S="${SPOOL_WINDOW_S:-25}"
SPOOL_MAX_SAMPLES="${SPOOL_MAX_SAMPLES:-400}"
# sample_spool <evidence dir> <spool dir> <window s> <max samples> <client body file> <sample file name>
sample_spool(){
  python3 - "$1" "$2" "$3" "$4" "$5" "$6" > /dev/null 2>&1 <<'PY'
import os, sys, time
ev, spool_dir, client = sys.argv[1], sys.argv[2], sys.argv[5]
window, max_samples = float(sys.argv[3]), int(sys.argv[4])
out = os.path.join(ev, sys.argv[6])
prefix, suffix = "scope-commit-gate-", ".spool"
deadline = time.time() + window
probes = 0
rows = []
while time.time() < deadline and len(rows) < max_samples * 4:
    try:
        names = os.listdir(spool_dir)
    except OSError:
        names = []
    for name in names:
        if not (name.startswith(prefix) and name.endswith(suffix)):
            continue
        probes += 1
        path = os.path.join(spool_dir, name)
        try:
            st = os.stat(path)
        except OSError:
            continue
        try:
            cb = os.stat(client).st_size
            present = 1
        except OSError:
            cb = 0
            present = 0
        try:
            dmode = "%o" % (os.stat(spool_dir).st_mode & 0o7777)
        except OSError:
            dmode = "?"
        rows.append("spool_path=%s mode=%s size=%d" % (path, "%o" % (st.st_mode & 0o7777), st.st_size))
        rows.append("spool_dir_mode=%s spool_dir=%s" % (dmode, spool_dir))
        rows.append("client_bytes_at_spool_sample=%d client_body_present=%d" % (cb, present))
        rows.append("spool_sample_index=%d spool_probe_index=%d" % (len(rows) // 4 + 1, probes))
        if len(rows) >= max_samples * 4:
            break
with open(out, "a") as fh:
    if rows:
        fh.write("\n".join(rows) + "\n")
    fh.write("spool_probes=%d\n" % probes)
    fh.write("spool_observations=%d\n" % (len(rows) // 4))
    fh.write("spool_window_s=%s\n" % window)
    if rows:
        fh.write("spool_all_mode_600=%d\n" % (1 if all(" mode=600 " in r for r in rows[0::4]) else 0))
        fh.write("spool_all_client_zero=%d\n" % (1 if all("client_bytes_at_spool_sample=0 " in r for r in rows[2::4]) else 0))
        fh.write("spool_all_client_absent=%d\n" % (1 if all("client_body_present=0" in r for r in rows[2::4]) else 0))
    else:
        fh.write("spool_not_observed_in_window=1\n")
PY
}
sample_spool "$EV" "$SPOOLDIR" "$SPOOL_WINDOW_S" "$SPOOL_MAX_SAMPLES" "$EV/neg-body.bin" spool-sample.txt &
SPOOLW=$!
curl -s -b "$RA" -o "$EV/neg-body.bin" -w '%{http_code} %{size_download} %{time_starttransfer} %{time_total}' -m 180 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" --data-binary @"$EV/payload-neg.json" > "$EV/neg-code.txt" 2>/dev/null &
NPID=$!
sleep 1
M0=$(now_ms)
SWRES=$(curl -s -m 120 -b "$RA" -c "$RA" -o "$EV/switch.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_A" -d "{\"workspace_id\":\"$WSB\"}")
M1=$(now_ms); SWMS=$(( M1 - M0 ))
wait $NPID
wait $SPOOLW 2>/dev/null
NC=$(cat "$EV/neg-code.txt" 2>/dev/null || echo "n/a 0 0 0")
NCODE=$(printf '%s' "$NC" | awk '{print $1}')
NSIZE=$(wc -c < "$EV/neg-body.bin")
NTT=$(printf '%s' "$NC" | awk '{print $3}')
NTOT=$(printf '%s' "$NC" | awk '{print $4}')
AFTER_NEG=$(canvas_rows "A7NEG-$TS")
AFTER_DENY=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2
WHERE action='authorization.denied' AND route_template LIKE '/api/v1/cases/%/canvases'
  AND error_code='AUTHZ_WORKSPACE_SCOPE_STALE';
SQL
)
SUCCESS_AUDIT=$(success_audit "$SID_A")
SPOOL_LEFT=$(spool_count)
MAIN_RSS1=$(ps -o rss= -p "$calib_pid" 2>/dev/null | tr -d ' ')
NERR=$(jget < "$EV/neg-body.bin" "['error']['code']" 2>/dev/null || echo "-")
NEW_DENY_DETAIL=$(pgq <<SQL
SELECT coalesce(string_agg(coalesce(detail::text,'-'),' ~ '),'-') FROM audit_events_v2
WHERE seq > $SEQ0 AND action='authorization.denied'
  AND route_template LIKE '/api/v1/cases/%/canvases';
SQL
)
echo "negative_control http=$NCODE body_bytes=$NSIZE starttransfer=$NTT total=$NTOT case_error=$NERR switch=$SWRES switch_ms=$SWMS"
echo "canvas_rows_neg before=$BEFORE_NEG after=$AFTER_NEG deny_rows_delta=$(( AFTER_DENY - BEFORE_DENY )) success_audit=$SUCCESS_AUDIT"
echo "spool_files_left=$SPOOL_LEFT calib_rss_kb_before=$MAIN_RSS0 calib_rss_kb_after=$MAIN_RSS1"
echo "neg_deny_detail=$NEW_DENY_DETAIL"
chk "$SWRES" "200" "the workspace switch itself succeeded"
if [ "$SWMS" -le "$THRESHOLD_MS" ]; then ok "the switch stayed within AUTH-007 ($SWMS ms <= $THRESHOLD_MS ms)"; else bad "the switch took $SWMS ms"; fi
chk "$(pgq <<SQL
SELECT workspace_id::text FROM sessions WHERE id='$SID_A'::uuid;
SQL
)" "$WSB" "the caller session really moved to workspace B while the handler was running"
chk "$AFTER_NEG" "$BEFORE_NEG" "the rolled-back canvas did not commit a business row"
chk "$SUCCESS_AUDIT" "0" "no success audit row exists for the rolled-back canvas"
chk "$SPOOL_LEFT" "0" "no response spool file is left behind"
SPOOLMET1=$(metric_val "$CALAPI" cs_scope_commit_gate_spooled_total)
SPOOL_SPILLS=$(( SPOOLMET1 - SPOOLMET0 ))
echo "spool_metric_after=$SPOOLMET1 spool_spills_during_round_2=$SPOOL_SPILLS"
if [ "$EXPECT" = "new" ]; then
  if [ "$SPOOL_SPILLS" -ge 1 ]; then
    ok "the retractable response buffer really spilled to disk during the negative control (counter +$SPOOL_SPILLS)"
  else
    bad "the spill counter did not move although the response exceeds the in-memory bound"
  fi
  # Round 2 is the negative control for the COMMIT DECISION, not for the spool
  # file: it rolls the transaction back microseconds after the tail write, so a
  # sample here is recorded and never asserted. Round 2b below blocks the same
  # release on an external row lock and carries the 0600 / zero-byte assertions
  # on a window that is seconds wide.
  if [ -s "$EV/spool-sample.txt" ] && ! grep -q 'spool_not_observed_in_window' "$EV/spool-sample.txt"; then
    cat "$EV/spool-sample.txt"
    echo "ROUND2_SAMPLE=observed observations=$(awk -F= '/^spool_observations=/{print $2}' "$EV/spool-sample.txt")"
  else
    echo "ROUND2_SAMPLE=not_observed the file lives only between the tail write and the rollback; round 2b measures it deterministically"
  fi
else
  # The pre-fix writer has no spool at all: past 256 KiB it writes straight to
  # the client. Absence of a spool file is the pre-fix behaviour, not a defect of
  # this measurement, so it is recorded instead of asserted.
  if [ "$SPOOL_SPILLS" -eq 0 ]; then
    ok "PRE-FIX: the frozen candidate never spills a response to disk (no spool path exists)"
  else
    bad "PRE-FIX: an unexpected spill appeared in the frozen candidate"
  fi
fi
if onestep "$NTT" "$NTOT"; then ok "the whole refusal arrived in one transfer: nothing was sent before the commit decision"; else bad "the refusal arrived in more than one transfer (starttransfer=$NTT total=$NTOT)"; fi

if [ "$EXPECT" = "new" ]; then
echo "=== round 2b: the RELEASE re-read blocked on an external row lock (deterministic spool window) ==="
# Round 2 proves the negative control of the commit decision. This round proves
# the positive control of the RETRACTABLE BUFFER with a window that is seconds
# wide instead of microseconds: an external transaction holds FOR UPDATE on the
# caller session row, so the handler still writes its > 256 KiB body (the buffer
# really spills to disk) but the commit decision cannot be taken until that
# transaction ends. While it is blocked the spool file is on disk and the caller
# has received nothing, which is exactly the invariant the buffer exists for.
BEFORE_HOLD=$(canvas_rows "A7HOLD-$TS")
HOLD_MS=$(( HANDLER_MS + 6000 ))
# Warm-up first: a session row is touched at most once per 60 s, and that touch
# is an UPDATE on the very row this round locks. If a pending touch were queued
# behind the lock it would delay the whole request instead of only its release,
# and the deterministic window would collapse back to microseconds.
curl -s -b "$RB" -o "$EV/hold-warmup.json" -w '%{http_code}' -m 30 \
  -H 'X-Auth-Audience: user' "$CALAPI/api/auth/v1/session" > "$EV/hold-warmup-code.txt" 2>/dev/null
chk "$(cat "$EV/hold-warmup-code.txt")" "200" "2b: the owner session is warm before the caller row lock is taken"
HOLD_PID=$(hold_release "$EV/hold-release.log" "$SID_B" "$HOLD_MS")
sleep 1
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE state='active' AND query LIKE '%$SID_B%' AND query LIKE '%FOR UPDATE%';
SQL
)" "1" "2b: the external transaction really holds the caller session row lock"
rm -f "$EV/hold-sample.txt"
sample_spool "$EV" "$SPOOLDIR" "$SPOOL_WINDOW_S" "$SPOOL_MAX_SAMPLES" "$EV/hold-body.bin" hold-sample.txt &
SPOOLW2=$!
HCODE=$(curl -s -b "$RB" -o "$EV/hold-body.bin" -w '%{http_code} %{size_download} %{time_starttransfer} %{time_total}' -m 240 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/payload-hold.json")
wait "$SPOOLW2" 2>/dev/null
wait "$HOLD_PID" 2>/dev/null
HH=$(printf '%s' "$HCODE" | awk '{print $1}')
HSZ=$(printf '%s' "$HCODE" | awk '{print $2}')
HTT=$(printf '%s' "$HCODE" | awk '{print $3}')
HTOT=$(printf '%s' "$HCODE" | awk '{print $4}')
AFTER_HOLD=$(canvas_rows "A7HOLD-$TS")
echo "hold_round http=$HH downloaded_bytes=$HSZ starttransfer=$HTT total=$HTOT rows_before=$BEFORE_HOLD rows_after=$AFTER_HOLD hold_ms=$HOLD_MS"
cat "$EV/hold-sample.txt"
if [ "$HH" = "201" ] || [ "$HH" = "200" ]; then ok "2b: the held round still committed ($HH)"; else bad "2b: the held round must commit (got $HH)"; fi
if [ "$HSZ" -gt "$BOUND_BYTES" ]; then ok "2b: the committed response is larger than the retractable bound ($HSZ bytes)"; else bad "2b: expected a > $BOUND_BYTES byte response (got $HSZ)"; fi
chk "$AFTER_HOLD" "$(( BEFORE_HOLD + 1 ))" "2b: the held round committed exactly one canvas row"
if [ -s "$EV/hold-sample.txt" ] && ! grep -q 'spool_not_observed_in_window' "$EV/hold-sample.txt"; then
  ok "2b: the spilled response file was observed on disk while the commit decision was blocked"
  chk "$(awk -F= '/^spool_all_mode_600=/{print $2}' "$EV/hold-sample.txt")" "1" "2b: every observation of the spool file shows mode 0600"
  chk "$(awk -F= '/^spool_all_client_zero=/{print $2}' "$EV/hold-sample.txt")" "1" "2b: every observation shows the caller still holding zero bytes"
  chk "$(awk -F= '/^spool_all_client_absent=/{print $2}' "$EV/hold-sample.txt")" "1" "2b: the caller body file did not exist at any observation (not one byte was forwarded)"
  OBS=$(awk -F= '/^spool_observations=/{print $2}' "$EV/hold-sample.txt")
  if [ "$OBS" -ge 2 ]; then ok "2b: the window was wide enough for repeated observation ($OBS samples)"; else bad "2b: only $OBS observation(s) of the spool file"; fi
  chk "$(grep -c 'spool_sample_index=' "$EV/hold-sample.txt")" "$OBS" "2b: every observation is accounted for by the observation counter"
else
  bad "2b: the spool file was not observed even with the release blocked for $HOLD_MS ms"
fi
chk "$(spool_count)" "0" "2b: no spool file survives the held round"
if awk -v a="$HTT" -v b="$HOLD_MS" 'BEGIN{ exit !(a * 1000 + 500 >= b) }'; then
  ok "2b: the first response byte reached the caller only after the release lock ended ($HTT s vs a $HOLD_MS ms hold)"
else
  bad "2b: bytes reached the caller while the commit decision was still blocked (starttransfer=$HTT s)"
fi
fi

if [ "$EXPECT" = "old" ]; then
  echo "=== negative calibration of the PRE-FIX candidate: the inconsistency must be visible ==="
  case "$NCODE" in 2*) ok "PRE-FIX: the caller was handed a success status ($NCODE) for a write that was rolled back";; *) bad "PRE-FIX: expected a success status to be exposed, got $NCODE";; esac
  if [ "$NSIZE" -gt "$BOUND_BYTES" ]; then ok "PRE-FIX: the caller received $NSIZE bytes it cannot un-see"; else bad "PRE-FIX: expected more than $BOUND_BYTES bytes on the wire, got $NSIZE"; fi
  MU=$(marker_count calib.log "exceeded the retractable buffer")
  if [ "$MU" -ge 1 ]; then ok "PRE-FIX: the server recorded that the response had already exceeded the retractable buffer"; else bad "PRE-FIX: the pre-fix writer marker is absent, so the overflow branch was never reached"; fi
  case "$NEW_DENY_DETAIL" in *response_unretractable*) ok "PRE-FIX: the deny audit admits response_unretractable";; *) bad "PRE-FIX: the deny audit must admit response_unretractable";; esac
  echo "OLD_INCONSISTENT=1 old_http=$NCODE old_bytes=$NSIZE old_rows_before=$BEFORE_NEG old_rows_after=$AFTER_NEG old_unretractable_markers=$MU"
else
  echo "=== the FIXED candidate must produce one consistent result ==="
  chk "$NCODE" "409" "the gated write was refused with 409, not committed under the stale scope"
  chk "$NERR" "AUTHZ_WORKSPACE_SCOPE_STALE" "the refusal carries the stable scope code"
  if [ "$NSIZE" -lt "$BOUND_BYTES" ]; then ok "the refusal body stayed far below the retractable bound ($NSIZE bytes)"; else bad "the refusal body is $NSIZE bytes"; fi
  chk "$(( AFTER_DENY - BEFORE_DENY ))" "1" "exactly one detached deny audit row (exactly-once)"
  echo "NEW_CONSISTENT=1 new_http=$NCODE new_bytes=$NSIZE new_error=$NERR"
fi

echo "=== round 3: client disconnect inside the delay (the transaction must not survive) ==="
BEFORE_DIS=$(canvas_rows "A7DIS-$TS")
curl -s -b "$RA" -o "$EV/dis-body.bin" -w '%{http_code}' -m 180 \
  -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_A" --data-binary @"$EV/payload-dis.json" > "$EV/dis-code.txt" 2>/dev/null &
DPID=$!
sleep 3
kill -9 "$DPID" 2>/dev/null || true
wait "$DPID" 2>/dev/null
sleep 6
chk "$(canvas_rows "A7DIS-$TS")" "$BEFORE_DIS" "a handler abandoned by its client committed no business row"
chk "$(spool_count)" "0" "a handler abandoned by its client left no spool file"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
)" "0" "no commit-gate backend is left behind by the abandoned request"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "calibration instance is still ready after the disconnect"

if [ "$HARD_ROUND" = "1" ]; then
  echo "=== round 4: the DEFAULT 32 MiB hard bound, driven by a $HARD_GROUPS group payload ==="
  gen_payload "$EV/payload-hard.json" "A7HARD-$TS" "$HARD_GROUPS" "$HARD_PAD" '<'
  HARDBYTES=$(wc -c < "$EV/payload-hard.json")
  echo "hard_payload_bytes=$HARDBYTES decode_limit_bytes=10485760"
  if [ "$HARDBYTES" -lt 10485760 ]; then ok "the hard-bound payload fits inside the 10 MiB request decode limit"; else bad "the hard-bound payload exceeds the 10 MiB decode limit (got $HARDBYTES)"; fi
  BEFORE_HARD=$(canvas_rows "A7HARD-$TS")
  TOOBIG0=$(metric_val "$CALAPI" cs_scope_commit_gate_response_too_large_total)
  HCODE=$(curl -s -b "$RB" -o "$EV/hard-body.bin" -w '%{http_code} %{size_download}' -m 300 \
    -X POST "$CALAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
    -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/payload-hard.json")
  HH=$(printf '%s' "$HCODE" | awk '{print $1}')
  HSIZE=$(wc -c < "$EV/hard-body.bin")
  HERR=$(jget < "$EV/hard-body.bin" "['error']['code']" 2>/dev/null || echo "-")
  AFTER_HARD=$(canvas_rows "A7HARD-$TS")
  HMARK=$(marker_count calib.log "exceeded the hard bound")
  HBIG=$(marker_count calib.log "exceeded the retractable buffer")
  TOOBIG1=$(metric_val "$CALAPI" cs_scope_commit_gate_response_too_large_total)
  echo "too_large_metric before=$TOOBIG0 after=$TOOBIG1"
  echo "hard_round http=$HH body_bytes=$HSIZE error=$HERR rows_before=$BEFORE_HARD rows_after=$AFTER_HARD cancel_markers=$HMARK unretractable_markers=$HBIG"
  if [ "$EXPECT" = "new" ]; then
    chk "$HH" "503" "a response past the 32 MiB hard bound fails closed with a stable 503"
    chk "$HERR" "INTERNAL_RESPONSE_TOO_LARGE" "the stable hard-bound error code is returned"
    chk "$AFTER_HARD" "$BEFORE_HARD" "the cancelled handler committed no business row"
    chk "$(spool_count)" "0" "the cancelled handler left no spool file"
    if [ "$HMARK" -ge 1 ]; then ok "the server recorded the cancelling hard bound"; else bad "the hard-bound cancel marker is absent"; fi
    if [ $(( TOOBIG1 - TOOBIG0 )) -ge 1 ]; then ok "the hard-bound refusal counter moved (deterministic proof the cancel path ran)"; else bad "the hard-bound refusal counter did not move"; fi
  else
    # The frozen pre-fix candidate has no hard bound at all: the only ceiling it
    # knows is the 256 KiB retractable buffer, past which it streams and keeps
    # whatever the handler produced. Recording that is the negative control.
    if [ "$HSIZE" -gt 33554432 ]; then ok "PRE-FIX: a $HSIZE byte response past 32 MiB was delivered instead of being refused"; else bad "PRE-FIX: expected the unbounded freeze to deliver more than 32 MiB (got $HSIZE)"; fi
    if [ "$HMARK" -eq 0 ]; then ok "PRE-FIX: no hard-bound cancel exists in the frozen candidate"; else bad "PRE-FIX: an unexpected hard-bound cancel appeared"; fi
    echo "OLD_NO_HARD_BOUND=1 old_hard_http=$HH old_hard_bytes=$HSIZE old_hard_rows_after=$AFTER_HARD"
  fi
  chk "$(curl -s -o /dev/null -w '%{http_code}' "$CALAPI/health/ready")" "200" "calibration instance is still ready after the hard-bound round"
fi

echo "=== round 5: the same hard-bound path with an explicitly lowered bound (deterministic) ==="
if [ "$EXPECT" = "old" ]; then
  echo "SKIP round 5: the frozen pre-fix candidate has no SCOPE_COMMIT_GATE_RESPONSE_HARD_MAX_BYTES knob, so a lowered bound cannot change its behaviour"
  HARD_LOW_SKIPPED=1
fi
if [ "$EXPECT" = "new" ]; then
stop_calib calib_pid
printf 'export SCOPE_COMMIT_GATE_RESPONSE_HARD_MAX_BYTES=%s\n' "$HARD_LOW_BYTES" > "$EV/hard-low.env"
start_calib hard_pid "$PORT_HARD" "$EV/hard-low.env" hard.log
BEFORE_LOW=$(canvas_rows "A7POS-$TS")
LTOOBIG0=$(metric_val "$HARDAPI" cs_scope_commit_gate_response_too_large_total)
LCODE=$(curl -s -b "$RB" -o "$EV/low-body.bin" -w '%{http_code} %{size_download}' -m 180 \
  -X POST "$HARDAPI/api/v1/cases/$CASEID/canvases" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -H "X-CSRF-Token: $CSRF_B" --data-binary @"$EV/payload-neg.json")
LH=$(printf '%s' "$LCODE" | awk '{print $1}')
LSIZE=$(wc -c < "$EV/low-body.bin")
LERR=$(jget < "$EV/low-body.bin" "['error']['code']" 2>/dev/null || echo "-")
AFTER_LOW=$(canvas_rows "A7POS-$TS")
LMARK=$(marker_count hard.log "exceeded the hard bound")
LTOOBIG1=$(metric_val "$HARDAPI" cs_scope_commit_gate_response_too_large_total)
echo "low_bound_too_large_metric before=$LTOOBIG0 after=$LTOOBIG1"
echo "low_bound_round http=$LH body_bytes=$LSIZE error=$LERR rows_before=$BEFORE_LOW rows_after=$AFTER_LOW cancel_markers=$LMARK"
chk "$LH" "503" "a response past a lowered hard bound fails closed with a stable 503"
chk "$LERR" "INTERNAL_RESPONSE_TOO_LARGE" "the stable hard-bound error code is returned at the lowered bound"
chk "$AFTER_LOW" "$BEFORE_LOW" "the lowered-bound rollback left the business rows unchanged"
chk "$(spool_count)" "0" "the lowered-bound rollback left no spool file"
if [ "$LMARK" -ge 1 ]; then ok "the server recorded the lowered-bound cancel"; else bad "the lowered-bound cancel marker is absent"; fi
if [ $(( LTOOBIG1 - LTOOBIG0 )) -ge 1 ]; then ok "the lowered-bound refusal counter moved (deterministic proof the cancel path ran)"; else bad "the lowered-bound refusal counter did not move"; fi
stop_calib hard_pid
else
  stop_calib calib_pid
fi

echo "=== restore and final assertions ==="
curl -s -m 60 -b "$RA" -c "$RA" -o "$EV/switch-back.json" -w '%{http_code}' \
  -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
  -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $CSRF_A" -d "{\"workspace_id\":\"$WSA\"}" > "$EV/switch-back-code.txt" 2>/dev/null
chk "$(cat "$EV/switch-back-code.txt")" "200" "the caller session was switched back to workspace A"
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged after the gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is still ready"
chk "$(spool_count)" "0" "no spool file exists anywhere after the gate"
chk "$(ss -ltn 2>/dev/null | grep -cE ":$PORT |:$PORT_HARD ")" "0" "both calibration ports are released"
DISK1=$(df --output=avail -k / | tail -1 | tr -d ' ')
echo "disk_avail_kb_after=$DISK1"
if [ "$DISK1" -gt $(( DISK0 - 204800 )) ]; then ok "disk usage stayed bounded during the gate"; else bad "disk dropped by more than 200 MiB during the gate"; fi
echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
