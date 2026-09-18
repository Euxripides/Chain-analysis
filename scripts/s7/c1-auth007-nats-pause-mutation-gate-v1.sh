#!/usr/bin/env bash
# c1-auth007-nats-pause-mutation-gate-v1.sh
#
# P1-B: AUTH-007 while the event bus is really unavailable.
#
# Every scope mutation (workspace switch, logout, session revoke, identity
# revoke, admin force revoke) closes the SSE subscriptions of the affected scope
# and then asks the other API instances to do the same through NATS:
#
#   closeScopeSSESubscriptionsCtx -> publishSSEScopeControl
#                                 -> deps.Bus.Publish(context.Background(), ...)
#
# AUTH-007 requires those mutations to take effect within five seconds. The bus
# publish is documented as "best effort: the local close never depends on it",
# but it is called SYNCHRONOUSLY with a context that has no deadline at all, so
# a bus that accepts the TCP connection and then stops answering can hold the
# mutation open for as long as it likes. This gate measures that instead of
# arguing about it: the NATS container is really paused, the mutations are really
# issued, and the bus is really resumed afterwards.
#
# The pause is bounded and always released by a trap, including on interrupt.
#
# ASCII only. No credential, Cookie, CSRF token or DSN is printed.
set -uo pipefail

R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
LABEL="${LABEL:-natspause}"
BUDGET_MS="${BUDGET_MS:-5000}"
PROBE_TIMEOUT="${PROBE_TIMEOUT:-12}"
NATS_CTR="${NATS_CTR:-deploy-nats-1}"
NATS_MON="${NATS_MON:-http://127.0.0.1:8222}"
API_EXPECT_SHA="${API_EXPECT_SHA:-910b7ca29e8cbb6dc464196a481e63d1ba80335db25a359dfd59f1ec6d7cea38}"
set +u
if [ -z "$EV" ]; then EV=$R/acceptance-evidence/dual-system-v1/sprint7/c1-auth007-$LABEL-$TS; fi
set -u
API=http://127.0.0.1:8080
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
jsz_messages(){ curl -s -m 5 "$NATS_MON/jsz?streams=1" 2>/dev/null | jget "['messages']"; }
bus_state(){ docker inspect -f '{{.State.Status}} {{.State.Paused}}' "$NATS_CTR" 2>/dev/null | tr -d '\n'; }

PAUSED=0
unpause(){
  if [ "$PAUSED" = "1" ]; then
    docker unpause "$NATS_CTR" >/dev/null 2>&1 || true
    PAUSED=0
    sleep 1
  fi
}
trap 'unpause' EXIT INT TERM

echo "ts_utc=$TS script_sha256=$(sha256sum "$0" | cut -d\  -f1)"
echo "label=$LABEL budget_ms=$BUDGET_MS probe_timeout_s=$PROBE_TIMEOUT nats_container=$NATS_CTR"
echo "evidence=$EV"

AP=$(systemctl --user show blockchain-api.service -p MainPID --value)
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is unchanged by this gate"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "production API is ready"
echo "bus_state_before=$(bus_state)"
MSGS0=$(jsz_messages)

echo "=== fixture (dedicated c1lat organisation; the real administrator is untouched) ==="
FIX=$R/backend/bin/fixture-membership
CRED=/home/euripides/.c1lat-creds.env
( set -a; . "$R/deploy/.env"; set +a
  "$FIX" -org-slug c1lat -org-name "AUTH-007 NATS pause acceptance" \
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
PW1="Pw-$TS-natspause-77e1"
SECPW1="Pw-$TS-natspause-2b4c"

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
clear_flag inv "$INVEMAIL" "$INVPW" "$PW1" user
clear_flag sec "$SECEMAIL" "$SECPW" "$SECPW1" admin
rm -f "$EV"/clear-*.jar "$EV"/clear-*.json

login_ws(){
  local j=$1 email=$2 pw=$3 ws=$4 aud=$5 out=$6
  rm -f "$j"
  curl -s -c "$j" -o "$out" -w '%{http_code}' -X POST "$API/api/auth/v1/login" \
    -H 'Content-Type: application/json' -H "X-Auth-Audience: $aud" \
    -d "{\"identifier\":\"$email\",\"password\":\"$pw\",\"audience\":\"$aud\",\"workspace_id\":\"$ws\"}"
}
RA=$EV/ra.jar; RB=$EV/rb.jar
chk "$(login_ws "$RA" "$INVEMAIL" "$PW1" "$WSA" user "$EV/ra-login.json")" "200" "caller session RA logs in to workspace A"
chk "$(login_ws "$RB" "$INVEMAIL" "$PW1" "$WSB" user "$EV/rb-login.json")" "200" "victim session RB logs in to workspace B"
SID_A=$(sess_id "$RA" user); SID_B=$(sess_id "$RB" user)
if [ -z "$SID_A" ] || [ "$SID_A" = "__ERR__" ] || [ -z "$SID_B" ] || [ "$SID_B" = "__ERR__" ]; then bad "session ids not resolvable"; exit 0; fi
CSRF_A=$(csrf_of "$RA" cs_user_session_csrf)
SECJAR=$EV/sec.jar
chk "$(login_ws "$SECJAR" "$SECEMAIL" "$SECPW1" "$WSA" admin "$EV/sec-login.json")" "200" "security_admin session logs in (admin audience)"
SEC_CSRF=$(csrf_of "$SECJAR" cs_admin_session_csrf)

switch_probe(){ # $1 jar $2 csrf $3 target-ws $4 file-tag
  local t0 t1 code
  t0=$(now_ms)
  code=$(curl -s -b "$1" -c "$1" -o "$EV/$4.json" -w '%{http_code}' -m "$PROBE_TIMEOUT" \
    -X POST "$API/api/auth/v1/session/workspace" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $2" -d "{\"workspace_id\":\"$3\"}")
  t1=$(now_ms)
  echo "$code $(( t1 - t0 ))"
}
logout_probe(){ # $1 jar $2 csrf $3 tag
  local t0 t1 code
  t0=$(now_ms)
  code=$(curl -s -b "$1" -c "$1" -o "$EV/$3.json" -w '%{http_code}' -m "$PROBE_TIMEOUT" \
    -X POST "$API/api/auth/v1/logout" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: user' -H "X-CSRF-Token: $2" -d '{}')
  t1=$(now_ms)
  echo "$code $(( t1 - t0 ))"
}
revoke_probe(){ # $1 secjar $2 csrf $3 target-sid $4 tag
  local t0 t1 code
  t0=$(now_ms)
  code=$(curl -s -b "$1" -o "$EV/$4.json" -w '%{http_code}' -m "$PROBE_TIMEOUT" \
    -X POST "$API/api/admin/v1/sessions/$3/commands/revoke" -H 'Content-Type: application/json' \
    -H 'X-Auth-Audience: admin' -H "X-CSRF-Token: $2" -d '{"confirm":true,"reason":"nats-pause-drill"}')
  t1=$(now_ms)
  echo "$code $(( t1 - t0 ))"
}

echo "=== 1. baseline: the same mutations with the bus healthy ==="
read -r C1 M1 <<< "$(switch_probe "$RA" "$CSRF_A" "$WSB" sw-base)"
echo "baseline_switch http=$C1 elapsed_ms=$M1"
chk "$C1" "200" "1: the workspace switch works with the bus healthy"
if [ "$M1" -le "$BUDGET_MS" ]; then ok "1: the healthy switch stayed inside AUTH-007 ($M1 ms <= $BUDGET_MS ms)"; else bad "1: the healthy switch took $M1 ms"; fi
read -r C2 M2 <<< "$(switch_probe "$RA" "$CSRF_A" "$WSA" sw-back)"
chk "$C2" "200" "1: the switch back to workspace A works"

echo "=== 2. the bus is really paused (SIGSTOP on the NATS container) ==="
docker pause "$NATS_CTR" >/dev/null 2>&1 && PAUSED=1
sleep 1
echo "bus_state_paused=$(bus_state)"
chk "$(docker inspect -f '{{.State.Paused}}' "$NATS_CTR" 2>/dev/null | tr -d '\n')" "true" "2: the NATS container really is paused"
if [ "$(curl -s -o /dev/null -w '%{http_code}' -m 3 "$NATS_MON/varz")" != "200" ]; then ok "2: the bus monitoring endpoint confirms the server is not answering"; else bad "2: the NATS server still answers while it is supposed to be paused"; fi

echo "=== 3. workspace switch while the bus is unavailable ==="
read -r C3 M3 <<< "$(switch_probe "$RA" "$CSRF_A" "$WSB" sw-paused)"
# Settle before reading the database: a client that gave up at the probe timeout
# may still have a handler in flight, and the state after it finishes is the fact
# that matters, not the state at the moment curl gave up.
sleep 3
echo "paused_switch http=$C3 elapsed_ms=$M3"
if [ "$C3" = "200" ]; then ok "3: the switch still answered 200 while the bus was down"; else bad "3: the switch did not answer 200 while the bus was down (http=$C3)"; fi
if [ "$M3" -le "$BUDGET_MS" ]; then ok "3: the switch stayed inside the AUTH-007 budget during the outage ($M3 ms <= $BUDGET_MS ms)"; else bad "3: the switch took $M3 ms during the outage (budget $BUDGET_MS ms)"; fi
chk "$(pgq <<SQL
SELECT workspace_id::text FROM sessions WHERE id='$SID_A'::uuid;
SQL
)" "$WSB" "3: the switched session really carries the new workspace in the database"

echo "=== 4. logout while the bus is unavailable ==="
read -r C4 M4 <<< "$(logout_probe "$RB" "$(csrf_of "$RB" cs_user_session_csrf)" lo-paused)"
sleep 3
echo "paused_logout http=$C4 elapsed_ms=$M4"
if [ "$C4" = "200" ] || [ "$C4" = "204" ]; then ok "4: logout still answered while the bus was down ($C4)"; else bad "4: logout did not answer while the bus was down (http=$C4)"; fi
if [ "$M4" -le "$BUDGET_MS" ]; then ok "4: logout stayed inside the AUTH-007 budget during the outage ($M4 ms <= $BUDGET_MS ms)"; else bad "4: logout took $M4 ms during the outage (budget $BUDGET_MS ms)"; fi
chk "$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_B'::uuid AND revoked_at IS NOT NULL;
SQL
)" "1" "4: the logged-out session really is revoked in the database"

echo "=== 5. admin force revoke while the bus is unavailable ==="
SID_A2=$(sess_id "$RA" user)
read -r C5 M5 <<< "$(revoke_probe "$SECJAR" "$SEC_CSRF" "$SID_A2" rv-paused)"
sleep 3
echo "paused_admin_revoke http=$C5 elapsed_ms=$M5"
if [ "$C5" = "200" ]; then ok "5: the admin revoke still answered 200 while the bus was down"; else bad "5: the admin revoke did not answer 200 while the bus was down (http=$C5)"; fi
if [ "$M5" -le "$BUDGET_MS" ]; then ok "5: the admin revoke stayed inside the AUTH-007 budget during the outage ($M5 ms <= $BUDGET_MS ms)"; else bad "5: the admin revoke took $M5 ms during the outage (budget $BUDGET_MS ms)"; fi
REV=$(pgq <<SQL
SELECT count(*) FROM sessions WHERE id='$SID_A2'::uuid AND revoked_at IS NOT NULL;
SQL
)
echo "revoked_after_admin=$REV"
chk "$REV" "1" "5: the force-revoked session really is revoked in the database"

echo "=== 6. the bus is resumed and the system recovers ==="
unpause
sleep 2
chk "$(bus_state | awk '{print $1}')" "running" "6: the NATS container is running again"
chk "$(curl -s -o /dev/null -w '%{http_code}' -m 5 "$NATS_MON/varz")" "200" "6: the bus monitoring endpoint answers again"
chk "$(curl -s -o /dev/null -w '%{http_code}' "$API/health/ready")" "200" "6: the production API is ready after the outage"
MSGS1=$(jsz_messages)
echo "bus_messages_before=$MSGS0 after=$MSGS1 delta=$(( MSGS1 - MSGS0 ))"
RA2=$EV/ra2.jar
chk "$(login_ws "$RA2" "$INVEMAIL" "$PW1" "$WSA" user "$EV/ra2-login.json")" "200" "6: a fresh session logs in after the outage"
CSRF_A2=$(csrf_of "$RA2" cs_user_session_csrf)
read -r C7 M7 <<< "$(switch_probe "$RA2" "$CSRF_A2" "$WSB" sw-recovered)"
echo "recovered_switch http=$C7 elapsed_ms=$M7"
chk "$C7" "200" "6: a switch after the outage works again"
if [ "$M7" -le "$BUDGET_MS" ]; then ok "6: the recovered switch is back inside the AUTH-007 budget ($M7 ms)"; else bad "6: the recovered switch took $M7 ms"; fi

echo "=== 8. final invariants ==="
chk "$(sha256sum /proc/$AP/exe 2>/dev/null | cut -d\  -f1)" "$API_EXPECT_SHA" "production API binary is still unchanged"
chk "$(pgq <<SQL
SELECT count(*) FROM identities WHERE email='admin@local' AND must_change_password;
SQL
)" "1" "the real admin identity is still in the same must_change_password state"
chk "$(pgq <<SQL
SELECT count(*) FROM pg_stat_activity WHERE application_name LIKE 'bpgate:%';
SQL
)" "0" "no commit-gate backend is left behind"
SWPROBES=$(pgq <<SQL
SELECT count(*) FROM audit_events_v2 WHERE route_template='/api/auth/v1/session/workspace' AND occurred_at > now() - interval '30 minutes';
SQL
)
echo "workspace_switch_audit_rows_30m=$SWPROBES"

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "GATE_FAIL=$FAIL"
exit 0
