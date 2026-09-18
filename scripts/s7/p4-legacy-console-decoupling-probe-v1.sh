#!/usr/bin/env bash
# LEGACY-CONSOLE-01 probe: the legacy console keeps its own read/write path
# through an entry-layer token, while anonymous access stays refused (the
# 2026-09-13 S7-SEC-LEGACY-V1 P0 is NOT reopened). Candidate only.
#
# No data is created: the write probe sends an invalid body, so an authorised
# request is answered by validation (400) and never reaches an inserting handler.
set -u
R=/home/euripides/projects/blockchain-platform
BIN="${BIN:-$R/backend/bin.new/api-legacyconsole-calib}"
PORT="${PORT:-18199}"
TOKEN="legacy-console-probe-token-0001"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/p4-legacyconsole-$TS}"
API="http://127.0.0.1:$PORT"
mkdir -p "$EV"
PASS=0
FAIL=0
chk(){ if [ "$1" = "$2" ]; then PASS=$((PASS+1)); echo "PASS $3 ($1)"; else FAIL=$((FAIL+1)); echo "FAIL $3 (got=$1 want=$2)"; fi; }
SUM=$(printf %s "$TOKEN" | sha256sum | cut -d' ' -f1)

echo "=== candidate $BIN on port $PORT (evidence $EV) ==="
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free before start"
(
  cd "$R/backend"
  set -a; . "$R/deploy/.env"; set +a
  export API_HTTP_PORT="$PORT"
  export LEGACY_CONSOLE_TOKEN_SHA256="$SUM"
  export LEGACY_CONSOLE_PERMISSIONS="investigation:list,investigation:read,investigation:create,report:list,subject:list,download:create,explorer:read,job:list:workspace,job:read,job:control,export:create,export:read,export:download,case:list,case:read,case:create,canvas:list,admin:provider:read,admin:cost:read,admin:performance:read,admin:performance:update,admin:dataset:read,admin:dataset:manage"
  export LEGACY_CONSOLE_ORGANIZATION_ID="d3b152f2-44c0-441c-b047-be24c137a84b"
  export LEGACY_CONSOLE_WORKSPACE_ID="000e31bb-8c48-435d-af97-95bbc75a496c"
  export LEGACY_CONSOLE_IDENTITY_ID="6e192e52-d0aa-4e4a-9497-4953a332cfd3"
  export LEGACY_CONSOLE_AUDIENCE="admin"
  nohup setsid "$BIN" >> "$EV/candidate.log" 2>&1 &
  echo $! > "$EV/candidate.pid"
)
sleep 1
cand_pid=$(cat "$EV/candidate.pid")
ready=0
for _ in $(seq 1 240); do
  rc=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "$API/health/ready" 2>/dev/null)
  if [ "$rc" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
chk "$ready" "1" "candidate ready on $PORT"
if [ "$ready" != "1" ]; then
  kill -KILL "$cand_pid" 2>/dev/null
  echo "READY_ABORT=1"
  echo "=== summary ==="; echo "PASS=$PASS FAIL=$FAIL"; echo "PROBE_FAIL=$FAIL"
  exit 0
fi
chk "$(sha256sum /proc/$cand_pid/exe 2>/dev/null | cut -d' ' -f1)" "$(sha256sum "$BIN" | cut -d' ' -f1)" "running candidate is the binary under test"

probe(){
  local name=$1 want=$2 method=$3 path=$4 data=$5 header=$6
  local out="$EV/$(echo "$name" | tr ' /' '__').json"
  local got
  if [ -n "$header" ] && [ -n "$data" ]; then
    got=$(curl -s -o "$out" -w '%{http_code}' -X "$method" -H "$header" -H 'Content-Type: application/json' --data "$data" "$API$path")
  elif [ -n "$header" ]; then
    got=$(curl -s -o "$out" -w '%{http_code}' -X "$method" -H "$header" "$API$path")
  elif [ -n "$data" ]; then
    got=$(curl -s -o "$out" -w '%{http_code}' -X "$method" -H 'Content-Type: application/json' --data "$data" "$API$path")
  else
    got=$(curl -s -o "$out" -w '%{http_code}' -X "$method" "$API$path")
  fi
  chk "$got" "$want" "$name"
}

echo "=== the legacy console with its own token ==="
probe "legacy cases list" 200 GET "/api/v1/cases" "" "X-Legacy-Console-Token: $TOKEN"
probe "legacy job list" 200 GET "/api/v1/download-jobs?limit=1" "" "X-Legacy-Console-Token: $TOKEN"
probe "legacy investigation list" 200 GET "/api/v1/investigations" "" "X-Legacy-Console-Token: $TOKEN"
probe "legacy ankr provider status admin key" 200 GET "/api/v1/providers/ankr/status" "" "X-Legacy-Console-Token: $TOKEN"
probe "legacy write route authorises then validation refuses" 400 POST "/api/v1/investigations" '{}' "X-Legacy-Console-Token: $TOKEN"

echo "=== anonymous and wrong-token access stay refused ==="
probe "anonymous cases" 401 GET "/api/v1/cases" "" ""
probe "wrong token cases" 401 GET "/api/v1/cases" "" "X-Legacy-Console-Token: not-the-token"
probe "anonymous jobs" 401 GET "/api/v1/download-jobs?limit=1" "" ""
probe "anonymous investigation list" 401 GET "/api/v1/investigations" "" ""

echo "=== the admin console surface is not opened by the legacy token ==="
# 403, not 401: the legacy token IS authenticated, but the control-plane surface
# requires permissions it was not granted, so the policy engine refuses it.
probe "legacy token on admin jobs" 403 GET "/api/admin/v1/jobs?limit=1" "" "X-Legacy-Console-Token: $TOKEN"
probe "anonymous on admin jobs" 401 GET "/api/admin/v1/jobs?limit=1" "" ""

echo "=== rows actually returned ==="
python3 "$R/scripts/s7/p4_legacy_console_rows.py" "$EV" | tee "$EV/rows.txt"

echo "=== cleanup ==="
kill -TERM "$cand_pid" 2>/dev/null
for _ in $(seq 1 20); do kill -0 "$cand_pid" 2>/dev/null || break; sleep 0.5; done
kill -KILL "$cand_pid" 2>/dev/null
sleep 0.5
chk "$(ss -ltn 2>/dev/null | grep -c ":$PORT ")" "0" "port $PORT is free after cleanup"
# 候选探针全程不碰生产（它只在 $PORT 上跑自己的进程）。这一项是"确认没有误部署"的护栏：
# 期望值随每次发布变化，因此默认只记录、不判 FAIL —— 冻结在脚本里的旧哈希每次发布都会误报，
# 长期看只会训练人忽略这一行。需要严格比对时显式给 EXPECT_DEPLOYED_SHA。
DEPLOYED_SHA="$(sha256sum $R/backend/bin/api | cut -d' ' -f1)"
if [ -n "${EXPECT_DEPLOYED_SHA:-}" ]; then
  chk "$DEPLOYED_SHA" "$EXPECT_DEPLOYED_SHA" "the deployed production binary matches EXPECT_DEPLOYED_SHA"
else
  echo "INFO deployed production binary sha256=$DEPLOYED_SHA (未设 EXPECT_DEPLOYED_SHA，仅记录)"
fi

echo "=== summary ==="
echo "PASS=$PASS FAIL=$FAIL"
echo "PROBE_FAIL=$FAIL"
exit 0
