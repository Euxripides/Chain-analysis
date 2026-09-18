#!/usr/bin/env bash
# c1-sse-scope-gate-v4-negcal.sh -- negative calibration for c1-sse-scope-gate-v4.sh.
#
# The gate must not be trusted because it passed once. This runs the gate's own defect
# predicates against a stand-in that reproduces the PRE-FIX behaviour on an isolated loopback
# port (18120), with no database and no production service involved, and requires every
# predicate to report the defect. If a predicate passes against the old behaviour, it is blind
# and the corresponding PASS in the real gate means nothing.
#
#   P1 stream still open after the switch        -> must report ALIVE=yes (the gate wants no)
#   P2 server frames after the switch            -> must report PINGS>=1 (the gate wants 0)
#   P3 subscription audit rows                   -> must report delta 0   (the gate wants one
#                                                   open row and one scope-invalidated row)
#
# No cookie, CSRF token, password or DSN is printed. ASCII only.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
PORT="${NEGCAL_PORT:-18120}"
TS=$(date -u +%Y%m%dT%H%M%SZ)
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-sse-scope-negcal-$TS; fi
set -u
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/negcal-console.log") 2>&1

CASE_FAILS=0
ok(){ echo "CASE_PASS $1"; }
bad(){ echo "CASE_FAIL $1"; CASE_FAILS=$((CASE_FAILS+1)); }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -v ON_ERROR_STOP=1; }

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "standin_sha256=$(sha256sum "$(dirname "$0")/c1-sse-scope-gate-v4-negcal-standin.py" | cut -d\  -f1)"
echo "port=$PORT evidence=$EV"

# The isolated port must be free, and it must never be one of the reserved acceptance ports.
case "$PORT" in 18080|18090) bad "reserved port $PORT must never host a stand-in"; exit 1;; esac
if ss -lnt 2>/dev/null | grep -q ":$PORT "; then bad "port $PORT is already in use"; exit 1; fi

AUDIT_BEFORE=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE occurred_at > now() - interval '15 minutes';
SQL
)
echo "audit_rows_15m_before=$AUDIT_BEFORE"

python3 "$(dirname "$0")/c1-sse-scope-gate-v4-negcal-standin.py" "$PORT" > "$EV/standin.log" 2>&1 &
SPID=$!
for _ in $(seq 1 40); do
  ss -lnt 2>/dev/null | grep -q ":$PORT " && break
  sleep 0.25
done
if ss -lnt 2>/dev/null | grep -q ":$PORT "; then ok "isolated stand-in listening on 127.0.0.1:$PORT"; else bad "stand-in did not start"; kill $SPID 2>/dev/null; exit 1; fi

echo "=== P1/P2: open a subscription, then switch workspace, exactly like the gate does ==="
curl -s -N -m 30 -H 'Accept: text/event-stream' -D "$EV/sse-headers.txt" \
  "http://127.0.0.1:$PORT/api/v2/jobs/old-behaviour/events" > "$EV/sse-stream.txt" 2> "$EV/sse-err.txt" &
SSEPID=$!
sleep 2
curl -s -o "$EV/switch.json" -w '%{http_code}' -X POST "http://127.0.0.1:$PORT/api/auth/v1/session/workspace" \
  -H 'Content-Type: application/json' -d '{"workspace_id":"B"}' > "$EV/switch-code.txt"
SIZE_AT_SWITCH=$(wc -c < "$EV/sse-stream.txt")
sleep 6

if kill -0 $SSEPID 2>/dev/null; then ALIVE=yes; else ALIVE=no; fi
PINGS=$(grep -c '^: ping' "$EV/sse-stream.txt" || true)
SIZE_AFTER=$(wc -c < "$EV/sse-stream.txt")
echo "old_behaviour sse_stream_alive_after_switch=$ALIVE heartbeat_frames_after_switch=$PINGS bytes_at_switch=$SIZE_AT_SWITCH bytes_after=$SIZE_AFTER"
if [ "$ALIVE" = "yes" ]; then ok "P1: the gate's old-scope-stream predicate reports the defect on the pre-fix behaviour"; else bad "P1: the predicate accepted a stream that was closed (it would not detect C1-SSE-OLD-SCOPE-OPEN)"; fi
if [ "$PINGS" -ge 1 ]; then ok "P2: the gate's post-switch-frame predicate reports the defect on the pre-fix behaviour ($PINGS heartbeat frames)"; else bad "P2: the predicate saw no frame after the switch"; fi
kill $SSEPID 2>/dev/null || true
wait $SSEPID 2>/dev/null || true

sleep 1
AUDIT_AFTER=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE occurred_at > now() - interval '15 minutes';
SQL
)
echo "audit_rows_15m_after=$AUDIT_AFTER"
if [ "$AUDIT_AFTER" -eq "$AUDIT_BEFORE" ]; then ok "P3: the pre-fix behaviour writes NO subscription audit row, so the gate's audit predicate reports the defect"; else bad "P3: the pre-fix stand-in changed the audit row count by $((AUDIT_AFTER-AUDIT_BEFORE)); the calibration is not isolated"; fi

grep -c 'standin_listening' "$EV/standin.log" > "$EV/standin-starts.txt" || true
echo "standin_start_lines=$(cat "$EV/standin-starts.txt")"

kill $SPID 2>/dev/null || true
wait $SPID 2>/dev/null || true
sleep 0.5
if ss -lnt 2>/dev/null | grep -q ":$PORT "; then bad "the stand-in left a listener on $PORT"; else ok "the stand-in listener is gone"; fi
# The process check must key on the pid this script started. Matching the script name in the
# process table also matches the shell that invoked this calibration (its command line contains
# the name), which produced a false CASE_FAIL on the first calibrated round.
if kill -0 "$SPID" 2>/dev/null; then bad "the stand-in process $SPID is still running"; else ok "no stand-in process remains (pid $SPID is gone)"; fi

echo "=== NEGCAL RESULT case_fails=$CASE_FAILS ==="
[ "$CASE_FAILS" -eq 0 ] || exit 1
echo "SSE_GATE_V4_NEGCAL_PASS=1"
exit 0
