#!/usr/bin/env bash
# s7-deploy-api-p1a-commit-gate.sh -- controlled deployment of the P1-A commit gate.
# Backs up the running binary, installs the candidate, restarts only blockchain-api, and
# asserts runtime identity, readiness, census and journal. No other unit is touched.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
CAND=${CAND:-$R/backend/bin.new/api-p1a}
CAND_SHA=$(sha256sum "$CAND" | cut -d\  -f1)
EV=$R/acceptance-evidence/dual-system-v1/sprint7/s7-deploy-api-p1a-$TS
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee "$EV/deploy-console.log") 2>&1
PASS=0; FAIL=0
ok(){ PASS=$((PASS+1)); echo "PASS[$PASS] $1"; }
bad(){ FAIL=$((FAIL+1)); echo "FAIL[$FAIL] $1"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (got='$1' want='$2')"; fi; }
pgq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -F'|' -v ON_ERROR_STOP=1; }

echo "ts_utc=$TS candidate=$CAND sha=$CAND_SHA"
OLD_PID=$(systemctl --user show blockchain-api.service -p MainPID --value)
OLD_SHA=$(sha256sum /proc/$OLD_PID/exe | cut -d\  -f1)
WPID=$(systemctl --user show blockchain-worker.service -p MainPID --value)
WSHA=$(sha256sum /proc/$WPID/exe | cut -d\  -f1)
echo "old_api_pid=$OLD_PID old_api_sha=$OLD_SHA worker_pid=$WPID worker_sha=$WSHA"
JOBS0=$(pgq <<SQL
SELECT count(*) FROM download_jobs;
SQL
); SH0=$(pgq <<SQL
SELECT count(*) FROM download_shards;
SQL
); CK0=$(pgq <<SQL
SELECT count(*) FROM checkpoints;
SQL
); OB0=$(pgq <<SQL
SELECT count(*) FROM outbox_events;
SQL
); AU0=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)
echo "census_before jobs=$JOBS0 shards=$SH0 checkpoints=$CK0 outbox=$OB0 audit_head=$AU0"
chk "$(pgq <<SQL
SELECT count(*) FROM exports WHERE status IN ('PENDING','RUNNING');
SQL
)" "0" "no export is in flight before the restart"

BAK=$R/backend/bin/api.bak-p1a-$TS
cp -p "$R/backend/bin/api" "$BAK"
chk "$(sha256sum "$BAK" | cut -d\  -f1)" "$OLD_SHA" "backup of the running binary matches the running sha"
install -m 755 "$CAND" "$R/backend/bin/api.new"
mv -f "$R/backend/bin/api.new" "$R/backend/bin/api"
chk "$(sha256sum "$R/backend/bin/api" | cut -d\  -f1)" "$CAND_SHA" "installed on-disk binary is the candidate"

systemctl --user restart blockchain-api.service
for _ in $(seq 1 40); do
  NEW_PID=$(systemctl --user show blockchain-api.service -p MainPID --value)
  RC=$(curl -s -o "$EV/ready.json" -w '%{http_code}' http://127.0.0.1:8080/health/ready 2>/dev/null)
  [ "$RC" = "200" ] && [ "$NEW_PID" != "$OLD_PID" ] && [ "$NEW_PID" != "0" ] && break
  sleep 0.5
done
echo "new_api_pid=$NEW_PID ready_http=$RC"
if [ "$NEW_PID" != "$OLD_PID" ] && [ -n "$NEW_PID" ] && [ "$NEW_PID" != "0" ]; then ok "the API process really restarted"; else bad "the API process did not change (pid=$NEW_PID)"; fi
chk "$(sha256sum /proc/$NEW_PID/exe | cut -d\  -f1)" "$CAND_SHA" "the running process is the new binary"
chk "$RC" "200" "readiness returns 200"
chk "$(python3 -c "import json;d=json.load(open('$EV/ready.json'));print('|'.join(k+'='+str(v.get('status',v)) for k,v in sorted(d.get('dependencies',{}).items())))" 2>/dev/null)" "clickhouse=ok|minio=ok|nats=ok|postgres=ok" "all four dependencies report ok"
chk "$(sha256sum /proc/$WPID/exe | cut -d\  -f1)" "$WSHA" "the worker binary is unchanged by this deployment"
chk "$(systemctl --user is-active blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console | tr '\n' ' ')" "active active active active " "all four units are active"

chk "$(pgq <<SQL
SELECT count(*) FROM download_jobs;
SQL
)" "$JOBS0" "download_jobs census did not change"
chk "$(pgq <<SQL
SELECT count(*) FROM download_shards;
SQL
)" "$SH0" "download_shards census did not change"
chk "$(pgq <<SQL
SELECT count(*) FROM checkpoints;
SQL
)" "$CK0" "checkpoints census did not change"
chk "$(pgq <<SQL
SELECT count(*) FROM outbox_events;
SQL
)" "$OB0" "outbox_events census did not change"
AU1=$(pgq <<SQL
SELECT max(seq) FROM audit_events_v2;
SQL
)
if [ "$AU1" -ge "$AU0" ]; then ok "audit chain head advanced monotonically ($AU0 -> $AU1)"; else bad "audit head went backwards ($AU0 -> $AU1)"; fi
chk "$(pgq <<SQL
SELECT must_change_password::text FROM identities WHERE email='admin@local';
SQL
)" "true" "the real administrator account is untouched"

if grep -qiE 'panic|fatal' /tmp/api.log 2>/dev/null; then bad "the API journal contains panic/fatal"; else ok "no panic or fatal in the API journal"; fi
echo "--- last api journal lines ---"; tail -3 /tmp/api.log
PGD=$(grep -cE 'scope commit gate' /tmp/api.log 2>/dev/null || true)
echo "scope_gate_log_lines=$PGD"

code=$(curl -s -o "$EV/anon.json" -w '%{http_code}' -X POST http://127.0.0.1:8080/api/v2/cases -H 'Content-Type: application/json' -d '{"case_no":"x","name":"x"}')
chk "$code" "401" "anonymous state-changing request is still 401"
chk "$(ss -ltn | grep -cE ':(18080|18090|18130) ')" "0" "no listener on 18080/18090/18130 after the deployment"

echo "TOTAL_PASS=$PASS TOTAL_FAIL=$FAIL"
if [ "$FAIL" = "0" ]; then echo "S7_DEPLOY_API_P1A_PASS=1"; else echo "S7_DEPLOY_API_P1A_PASS=0"; fi
echo "evidence=$EV"
