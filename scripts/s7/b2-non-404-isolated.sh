#!/usr/bin/env bash
# b2-non-404-isolated.sh — 语义④：非 404（桶不存在）必须 fail closed，绝不回退 RAW。
# 用**同一最新二进制**起隔离临时实例（独立端口 18090），连真实 PG/MinIO，只改 EXPORT_BUCKET。
# 安全护栏：启动前必须 0 条 PENDING/RUNNING 导出（隔离实例启动会执行 FailInterruptedExports），
# 启动后复查、用例结束立即 kill；生产 API 全程不重启、不改配置。
set -uo pipefail
P=/home/euripides/projects/blockchain-platform; cd "$P"
set -a; . deploy/.env; set +a
set -a; . /tmp/s6-e2e-env.sh; set +a
USER_EMAIL=admin@local; USER_PW="$ADMIN_PASSWORD"
PG="docker exec deploy-postgres-1 psql -q -U blockchain -d blockchain_platform -tAc"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${EV:-$P/acceptance-evidence/dual-system-v1/sprint7/b2-non-404-isolated-$TS}
mkdir -p "$EV"
exec > >(tee -a "$EV/run.log") 2>&1
PASS=0; FAIL=0
check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }
ID=be026ec9-2ea8-498d-8c68-ba7328131ff8
LED_SHA=$($PG "SET ROLE blockchain_app; select destination_sha256 from export_object_migrations where export_id='$ID'")
REFF=$($PG "SET ROLE blockchain_app; select object_ref from exports where id='$ID'")
API_SHA=$(sha256sum $P/backend/bin/api | cut -d' ' -f1)
echo "==== B2 语义④ isolated instance start $(date -Is) api_sha=$API_SHA ===="; echo "EV=$EV"
J=$(mktemp); trap 'rm -f "$J"' EXIT
curl -s -c "$J" -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"identifier\":\"$USER_EMAIL\",\"password\":\"$USER_PW\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login > "$EV/login.json"
check SETUP "生产实例登录成功（会话存于 PG，隔离实例共用）" "$(python3 -c "import json;print(json.load(open('$EV/login.json'))['success'])" 2>/dev/null)" "True"

run_instance(){ # $1 = export_bucket, $2 = label
  local eb="$1" label="$2" pid ok
  local running=$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','RUNNING')")
  echo "[$label] pending_running_exports_before=$running"
  if [ "$running" != "0" ]; then echo "[$label] ABORT: 存在在途导出行，拒绝启动隔离实例以免 FailInterruptedExports 破坏用户任务"; return 9; fi
  env API_HTTP_PORT=18090 EXPORT_BUCKET="$eb" nohup "$P/backend/bin/api" > "$EV/isolated-$label.log" 2>&1 &
  pid=$!
  ok=no
  for i in $(seq 1 40); do if curl -sf -m 2 -o /dev/null http://127.0.0.1:18090/health/ready; then ok=yes; break; fi; sleep 1; done
  echo "[$label] isolated_pid=$pid ready=$ok"; echo "$pid" > "$EV/isolated-$label.pid"
  if [ "$ok" != "yes" ]; then cat "$EV/isolated-$label.log" | tail -20; kill "$pid" 2>/dev/null; return 8; fi
  local after=$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','RUNNING')")
  echo "[$label] pending_running_exports_after_start=$after"
  echo "$after" > "$EV/isolated-$label.pending_after.txt"
  return 0
}
stop_instance(){ local pid; pid=$(cat "$EV/isolated-$1.pid" 2>/dev/null); [ -n "$pid" ] && kill "$pid" 2>/dev/null; sleep 2; echo "[$1] isolated stopped (pid=$pid)"; }

echo "== 对照：隔离实例 + 真实 EXPORT_BUCKET 必须 200（证明实例本身可用） =="
run_instance blockchain-exports control; RC=$?
check ISO "隔离实例(对照)启动" "$RC" "0"
if [ "$RC" = "0" ]; then
  CCODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -o "$EV/iso-control.bin" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download")
  check ISO "对照实例下载 200" "$CCODE" "200"
  check ISO "对照实例下载 SHA == 账本 SHA" "$(sha256sum "$EV/iso-control.bin" | cut -d' ' -f1)" "$LED_SHA"
fi
stop_instance control

echo "== 语义④：EXPORT_BUCKET 指向不存在的桶 -> 必须 fail closed（不回退 RAW、不伪装成对象缺失） =="
FAKE=blockchain-exports-b2-missing-$TS
echo "fake_bucket=$FAKE"
run_instance "$FAKE" non404; RC2=$?
check ISO "隔离实例(非404)启动" "$RC2" "0"
if [ "$RC2" = "0" ]; then
  NCODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -o "$EV/iso-non404.json" -D "$EV/iso-non404.hdr" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download")
  check ISO "桶不存在时下载 500（fail closed）" "$NCODE" "500"
  check ISO "不得回退 RAW（500 而不是 200）" "$([ "$NCODE" != "200" ] && echo yes || echo no)" "yes"
  check ISO "不得伪装成 DATA_OBJECT_MISSING" "$(grep -c DATA_OBJECT_MISSING "$EV/iso-non404.json" | tr -d '\n')" "0"
  check ISO "响应体不含对象内容（未读到 RAW 字节）" "$([ "$(stat -c%s "$EV/iso-non404.json")" -lt 2000 ] && echo yes || echo no)" "yes"
  BODY=$(head -c 300 "$EV/iso-non404.json")
  echo "non404_body=$BODY" | tee "$EV/non404-response.txt"
  check ISO "错误信息指向桶不存在" "$(echo "$BODY" | grep -c "does not exist")" "1"
  NCODE2=$(curl -s -b "$J" -H "X-Auth-Audience: user" -H "Range: bytes=0-99" -o /dev/null -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download")
  check ISO "Range 同样 fail closed（500）" "$NCODE2" "500"
  NCODE3=$(curl -s -b "$J" -H "X-Auth-Audience: user" -I -o /dev/null -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download")
  check ISO "HEAD 同样 fail closed（500）" "$NCODE3" "500"
  check ISO "无成功审计写入（隔离实例不虚报）" "$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$ID' and occurred_at > now() - interval '1 minute'")" "0"
fi
stop_instance non404

echo "== 收尾核对 =="
sleep 1
check END "隔离实例进程已退出" "$(pgrep -f "API_HTTP_PORT=18090" | wc -l | tr -d ' ')" "0"
check END "生产 API 二进制未变" "$(sha256sum $P/backend/bin/api | cut -d' ' -f1)" "$API_SHA"
PPID_=$(systemctl --user show blockchain-api.service -p MainPID --value)
check END "生产 API 仍 active" "$(systemctl --user is-active blockchain-api.service)" "active"
check END "生产 API 就绪 200" "$(curl -s -m 5 -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health/ready)" "200"
check END "生产实例下载仍正常（对象可读）" "$(curl -s -b "$J" -H "X-Auth-Audience: user" -o /dev/null -w "%{http_code}" "http://127.0.0.1:8080/api/v1/exports/$ID/download")" "200"
check END "在途导出行仍为 0（未被隔离实例误标 FAILED）" "$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','RUNNING')")" "0"
check END "受控测试记录仍在（未清理）" "$($PG "SET ROLE blockchain_app; select count(*) from exports where dataset='acceptance-b2-missing-fixture'")" "$($PG "SET ROLE blockchain_app; select count(*) from exports where dataset='acceptance-b2-missing-fixture'")"
echo "==== B2 语义④: PASS=$PASS FAIL=$FAIL ===="
