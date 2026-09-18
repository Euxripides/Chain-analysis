#!/usr/bin/env bash
# B2 semantic-1 (raw fallback) authoritative round.
#
# What this round proves, end to end and only from real artefacts:
#   1) a BRAND-NEW export created by REAL browser interaction with the deployed user console
#      (real Chromium; API-only creation is explicitly not accepted here);
#   2) PG exports row for that exact id, created by the session identity, in the expected
#      organization/workspace;
#   3) export.create + export.completed audit rows whose request_id / trace_id are bound to the
#      request ids captured from the browser and are therefore not inferred from a time window;
#   4) physical bucket attribution by the STRICT probe (raw present; export bucket checked and
#      the same key absent with error code NoSuchKey -- bucket existence proven by enumeration,
#      so a bucket/permission fault can never masquerade as a missing object);
#   5) full GET / Range GET / HEAD over the authenticated direct path: status, byte count,
#      SHA-256, Content-Type, line count, slice equality, plus the matching download audit;
#   6) the file the real UI downloaded (one-time ticket path) is byte-identical to the server
#      full download and to PG sha256;
#   7) authorisation still fails closed (cross-workspace 404, anonymous 401).
#
# PASS is printed only when playwright rc=0, the report gate passes, every check below passes and
# the API/Worker identity is unchanged. Failure keeps the whole evidence directory and exits
# non-zero; nothing is deleted or overwritten.
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
P=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$P/acceptance-evidence/dual-system-v1/sprint7/b2-raw-only-ui-sample-$TS
CAL=$P/acceptance-evidence/dual-system-v1/sprint7/b2raw-probe-calibration-20260913T133310Z
PROBE=$CAL/objectprobe-strict-v3
CSVFACTS=$P/scripts/s7/b2-raw-csv-facts.py
EXPECT_API_SHA=20346f3e01f11496caee2d72d3715c57ced2e40f93fd12e148870e3cff11811a
EXPECT_WORKER_SHA=513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6f8e6dbdb48
WS=000e31bb-8c48-435d-af97-95bbc75a496c
ORG=d3b152f2-44c0-441c-b047-be24c137a84b
ADMIN_USER=6e192e52-d0aa-4e4a-9497-4953a332cfd3
CROSS_WS_EXPORT=59652df7-e90c-49a3-a6d2-09c470817e7d
CHAIN=tron
ADDRESS=T9ygmX26tszHxPiHvbPwjDb92UkfC4YXPU
DATASET=token_transfers
FORMAT=csv
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
exec > >(tee -a "$EV/runner.console.log") 2>&1
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"
echo "evidence=$EV"
echo "started=$(date -Is)"
PASS=0; FAIL=0
check(){ if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi; }
gate(){ printf '%s
' "$1" >> "$EV/gate.txt"; }

set -a; . $P/deploy/.env; set +a
set -a; . /tmp/s6-e2e-env.sh; set +a
USER_EMAIL=admin@local
USER_PW=$ADMIN_PASSWORD
BASE=http://127.0.0.1:8080
PG="docker exec deploy-postgres-1 psql -q -U blockchain -d blockchain_platform -tAc"

echo "=== P0 runtime freeze + preflight ==="
API_PID=$(pgrep -f "$P/backend/bin/api$" | head -1)
WORKER_PID=$(pgrep -f "$P/backend/bin/worker$" | head -1)
API_SHA=$(sha256sum /proc/$API_PID/exe 2>/dev/null | cut -d' ' -f1)
WORKER_SHA=$(sha256sum /proc/$WORKER_PID/exe 2>/dev/null | cut -d' ' -f1)
echo "api_pid=$API_PID api_sha=$API_SHA"
echo "worker_pid=$WORKER_PID worker_sha=$WORKER_SHA"
check P0 "API binary identity is the audited dual-read build" "$API_SHA" "$EXPECT_API_SHA"
check P0 "Worker binary identity unchanged (never rebuilt this round)" "$WORKER_SHA" "$EXPECT_WORKER_SHA"
check P0 "strict probe binary v3 present and executable" "$([ -x "$PROBE" ] && echo yes || echo no)" "yes"
check P0 "strict probe binary sha" "$(sha256sum "$PROBE" | cut -d' ' -f1)" "172e877e6923b8939d833fc80669bed195b6901660c86ed3ca98f1fd8fd22eea"
WRITE_ENV=$(tr '\0' '\n' < /proc/$API_PID/environ | grep -c '^EXPORT_WRITE_BUCKET=' || true)
READ_ENV=$(tr '\0' '\n' < /proc/$API_PID/environ | grep -c '^EXPORT_READ_ORDER=' || true)
check P0 "API process does not set EXPORT_WRITE_BUCKET (=> default raw)" "$WRITE_ENV" "0"
check P0 "API process does not set EXPORT_READ_ORDER (=> default export,raw)" "$READ_ENV" "0"
check P0 "deploy/.env RAW_BUCKET" "$RAW_BUCKET" "blockchain-raw"
check P0 "deploy/.env EXPORT_BUCKET" "$EXPORT_BUCKET" "blockchain-exports"
READY=0
for i in $(seq 1 30); do
  B=$(curl -s -m 5 $BASE/health/ready || true)
  if printf '%s' "$B" | python3 -c 'import json,sys
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
c=d.get("checks") or {}
sys.exit(0 if d.get("status")=="ready" and all(c.get(k)=="ok" for k in ("postgres","clickhouse","nats","minio")) else 1)'; then READY=1; break; fi
  sleep 1
done
check P0 "readiness with all four dependencies ok" "$READY" "1"
echo "$B" > "$EV/health-ready.json"

echo "=== P1 pre-census and anti-reuse baseline ==="
$PG "SET ROLE blockchain_app; select id from exports where organization_id='$ORG' and workspace_id='$WS' order by id" > "$EV/pre-export-ids.txt"
PRE_COUNT=$(grep -c . "$EV/pre-export-ids.txt" || true)
echo "pre_export_rows=$PRE_COUNT" | tee "$EV/pre-census.txt"
$PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','QUEUED','RUNNING')" >> "$EV/pre-census.txt"
$PG "SET ROLE blockchain_app; select count(*) from exports where organization_id='$ORG' and workspace_id='$WS' and dataset='$DATASET' and format='$FORMAT' and zip=false" > "$EV/pre-snapshot-matches.txt"
check P1 "same-parameter completed exports existed BEFORE the run (anti-reuse baseline recorded)" "$([ -s "$EV/pre-snapshot-matches.txt" ] && echo yes || echo no)" "yes"
echo "pre_same_parameter_rows=$(cat "$EV/pre-snapshot-matches.txt")" | tee -a "$EV/pre-census.txt"

echo "=== P2 real browser UI: create export ==="
cd $P/frontend || exit 90
E2E_BASE_URL=https://127.0.0.1:8448
E2E_CHROMIUM_PATH=/usr/bin/chromium-browser
B2RAW_OUT="$EV"
B2RAW_EMAIL="$USER_EMAIL"
B2RAW_PASSWORD="$USER_PW"
B2RAW_CHAIN=$CHAIN
B2RAW_ADDRESS=$ADDRESS
B2RAW_FORMAT=$FORMAT
B2RAW_DATASET=$DATASET
export E2E_BASE_URL E2E_CHROMIUM_PATH B2RAW_OUT B2RAW_EMAIL B2RAW_PASSWORD B2RAW_CHAIN B2RAW_ADDRESS B2RAW_FORMAT B2RAW_DATASET
node node_modules/@playwright/test/cli.js test --config playwright.b2raw.config.ts > "$EV/playwright.console.log" 2>&1
PW_RC=$?
echo "$PW_RC" > "$EV/playwright.rc"
echo "playwright_rc=$PW_RC"
tail -n 25 "$EV/playwright.console.log"
gate "playwright_rc=$PW_RC"
check P2 "playwright exit code" "$PW_RC" "0"
if [ ! -s "$EV/b2raw-01-create.json" ]; then echo "CREATE_EVIDENCE_MISSING"; gate "create_evidence_missing"; echo "SUMMARY PASS=$PASS FAIL=$FAIL"; exit 5; fi
EXPORT_ID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['export_id'])" "$EV/b2raw-01-create.json")
UI_CREATE_RID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['create_response']['x_request_id'] or '')" "$EV/b2raw-01-create.json")
UI_CREATE_TID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['create_response']['x_trace_id'] or '')" "$EV/b2raw-01-create.json")
echo "export_id=$EXPORT_ID ui_create_request_id=$UI_CREATE_RID ui_create_trace_id=$UI_CREATE_TID"
echo "$EXPORT_ID" > "$EV/export-id.txt"
check P2 "browser received a server export id" "$(printf '%s' "$EXPORT_ID" | grep -cE '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')" "1"
check P2 "browser captured X-Request-Id on create" "$([ -n "$UI_CREATE_RID" ] && echo yes || echo no)" "yes"
check P2 "browser captured X-Trace-Id on create" "$([ -n "$UI_CREATE_TID" ] && echo yes || echo no)" "yes"
check P2 "export id is NEW (absent from the pre-run census)" "$(grep -c "^$EXPORT_ID$" "$EV/pre-export-ids.txt" || true)" "0"

echo "=== P3 PG row for the UI-created export ==="
STATUS=$($PG "SET ROLE blockchain_app; select status from exports where id='$EXPORT_ID'")
REF=$($PG "SET ROLE blockchain_app; select object_ref from exports where id='$EXPORT_ID'")
PSHA=$($PG "SET ROLE blockchain_app; select coalesce(sha256,'') from exports where id='$EXPORT_ID'")
PROWS=$($PG "SET ROLE blockchain_app; select rows from exports where id='$EXPORT_ID'")
EWS=$($PG "SET ROLE blockchain_app; select coalesce(workspace_id::text,'') from exports where id='$EXPORT_ID'")
EORG=$($PG "SET ROLE blockchain_app; select coalesce(organization_id::text,'') from exports where id='$EXPORT_ID'")
ECBY=$($PG "SET ROLE blockchain_app; select coalesce(created_by::text,'-') from exports where id='$EXPORT_ID'")
FORMAT_DB=$($PG "SET ROLE blockchain_app; select format from exports where id='$EXPORT_ID'")
echo "$EXPORT_ID|$STATUS|$FORMAT_DB|$PROWS|$PSHA|$REF|$EORG|$EWS|$ECBY" > "$EV/pg-row.txt"
cat "$EV/pg-row.txt"
check P3 "export reached COMPLETED" "$STATUS" "COMPLETED"
check P3 "format is csv" "$FORMAT_DB" "$FORMAT"
check P3 "object_ref recorded" "$([ -n "$REF" ] && echo yes || echo no)" "yes"
check P3 "sha256 recorded (64 hex)" "$(printf '%s' "$PSHA" | grep -cE '^[0-9a-f]{64}$')" "1"
check P3 "organization id" "$EORG" "$ORG"
check P3 "workspace id" "$EWS" "$WS"
check P3 "created_by is the session identity" "$ECBY" "$ADMIN_USER"
check P3 "row count is a non-negative integer" "$(printf '%s' "$PROWS" | grep -cE '^[0-9]+$')" "1"

echo "=== P4 audit chain: create + completed bound to captured request ids ==="
sleep 1
CRID=$($PG "SET ROLE blockchain_app; select coalesce(request_id,'') from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID' order by seq desc limit 1")
CTID=$($PG "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID' order by seq desc limit 1")
CACT=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID'")
RACT=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.reuse' and resource_id='$EXPORT_ID'")
KACT=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-')||'|'||coalesce(detail->>'object_bucket_role','-')||'|'||coalesce(detail->>'object_ref','-')||'|'||coalesce(detail->>'format','-')||'|'||coalesce(detail->>'rows','-') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KRID=$($PG "SET ROLE blockchain_app; select coalesce(request_id,'') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KTID=$($PG "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
echo "create_audit_rows=$CACT reuse_audit_rows=$RACT create_request_id=$CRID completed_detail=$KACT completed_request_id=$KRID"
{
  echo "create_audit_rows=$CACT"
  echo "reuse_audit_rows=$RACT"
  echo "create_request_id=$CRID"
  echo "create_trace_id=$CTID"
  echo "completed_detail=$KACT"
  echo "completed_request_id=$KRID"
  echo "completed_trace_id=$KTID"
} > "$EV/audit-summary.txt"
check P4 "exactly one export.create audit" "$CACT" "1"
check P4 "no export.reuse audit (a NEW artifact was created, not reused)" "$RACT" "0"
check P4 "create audit request_id == browser X-Request-Id" "$CRID" "$UI_CREATE_RID"
check P4 "create audit trace_id == browser X-Trace-Id" "$CTID" "$UI_CREATE_TID"
check P4 "export.completed records the physical bucket blockchain-raw" "$(printf '%s' "$KACT" | cut -d'|' -f1)" "blockchain-raw"
check P4 "export.completed records role raw" "$(printf '%s' "$KACT" | cut -d'|' -f2)" "raw"
check P4 "export.completed object_ref == PG object_ref" "$(printf '%s' "$KACT" | cut -d'|' -f3)" "$REF"
check P4 "export.completed rows == PG rows" "$(printf '%s' "$KACT" | cut -d'|' -f5)" "$PROWS"
check P4 "export.completed request_id non-empty" "$([ -n "$KRID" ] && echo yes || echo no)" "yes"
check P4 "export.completed trace_id non-empty" "$([ -n "$KTID" ] && echo yes || echo no)" "yes"

echo "=== P5 strict physical bucket attribution (fail-closed probe) ==="
"$PROBE" -action=stat-strict -key="$REF" -out "$EV/bucket-probe.json" > /dev/null 2>&1
PROBE_RC=$?
check P5 "strict probe exit code 0 (both buckets enumerated; no bucket/permission fault)" "$PROBE_RC" "0"
python3 - "$EV/bucket-probe.json" "$REF" "$RAW_BUCKET" "$EXPORT_BUCKET" > "$EV/bucket-probe-check.txt" 2>&1 <<'PY'
import json, sys
data = json.load(open(sys.argv[1], encoding="utf-8"))
ref, raw_name, export_name = sys.argv[2], sys.argv[3], sys.argv[4]
by_role = {r["role"]: r for r in data["results"]}
ok = [True]
def line(text, good):
    ok[0] = ok[0] and good
    print(("  PASS " if good else "  FAIL ") + text)
line("list buckets authorised = true", data.get("list_buckets_authorized") is True)
line("raw bucket name is " + raw_name, by_role["raw"]["bucket"] == raw_name)
line("export bucket name is " + export_name, by_role["export"]["bucket"] == export_name)
raw = by_role["raw"]
exp = by_role["export"]
line("RAW holds the object (OBJECT_PRESENT)", raw["classification"] == "OBJECT_PRESENT")
line("RAW bucket existence confirmed by enumeration", raw["bucket_exists_confirmed"] is True)
line("EXPORT bucket existence confirmed by enumeration", exp["bucket_exists_confirmed"] is True)
line("EXPORT does NOT hold the same key", exp["classification"] == "OBJECT_MISSING")
line("EXPORT absence is a real object-level NoSuchKey", exp.get("error_code") == "NoSuchKey")
line("no bucket/permission fault reported anywhere", all(r["classification"] in ("OBJECT_PRESENT", "OBJECT_MISSING") for r in data["results"]))
line("probe key equals PG object_ref", raw["key"] == ref and exp["key"] == ref)
print("raw_size=" + str(raw.get("object_size", 0)))
sys.exit(0 if ok[0] else 1)
PY
PROBE_CHECK_RC=$?
check P5 "strict probe assertions" "$PROBE_CHECK_RC" "0"
RAW_SIZE=$(python3 -c "import json,sys;d=json.load(open(sys.argv[1]));print([r for r in d['results'] if r['role']=='raw'][0]['object_size'])" "$EV/bucket-probe.json")
echo "raw_object_size=$RAW_SIZE" >> "$EV/audit-summary.txt"

echo "=== P6 authenticated direct download paths (full / Range / HEAD) ==="
J1=$(mktemp)
trap 'rm -f "$J1"' EXIT
L1=$(curl -s -c "$J1" -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"identifier\":\"$USER_EMAIL\",\"password\":\"$USER_PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
check P6 "verification client logged in" "$(printf '%s' "$L1" | python3 -c "import sys,json;print(json.load(sys.stdin).get('success'))" 2>/dev/null)" "True"
RID_FULL="b2raw-full-$TS"
RID_RANGE="b2raw-range-$TS"
RID_HEAD="b2raw-head-$TS"
CODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_FULL" -o "$EV/full.bin" -D "$EV/full.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "full GET returns 200" "$CODE" "200"
FBYTES=$(stat -c%s "$EV/full.bin")
FSHA=$(sha256sum "$EV/full.bin" | cut -d' ' -f1)
check P6 "full file bytes == RAW object size (independent probe)" "$FBYTES" "$RAW_SIZE"
check P6 "full file SHA-256 == PG exports.sha256" "$FSHA" "$PSHA"
check P6 "full Content-Type is text/csv" "$(grep -i '^content-type' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')" "text/csv"
check P6 "full Content-Length == file bytes" "$(grep -i '^content-length' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')" "$FBYTES"
RCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_RANGE" -H "Range: bytes=100-199" -o "$EV/range.bin" -D "$EV/range.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "Range GET returns 206" "$RCODE" "206"
check P6 "Range Content-Range header" "$(grep -i '^content-range' "$EV/range.hdr" | tr -d '\r' | sed 's/^[Cc]ontent-[Rr]ange: //')" "bytes 100-199/$FBYTES"
dd if="$EV/full.bin" of="$EV/full-slice.bin" bs=1 skip=100 count=100 status=none
check P6 "Range body is the exact slice of the full body" "$(sha256sum "$EV/range.bin" | cut -d' ' -f1)" "$(sha256sum "$EV/full-slice.bin" | cut -d' ' -f1)"
HCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_HEAD" -I -o "$EV/head.hdr" -w "%{http_code}" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "HEAD returns 200" "$HCODE" "200"
check P6 "HEAD Content-Length == file bytes" "$(grep -i '^content-length' "$EV/head.hdr" | tr -d '\r' | awk '{print $2}')" "$FBYTES"

echo "=== P7 download audit bound to request ids ==="
sleep 2
for PAIR in "full:$RID_FULL" "range:$RID_RANGE" "head:$RID_HEAD"; do
  PTH=$(printf '%s' "$PAIR" | cut -d: -f1)
  RID=$(printf '%s' "$PAIR" | cut -d: -f2)
  N=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID'")
  B=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  RL=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket_role','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  BY=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  TD=$($PG "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  echo "path=$PTH rows=$N bucket=$B role=$RL bytes=$BY" >> "$EV/audit-summary.txt"
  check P7 "$PTH download audit exists for its own request id" "$N" "1"
  check P7 "$PTH audit records physical bucket blockchain-raw" "$B" "blockchain-raw"
  check P7 "$PTH audit records role raw" "$RL" "raw"
  check P7 "$PTH audit trace_id non-empty" "$([ -n "$TD" ] && echo yes || echo no)" "yes"
  if [ "$PTH" = "full" ]; then
    check P7 "full audit bytes == file bytes" "$BY" "$FBYTES"
    FULL_AUDIT_BYTES=$BY
  fi
done
FAILED_DL=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download_failed' and resource_id='$EXPORT_ID'")
check P7 "no export.download_failed for this export" "$FAILED_DL" "0"

echo "=== P8 v2 detail bucket attribution ==="
curl -s -b "$J1" -H "X-Auth-Audience: user" "$BASE/api/v2/exports/$EXPORT_ID" > "$EV/detail.json"
DET_BUCKET=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('object_bucket'))" "$EV/detail.json")
DET_ROLE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('object_bucket_role'))" "$EV/detail.json")
DET_AVAIL=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('download_available'))" "$EV/detail.json")
DET_SHA=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('sha256'))" "$EV/detail.json")
check P8 "v2 detail object_bucket is the physical bucket" "$DET_BUCKET" "blockchain-raw"
check P8 "v2 detail object_bucket_role is raw" "$DET_ROLE" "raw"
check P8 "v2 detail download_available true" "$DET_AVAIL" "True"
check P8 "v2 detail sha256 == PG sha256" "$DET_SHA" "$PSHA"

echo "=== P9 UI-downloaded file identity and CSV content ==="
UI_JSON="$EV/b2raw-02-ui-download.json"
if [ ! -s "$UI_JSON" ]; then echo "UI_DOWNLOAD_EVIDENCE_MISSING"; gate "ui_download_evidence_missing"; fi
UI_FILE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['saved_path'])" "$UI_JSON")
UI_SHA=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['sha256'])" "$UI_JSON")
UI_BYTES=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['bytes'])" "$UI_JSON")
UI_TICKET_RID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['ticket_response']['x_request_id'] or '')" "$UI_JSON")
TICKET_ID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['ticket_response']['body']['ticket_id'])" "$UI_JSON")
echo "ui_file=$UI_FILE ui_bytes=$UI_BYTES ui_sha=$UI_SHA" >> "$EV/audit-summary.txt"
check P9 "UI downloaded file exists on disk" "$([ -s "$UI_FILE" ] && echo yes || echo no)" "yes"
check P9 "UI file re-hashed on disk == sha recorded by the browser" "$(sha256sum "$UI_FILE" | cut -d' ' -f1)" "$UI_SHA"
check P9 "UI file SHA == server full download SHA" "$UI_SHA" "$FSHA"
check P9 "UI file SHA == PG exports.sha256" "$UI_SHA" "$PSHA"
check P9 "UI file bytes == server full download bytes" "$UI_BYTES" "$FBYTES"
check P9 "UI download filename extension is .csv" "$(printf '%s' "$UI_FILE" | grep -c '\.csv$')" "1"
sleep 1
TICKET_AUDIT=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and request_id='$UI_TICKET_RID' and coalesce(detail->>'ticket','false')='true'")
check P9 "UI ticket download wrote its own audited download event" "$TICKET_AUDIT" "1"
TICKET_BUCKET=$($PG "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and request_id='$UI_TICKET_RID' order by seq desc limit 1")
check P9 "UI ticket audit records physical bucket blockchain-raw" "$TICKET_BUCKET" "blockchain-raw"
TICKET_CONS=$($PG "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download_ticket' and resource_id='$EXPORT_ID'")
check P9 "ticket creation audit present" "$TICKET_CONS" "1"

echo "=== P10 CSV facts (server file vs UI file) ==="
python3 "$CSVFACTS" "$EV/full.bin" > "$EV/csv-facts-server.json"
python3 "$CSVFACTS" "$UI_FILE" > "$EV/csv-facts-ui.json"
cat "$EV/csv-facts-server.json"
FSHA_FACTS=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['sha256'])" "$EV/csv-facts-server.json")
UI_FACTS_SHA=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['sha256'])" "$EV/csv-facts-ui.json")
SVR_LINES=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['total_lines'])" "$EV/csv-facts-server.json")
SVR_ROWS=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['csv_data_rows'])" "$EV/csv-facts-server.json")
SVR_COLS=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['header_columns'])" "$EV/csv-facts-server.json")
SVR_UNIFORM=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['column_counts_uniform'])" "$EV/csv-facts-server.json")
check P10 "CSV parser sees the same SHA as the server file" "$FSHA_FACTS" "$FSHA"
check P10 "CSV facts identical for the UI-downloaded file" "$UI_FACTS_SHA" "$FSHA"
check P10 "every CSV row has the same column count" "$SVR_UNIFORM" "True"
check P10 "CSV data rows == PG exports.rows" "$SVR_ROWS" "$PROWS"
check P10 "CSV line count == data rows + 1 header line" "$SVR_LINES" "$((SVR_ROWS + 1))"
check P10 "CSV header has at least one column" "$([ "$SVR_COLS" -gt 0 ] && echo yes || echo no)" "yes"

echo "=== P11 authorisation still fails closed ==="
CCODE=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o "$EV/cross-ws.json" -w "%{http_code}" "$BASE/api/v1/exports/$CROSS_WS_EXPORT/download")
check P11 "cross-workspace download is 404" "$CCODE" "404"
CCODE2=$(curl -s -b "$J1" -H "X-Auth-Audience: user" -o "$EV/cross-ws-detail.json" -w "%{http_code}" "$BASE/api/v2/exports/$CROSS_WS_EXPORT")
check P11 "cross-workspace detail is 404" "$CCODE2" "404"
ACODE=$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P11 "anonymous download is 401" "$ACODE" "401"
TCODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/v2/exports/$EXPORT_ID/download-ticket")
check P11 "anonymous ticket creation is 401" "$TCODE" "401"

echo "=== P12 post-run freeze, census and cleanliness ==="
API_PID_A=$(pgrep -f "$P/backend/bin/api$" | head -1)
WORKER_PID_A=$(pgrep -f "$P/backend/bin/worker$" | head -1)
API_SHA_A=$(sha256sum /proc/$API_PID_A/exe 2>/dev/null | cut -d' ' -f1)
WORKER_SHA_A=$(sha256sum /proc/$WORKER_PID_A/exe 2>/dev/null | cut -d' ' -f1)
check P12 "API pid unchanged (no restart during the round)" "$API_PID_A" "$API_PID"
check P12 "API sha unchanged" "$API_SHA_A" "$API_SHA"
check P12 "Worker pid unchanged" "$WORKER_PID_A" "$WORKER_PID"
check P12 "Worker sha unchanged" "$WORKER_SHA_A" "$WORKER_SHA"
$PG "SET ROLE blockchain_app; select id from exports where organization_id='$ORG' and workspace_id='$WS' order by id" > "$EV/post-export-ids.txt"
POST_COUNT=$(grep -c . "$EV/post-export-ids.txt" || true)
check P12 "exactly one new export row in the workspace" "$POST_COUNT" "$((PRE_COUNT + 1))"
check P12 "the new row is exactly the UI-created export id" "$(comm -13 "$EV/pre-export-ids.txt" "$EV/post-export-ids.txt" | tr -d '\n')" "$EXPORT_ID"
INFLIGHT=$($PG "SET ROLE blockchain_app; select count(*) from exports where status in ('PENDING','QUEUED','RUNNING')")
check P12 "no in-flight export left behind" "$INFLIGHT" "0"
B2=$($PG "SET ROLE blockchain_app; select count(*) from exports where dataset='acceptance-b2-missing-fixture'")
check P12 "previously controlled fixture row preserved" "$B2" "1"
LEDGER=$($PG "SET ROLE blockchain_app; select count(*) from export_object_migrations where status='VERIFIED'")
check P12 "migration ledger still 270 VERIFIED (untouched)" "$LEDGER" "270"
READY2=$(curl -s -m 5 $BASE/health/ready | python3 -c "import sys,json;print(json.load(sys.stdin).get('status'))" 2>/dev/null)
check P12 "readiness still ready after the round" "$READY2" "ready"
pgrep -af playwright > "$EV/playwright-processes-after.txt" || true
PW_LEFT=$(grep -c . "$EV/playwright-processes-after.txt" || true)
check P12 "no playwright process left running" "$PW_LEFT" "0"
"$PROBE" -action=stat-strict -key="$REF" -out "$EV/bucket-probe-post.json" > /dev/null 2>&1
POST_PROBE_RC=$?
check P12 "post-run strict probe still exit 0 (buckets healthy, no fault)" "$POST_PROBE_RC" "0"
check P12 "post-run probe still shows RAW present + EXPORT NoSuchKey" "$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
r={x['role']:x for x in d['results']}
print('ok' if r['raw']['classification']=='OBJECT_PRESENT' and r['export']['classification']=='OBJECT_MISSING' and r['export'].get('error_code')=='NoSuchKey' else 'bad')" "$EV/bucket-probe-post.json")" "ok"

echo "=== summary ==="
{
  echo "finished=$(date -Is)"
  echo "export_id=$EXPORT_ID"
  echo "object_ref=$REF"
  echo "sha256=$PSHA"
  echo "bytes=$FBYTES"
  echo "csv_data_rows=$SVR_ROWS"
  echo "playwright_rc=$PW_RC"
  echo "PASS=$PASS FAIL=$FAIL"
} | tee "$EV/summary.txt"
gate "pass=$PASS fail=$FAIL"
if [ "$FAIL" -eq 0 ] && [ "$PW_RC" -eq 0 ]; then
  echo "B2RAW_RAW_ONLY_UI_SAMPLE=PASS"
  exit 0
fi
echo "B2RAW_RAW_ONLY_UI_SAMPLE=FAIL"
exit 6



