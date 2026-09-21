#!/usr/bin/env bash
# b2-non-404-isolated-v2.sh — 语义④ fresh 权威轮（根 21:2x 门禁要求全部实装）
# 门禁：1) 启动前冻结 exports census/受控记录/中断计数，结束后逐字比较；2) 按 PID kill -0 + 端口双重确认终止；
#       3) 审计按 request_id 精确查询（不用时间窗）；4) control 全量 SHA / fake GET+Range+HEAD 500 / 非 DATA_OBJECT_MISSING /
#          响应非对象字节 / 0 在途导出且无新增 interrupted。绝不打印凭据值。
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
REFF=$($PG "SET ROLE blockchain_app; select object_ref from exports where id='$ID'")
LED_SHA=$($PG "SET ROLE blockchain_app; select destination_sha256 from export_object_migrations where export_id='$ID'")
LED_BYTES=$($PG "SET ROLE blockchain_app; select bytes from export_object_migrations where export_id='$ID'")
API_SHA=$(sha256sum $P/backend/bin/api | cut -d' ' -f1)
PROD_PID_BEFORE=$(systemctl --user show blockchain-api.service -p MainPID --value)
echo "==== B2③④ fresh authoritative round $(date -Is) api_sha=$API_SHA prod_pid=$PROD_PID_BEFORE ===="
echo "EV=$EV"

echo "== 冻结（启动任何隔离实例之前） =="
$PG "SET ROLE blockchain_app; select status||'='||count(*) from exports group by status order by 1" > "$EV/frozen-exports-census.txt"
$PG "SET ROLE blockchain_app; select id||'|'||status||'|'||coalesce(object_ref,'')||'|'||coalesce(sha256,'')||'|'||coalesce(dataset,'') from exports where dataset='acceptance-b2-missing-fixture' order by id" > "$EV/frozen-controlled-records.txt"
$PG "SET ROLE blockchain_app; select count(*) from exports where error='服务已重启，导出任务中断，请重新导出'" > "$EV/frozen-interrupted-count.txt"
AUD_BEFORE=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$ID'")
echo "$AUD_BEFORE" > "$EV/frozen-success-audit-count.txt"
echo "frozen: census_lines=$(wc -l < "$EV/frozen-exports-census.txt") controlled=$(wc -l < "$EV/frozen-controlled-records.txt") interrupted=$(cat "$EV/frozen-interrupted-count.txt") success_audits=$AUD_BEFORE"
check FREEZE "冻结 exports census 非空" "$([ -s "$EV/frozen-exports-census.txt" ] && echo yes || echo no)" "yes"
check FREEZE "受控测试记录已存在（上一轮创建，保留）" "$([ "$(wc -l < "$EV/frozen-controlled-records.txt")" -ge 1 ] && echo yes || echo no)" "yes"

J=$(mktemp); trap 'rm -f "$J"' EXIT
curl -s -c "$J" -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"identifier\":\"$USER_EMAIL\",\"password\":\"$USER_PW\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login > "$EV/login.json"
check SETUP "生产实例登录（受控 fixture env，未改任何口令/锁状态）" "$(python3 -c "import json;print(json.load(open('$EV/login.json'))['success'])" 2>/dev/null)" "True"

start_iso(){ # $1 bucket $2 label
  local running=$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','RUNNING')")
  echo "[$1] in_flight_exports_before=$running"
  if [ "$running" != "0" ]; then echo "[$1] ABORT: 有在途导出，拒绝启动隔离实例"; echo "" > "$EV/$2.pid"; return 9; fi
  env API_HTTP_PORT=18090 EXPORT_BUCKET="$1" nohup "$P/backend/bin/api" > "$EV/isolated-$2.log" 2>&1 &
  local pid=$!; echo "$pid" > "$EV/$2.pid"
  for i in $(seq 1 40); do curl -sf -m 2 -o /dev/null http://127.0.0.1:18090/health/ready && return 0; sleep 1; done
  tail -20 "$EV/isolated-$2.log"; kill "$pid" 2>/dev/null; return 8
}
stop_iso(){ # $1 label
  local pid; pid=$(cat "$EV/$1.pid" 2>/dev/null)
  [ -n "$pid" ] && kill "$pid" 2>/dev/null
  for i in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
  if kill -0 "$pid" 2>/dev/null; then kill -9 "$pid" 2>/dev/null; sleep 2; fi
  local alive=no; kill -0 "$pid" 2>/dev/null && alive=yes
  echo "[$1] pid=$pid still_alive=$alive" > "$EV/$1-stop-proof.txt"
  echo "$alive"
}

echo "== 对照实例（真实 EXPORT_BUCKET） =="
start_iso blockchain-exports control; RC=$?
check ISO "对照实例启动" "$RC" "0"
CTL_MARK="b2iso-ctl-get-$TS"
if [ "$RC" = "0" ]; then
  CCODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -H "X-Request-Id: $CTL_MARK" -o "$EV/iso-control.bin" -D "$EV/iso-control.hdr" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download")
  check ISO "对照实例全量下载 200" "$CCODE" "200"
  check ISO "对照实例全量 SHA == 账本 destination_sha256" "$(sha256sum "$EV/iso-control.bin" | cut -d' ' -f1)" "$LED_SHA"
  check ISO "对照实例全量字节数 == 账本 bytes" "$(stat -c%s "$EV/iso-control.bin")" "$LED_BYTES"
fi
CTL_ALIVE=$(stop_iso control)
check ISO "对照实例进程已终止（kill -0 失败）" "$(echo "$CTL_ALIVE" | tail -1)" "no"

echo "== 语义④ 非 404：EXPORT_BUCKET 指向不存在的桶 =="
FAKE=blockchain-exports-b2-missing-$TS; echo "fake_bucket=$FAKE" | tee "$EV/fake-bucket.txt"
start_iso "$FAKE" non404; RC2=$?
check ISO "非404实例启动" "$RC2" "0"
if [ "$RC2" = "0" ]; then
  for m in get range head; do
    MK="b2iso-non404-$m-$TS"
    case "$m" in
      get)   CODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -H "X-Request-Id: $MK" -o "$EV/non404-$m.out" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download") ;;
      range) CODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -H "X-Request-Id: $MK" -H "Range: bytes=0-99" -o "$EV/non404-$m.out" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download") ;;
      head)  CODE=$(curl -s -b "$J" -H "X-Auth-Audience: user" -H "X-Request-Id: $MK" -I -o "$EV/non404-$m.out" -w "%{http_code}" "http://127.0.0.1:18090/api/v1/exports/$ID/download") ;;
    esac
    check ISO "非404 $m -> 500 fail closed" "$CODE" "500"
    check ISO "非404 $m 不伪装成 DATA_OBJECT_MISSING" "$(grep -c DATA_OBJECT_MISSING "$EV/non404-$m.out" || true)" "0"
    SZ=$(stat -c%s "$EV/non404-$m.out")
    check ISO "非404 $m 未读到 RAW 对象字节(小于 1000B)" "$([ "$SZ" -lt 1000 ] && echo yes || echo no)" "yes"
    echo "$MK" >> "$EV/non404-request-ids.txt"
  done
  echo "non404_get_body=$(head -c 260 "$EV/non404-get.out")" | tee "$EV/non404-response.txt"
  check ISO "非404 错误信息命名了假桶" "$(grep -c "$FAKE" "$EV/non404-get.out" || true)" "1"
fi
NR_ALIVE=$(stop_iso non404)
check ISO "非404实例进程已终止（kill -0 失败）" "$(echo "$NR_ALIVE" | tail -1)" "no"
check ISO "18090 端口已不可连接" "$(curl -s -m 3 -o /dev/null -w "%{http_code}" http://127.0.0.1:18090/health/live 2>/dev/null; echo x)" "000x"

echo "== 审计按 request_id 精确核对（不使用时间窗） =="
sleep 2
CTL_AUD=$($PG "SET ROLE blockchain_app; select action||'|'||coalesce(result,'-')||'|'||coalesce(detail->>'object_bucket','-')||'|'||coalesce(detail->>'path','-') from audit_events_v2 where request_id='$CTL_MARK'")
check AUDIT "对照请求只有 1 条成功审计且字段正确" "$CTL_AUD" "export.download|success|blockchain-exports|full"
while read -r MK; do
  [ -z "$MK" ] && continue
  SUCC=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where request_id='$MK' and action='export.download'")
  FAILR=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where request_id='$MK' and action='export.download_failed'")
  REASON=$($PG "SET ROLE blockchain_app; select coalesce(string_agg(distinct detail->>'reason', ','),'-') from audit_events_v2 where request_id='$MK'")
  echo "request_id=$MK success_rows=$SUCC failure_rows=$FAILR reasons=$REASON" | tee -a "$EV/non404-audit-rows.txt"
  check AUDIT "非404 请求 $MK 无任何 export.download success" "$SUCC" "0"
  check AUDIT "非404 请求 $MK 有明确失败审计" "$FAILR" "1"
done < "$EV/non404-request-ids.txt"

echo "== 收尾逐字比较（冻结 vs 结束） =="
$PG "SET ROLE blockchain_app; select status||'='||count(*) from exports group by status order by 1" > "$EV/after-exports-census.txt"
$PG "SET ROLE blockchain_app; select id||'|'||status||'|'||coalesce(object_ref,'')||'|'||coalesce(sha256,'')||'|'||coalesce(dataset,'') from exports where dataset='acceptance-b2-missing-fixture' order by id" > "$EV/after-controlled-records.txt"
$PG "SET ROLE blockchain_app; select count(*) from exports where error='服务已重启，导出任务中断，请重新导出'" > "$EV/after-interrupted-count.txt"
check END "exports census 逐字一致" "$(diff -q "$EV/frozen-exports-census.txt" "$EV/after-exports-census.txt" >/dev/null && echo identical || echo changed)" "identical"
check END "受控测试记录逐字一致（且仍保留）" "$(diff -q "$EV/frozen-controlled-records.txt" "$EV/after-controlled-records.txt" >/dev/null && echo identical || echo changed)" "identical"
check END "无新增 interrupted 导出" "$(diff -q "$EV/frozen-interrupted-count.txt" "$EV/after-interrupted-count.txt" >/dev/null && echo identical || echo changed)" "identical"
check END "在途导出行仍为 0" "$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','RUNNING')")" "0"
check END "生产 API 二进制未变" "$(sha256sum $P/backend/bin/api | cut -d' ' -f1)" "$API_SHA"
check END "生产 API MainPID 未变" "$(systemctl --user show blockchain-api.service -p MainPID --value)" "$PROD_PID_BEFORE"
check END "生产 API active + ready 200" "$(systemctl --user is-active blockchain-api.service):$(curl -s -m 5 -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health/ready)" "active:200"
check END "生产实例下载仍正常" "$(curl -s -b "$J" -H "X-Auth-Audience: user" -o /dev/null -w "%{http_code}" "http://127.0.0.1:8080/api/v1/exports/$ID/download")" "200"
check END "成功审计计数基线可用于后续比对" "$([ -n "$AUD_BEFORE" ] && echo yes || echo no)" "yes"
echo "==== 语义④ fresh round: PASS=$PASS FAIL=$FAIL ===="
