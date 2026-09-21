#!/usr/bin/env bash
# b2-four-semantics.sh v3 — 使用受控 fixture env；admin@local(user audience) 为默认工作区成员。
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
cd "$P"
set -a; . deploy/.env; set +a
set -a; . /tmp/s6-e2e-env.sh; set +a
USER_EMAIL=admin@local
USER_PW="$ADMIN_PASSWORD"
BASE=http://127.0.0.1:8080
PG="docker exec deploy-postgres-1 psql -q -U blockchain -d blockchain_platform -tAc"
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=${EV:-$P/acceptance-evidence/dual-system-v1/sprint7/b2-four-semantics-$TS}
mkdir -p "$EV"
exec > >(tee -a "$EV/run.log") 2>&1
PASS=0; FAIL=0
J1=$(mktemp); trap 'rm -f "$J1"' EXIT
check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }
jget(){ python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" 2>/dev/null; }
echo "==== B2 four-semantics v3 start $(date -Is) (API=$(sha256sum $P/backend/bin/api | cut -c1-16)) ===="
echo "EV=$EV"
L1=$(curl -s -c "$J1" -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"identifier\":\"$USER_EMAIL\",\"password\":\"$USER_PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
check SETUP "受控 fixture 账号登录成功" "$(echo "$L1" | jget 'd["success"]')" "True"
CSRF1=$(grep cs_user_session_csrf "$J1" | awk '{print $7}')
check SETUP "拿到 CSRF token" "$([ -n "$CSRF1" ] && echo yes || echo no)" "yes"

ID=be026ec9-2ea8-498d-8c68-ba7328131ff8
REFF=$($PG "SET ROLE blockchain_app; select object_ref from exports where id='$ID'")
LED_BYTES=$($PG "SET ROLE blockchain_app; select bytes from export_object_migrations where export_id='$ID'")
LED_SHA=$($PG "SET ROLE blockchain_app; select destination_sha256 from export_object_migrations where export_id='$ID'")
PSHA=$($PG "SET ROLE blockchain_app; select sha256 from exports where id='$ID'")
echo "fixture=$ID ref=$REFF bytes=$LED_BYTES"

echo "== 语义② 既有双副本对象：export 优先命中（物理桶证据） =="
DET=$(curl -s -b "$J1" -H "X-Auth-Audience: user" "$BASE/api/v2/exports/$ID"); echo "$DET" > "$EV/detail-export.json"
check B2-EXPORT "v2 详情可下载" "$(echo "$DET" | jget 'd["data"]["download_available"]')" "True"
check B2-EXPORT "object_bucket=真实桶名 blockchain-exports" "$(echo "$DET" | jget 'd["data"]["object_bucket"]')" "blockchain-exports"
check B2-EXPORT "object_bucket_role=export" "$(echo "$DET" | jget 'd["data"]["object_bucket_role"]')" "export"
check B2-EXPORT "MinIO raw 桶存在（迁移不删源）" "$($P/backend/bin/objectprobe -action=stat -bucket=blockchain-raw -key="$REFF" 2>&1 | grep -o 'exists=[a-z]*' | head -1)" "exists=true"
check B2-EXPORT "MinIO export 桶存在（已迁移）" "$($P/backend/bin/objectprobe -action=stat -bucket=blockchain-exports -key="$REFF" 2>&1 | grep -o 'exists=[a-z]*' | head -1)" "exists=true"
CODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o "$EV/full.bin" -D "$EV/full.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$ID/download")
check B2-EXPORT "全量下载 200" "$CODE" "200"
check B2-EXPORT "字节数 == 账本 bytes" "$(stat -c%s "$EV/full.bin")" "$LED_BYTES"
DSHA=$(sha256sum "$EV/full.bin" | cut -d' ' -f1)
check B2-EXPORT "SHA256 == destination_sha256" "$DSHA" "$LED_SHA"
check B2-EXPORT "SHA256 == exports.sha256" "$DSHA" "$PSHA"
check B2-EXPORT "Content-Type=text/csv" "$(grep -i '^content-type' "$EV/full.hdr" | tr -d "\r" | awk '{print $2}')" "text/csv"
RCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -H "Range: bytes=100-199" -o "$EV/range.bin" -D "$EV/range.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$ID/download")
check B2-EXPORT "Range 206" "$RCODE" "206"
check B2-EXPORT "Content-Range 正确" "$(grep -i '^content-range' "$EV/range.hdr" | tr -d "\r" | sed 's/^[Cc]ontent-[Rr]ange: //')" "bytes 100-199/$LED_BYTES"
dd if="$EV/full.bin" of="$EV/full-slice.bin" bs=1 skip=100 count=100 status=none
check B2-EXPORT "Range 内容 == 全量切片" "$(sha256sum "$EV/range.bin" | cut -d' ' -f1)" "$(sha256sum "$EV/full-slice.bin" | cut -d' ' -f1)"
HCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -I -o "$EV/head.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$ID/download")
check B2-EXPORT "HEAD 200" "$HCODE" "200"
check B2-EXPORT "HEAD Content-Length == 账本 bytes" "$(grep -i '^content-length' "$EV/head.hdr" | tr -d "\r" | awk '{print $2}')" "$LED_BYTES"

echo "== D-R10V9-01 直接路径成功审计（全量/Range/HEAD 三条路径都要有） =="
sleep 1
PATHS=$($PG "SET ROLE blockchain_app; select string_agg(distinct detail->>'path', ',' order by detail->>'path') from audit_events_v2 where action='export.download' and resource_id='$ID' and occurred_at > now() - interval '3 minutes'")
check B2-AUDIT "直接路径写入 3 条成功审计路径(full,head,range)" "$PATHS" "full,head,range"
BUCKETS=$($PG "SET ROLE blockchain_app; select string_agg(distinct detail->>'object_bucket', ',') from audit_events_v2 where action='export.download' and resource_id='$ID' and occurred_at > now() - interval '3 minutes'")
ROLES=$($PG "SET ROLE blockchain_app; select string_agg(distinct detail->>'object_bucket_role', ',') from audit_events_v2 where action='export.download' and resource_id='$ID' and occurred_at > now() - interval '3 minutes'")
check B2-AUDIT "审计 object_bucket 全为真实桶名" "$BUCKETS" "blockchain-exports"
check B2-AUDIT "审计 object_bucket_role 全为 export" "$ROLES" "export"
ROW=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'result'::text,'-') from audit_events_v2 limit 0" 2>/dev/null)
FULLROW=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-')||'|'||coalesce(detail->>'object_ref','-')||'|'||coalesce(request_id,'-')||'|'||coalesce(trace_id,'-')||'|'||coalesce(result,'-') from audit_events_v2 where action='export.download' and resource_id='$ID' and detail->>'path'='full' and occurred_at > now() - interval '3 minutes' order by seq desc limit 1")
FB=$(echo "$FULLROW" | cut -d'|' -f1); FR=$(echo "$FULLROW" | cut -d'|' -f2); FQ=$(echo "$FULLROW" | cut -d'|' -f3); FT=$(echo "$FULLROW" | cut -d'|' -f4); FRS=$(echo "$FULLROW" | cut -d'|' -f5)
check B2-AUDIT "全量审计 bytes == 对象大小" "$FB" "$LED_BYTES"
check B2-AUDIT "全量审计 object_ref 正确" "$FR" "$REFF"
check B2-AUDIT "全量审计 request_id 非空" "$([ -n "$FQ" ] && [ "$FQ" != "-" ] && echo yes || echo no)" "yes"
check B2-AUDIT "全量审计 trace_id 非空" "$([ -n "$FT" ] && [ "$FT" != "-" ] && echo yes || echo no)" "yes"
check B2-AUDIT "全量审计 result=success" "$FRS" "success"
echo "$FULLROW" > "$EV/audit-full-row.txt"

echo "== 语义③ 两桶皆无：受控测试记录 -> 503 + 审计（记录永久保留） =="
MREF="exports/acceptance-b2-missing-$RANDOM$RANDOM$RANDOM.csv"
MID=$(python3 -c "import uuid;print(uuid.uuid4())")
$PG "SET ROLE blockchain_app; insert into exports (id, dataset, format, status, object_ref, rows, created_by, organization_id, workspace_id, sha256) values ('$MID','acceptance-b2-missing-fixture','csv','COMPLETED','$MREF',0,null,'d3b152f2-44c0-441c-b047-be24c137a84b','000e31bb-8c48-435d-af97-95bbc75a496c','')" >/dev/null 2>&1
check B2-MISSING "受控测试记录已创建（保留）" "$($PG "SET ROLE blockchain_app; select count(*) from exports where id='$MID'")" "1"
echo "controlled_export_id=$MID ref=$MREF" | tee "$EV/controlled-missing-record.txt"
check B2-MISSING "MinIO raw 桶无该 ref" "$($P/backend/bin/objectprobe -action=stat -bucket=blockchain-raw -key="$MREF" 2>&1 | grep -o 'exists=[a-z]*' | head -1)" "exists=false"
check B2-MISSING "MinIO export 桶无该 ref" "$($P/backend/bin/objectprobe -action=stat -bucket=blockchain-exports -key="$MREF" 2>&1 | grep -o 'exists=[a-z]*' | head -1)" "exists=false"
MDET=$(curl -s -b "$J1" -H "X-Auth-Audience: user" "$BASE/api/v2/exports/$MID"); echo "$MDET" > "$EV/detail-missing.json"
check B2-MISSING "v2 详情 object_missing=true" "$(echo "$MDET" | jget 'd["data"]["object_missing"]')" "True"
check B2-MISSING "v2 详情 download_available=false" "$(echo "$MDET" | jget 'd["data"]["download_available"]')" "False"
MCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o "$EV/missing.json" -w "%{http_code}" "$BASE/api/v1/exports/$MID/download")
check B2-MISSING "全量下载 503" "$MCODE" "503"
check B2-MISSING "错误码 DATA_OBJECT_MISSING" "$(jget 'd["error"]["code"]' < "$EV/missing.json")" "DATA_OBJECT_MISSING"
check B2-MISSING "Range 路径同样 503" "$(curl -s -b "$J1" -H "X-Auth-Audience: user" -H "Range: bytes=0-99" -o /dev/null -w "%{http_code}" "$BASE/api/v1/exports/$MID/download")" "503"
check B2-MISSING "HEAD 路径同样 503" "$(curl -s -b "$J1" -H "X-Auth-Audience: user" -I -o /dev/null -w "%{http_code}" "$BASE/api/v1/exports/$MID/download")" "503"
check B2-MISSING "无成功审计（不得虚报）" "$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$MID'")" "0"
sleep 1
MAUD=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download_failed' and resource_id='$MID'")
check B2-MISSING "export.download_failed 已记录" "$([ "${MAUD:-0}" -ge 1 ] && echo yes || echo no)" "yes"
check B2-MISSING "审计 reason/action 明确" "$($PG "SET ROLE blockchain_app; select coalesce(detail->>'reason','-')||'|'||coalesce(detail->>'action','-') from audit_events_v2 where action='export.download_failed' and resource_id='$MID' order by seq desc limit 1")" "object_missing|notify_admin"

echo "== 授权：跨工作区 fail closed =="
XID=59652df7-e90c-49a3-a6d2-09c470817e7d
XWS=$($PG "SET ROLE blockchain_app; select workspace_id from exports where id='$XID'")
echo "cross_ws=$XWS"
check B2-AUTHZ "跨工作区下载 404" "$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o "$EV/cross-ws.json" -w "%{http_code}" "$BASE/api/v1/exports/$XID/download")" "404"
check B2-AUTHZ "错误码 RESOURCE_NOT_FOUND" "$(jget 'd["error"]["code"]' < "$EV/cross-ws.json")" "RESOURCE_NOT_FOUND"
check B2-AUTHZ "跨工作区详情 404" "$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o /dev/null -w "%{http_code}" "$BASE/api/v2/exports/$XID")" "404"
sleep 1
check B2-AUTHZ "越权尝试写 authorization.object_denied" "$([ "$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='authorization.object_denied' and resource_id='$XID'")" -ge 1 ] && echo yes || echo no)" "yes"
check B2-AUTHZ "匿名下载 401" "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/v1/exports/$ID/download")" "401"
check B2-AUTHZ "匿名票据 401" "$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/v2/exports/$ID/download-ticket")" "401"
echo "==== B2 part A: PASS=$PASS FAIL=$FAIL ===="
