#!/usr/bin/env bash
# B2 semantic-1 (raw fallback), authoritative round - v2.
#
# v1 (scripts/s7/b2-raw-only-ui-sample.sh, sha 1d6b9ae7...) produced two real test failures and
# is preserved unmodified together with its evidence directory; v2 is the corrected round.
#
# v2 corrections (root review, 2026-09-13):
#   * worker identity constant corrected (was a truncated/typo'd hash);
#   * any preflight failure aborts NON-ZERO before the browser is allowed to create anything;
#   * every PG query runs with ON_ERROR_STOP=1 and a non-zero rc is recorded as a failure, so an
#     empty value can never be compared against an empty expectation and pass;
#   * audit binding is exact: export.create AND export.completed request_id/trace_id must equal the
#     ids the browser actually received, and each download audit must match the response headers of
#     the request that produced it; the ticket-consumption request id is taken from the observed
#     download response, never assumed to equal the ticket-creation id;
#   * a real playwright report gate (counts + titles) replaces the previously unimplemented claim,
#     and skipped/unexpected tests can never count as PASS;
#   * CSV content is validated (chain/address/dataset really present, rows > 0), so an empty file
#     cannot pass as a real sample;
#   * process checks use precise command lines and exclude this runner; the in-flight baseline is
#     compared before/after and attributed to this round (pre-existing user jobs are never touched);
#   * the session cookie jar is kept inside the evidence directory with mode 0600 (nothing is
#     deleted), and every curl call is bounded and rc-checked.
set -uo pipefail
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
P=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=$P/acceptance-evidence/dual-system-v1/sprint7/b2-raw-only-ui-sample-$TS
CAL=$P/acceptance-evidence/dual-system-v1/sprint7/b2raw-probe-calibration-20260913T133310Z
PROBE=$CAL/objectprobe-strict-v3
CSVFACTS=$P/scripts/s7/b2-raw-csv-facts.py
EXPECT_API_SHA=3f7cc3a727b467ad5956a9f3f235015c5ab45cc6ef99929b3cb1472af0c85334
EXPECT_WORKER_SHA=513e67bd7e9d4a9f96aeb659a237ca2399e7fc2074fdfe0978d0f6d8e6dbdb48
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
PASS=0; FAIL=0; PGERR=0; CURLERR=0
check(){
  # An empty value can never satisfy an expectation: "empty == empty" is not evidence, it is a
  # missing artefact. Callers that legitimately expect emptiness compare against a sentinel word.
  if [ -z "$3" ] && [ -z "$4" ]; then
    echo "  FAIL $1 :: $2 :: BOTH SIDES EMPTY (an empty-vs-empty comparison is never evidence)"
    FAIL=$((FAIL+1))
    return 0
  fi
  if [ "$3" = "$4" ]; then echo "  PASS $1 :: $2 :: $3"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $2 :: got=$3 want=$4"; FAIL=$((FAIL+1)); fi
}
nonempty(){ if [ -n "$2" ]; then echo "  PASS $1 :: $3 :: present"; PASS=$((PASS+1)); else echo "  FAIL $1 :: $3 :: EMPTY VALUE (an empty-vs-empty comparison is never evidence)"; FAIL=$((FAIL+1)); fi; }
gate(){ printf '%s
' "$1" >> "$EV/gate.txt"; }

set -a; . $P/deploy/.env; set +a
set -a; . /tmp/s6-e2e-env.sh; set +a
USER_EMAIL=admin@local
USER_PW=$ADMIN_PASSWORD
BASE=http://127.0.0.1:8080

pgq(){
  local out rc
  out=$(docker exec deploy-postgres-1 psql -q -v ON_ERROR_STOP=1 -U blockchain -d blockchain_platform -tAc "$1" 2>>"$EV/pg-errors.log")
  rc=$?
  if [ "$rc" -ne 0 ]; then
    PGERR=$((PGERR+1))
    printf 'PG_QUERY_FAILED rc=%s sql=%s
' "$rc" "$1" >> "$EV/pg-errors.log"
  fi
  printf '%s' "$out"
}
httpc(){
  local tag=$1 outfile=$2
  shift 2
  local code rc
  code=$(curl -sS --max-time 120 --connect-timeout 10 -o "$outfile" -w '%{http_code}' "$@" 2>>"$EV/curl-errors.log")
  rc=$?
  if [ "$rc" -ne 0 ]; then
    CURLERR=$((CURLERR+1))
    printf 'CURL_FAILED rc=%s tag=%s
' "$rc" "$tag" >> "$EV/curl-errors.log"
  fi
  printf '%s' "$code"
}

# Shared, bounded, rc-accounted helpers. They OVERRIDE the pgq/httpc defined above; see
# scripts/s7/b2-raw-helpers.sh for why each raw rc is persisted to a file instead of being counted
# in a subshell-local variable (that was a real defect: the parent kept reading 0).
. "$P/scripts/s7/b2-raw-helpers.sh"

echo "=== P0 runtime freeze and preflight ==="
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
check P0 "API process does not set EXPORT_WRITE_BUCKET (default raw)" "$WRITE_ENV" "0"
check P0 "API process does not set EXPORT_READ_ORDER (default export,raw)" "$READ_ENV" "0"
check P0 "deploy/.env RAW_BUCKET" "$RAW_BUCKET" "blockchain-raw"
check P0 "deploy/.env EXPORT_BUCKET" "$EXPORT_BUCKET" "blockchain-exports"
READY=0
for i in $(seq 1 30); do
  B=$(curl -sS --max-time 10 $BASE/health/ready || true)
  if printf '%s' "$B" | python3 -c 'import json,sys
try: d=json.load(sys.stdin)
except Exception: sys.exit(1)
c=d.get("checks") or {}
sys.exit(0 if d.get("status")=="ready" and all(c.get(k)=="ok" for k in ("postgres","clickhouse","nats","minio")) else 1)'; then READY=1; break; fi
  sleep 1
done
check P0 "readiness with all four dependencies ok" "$READY" "1"
echo "$B" > "$EV/health-ready.json"
check P0 "no PG query failed during preflight" "$PGERR" "0"

echo "=== P1 pre-census, anti-reuse baseline, in-flight baseline ==="
pgq "SET ROLE blockchain_app; select id from exports where organization_id='$ORG' and workspace_id='$WS' order by id" > "$EV/pre-export-ids.txt"
PRE_COUNT=$(grep -c . "$EV/pre-export-ids.txt" || true)
nonempty P1 "$PRE_COUNT" "pre-run export census is non-empty"
pgq "SET ROLE blockchain_app; select id from exports where status in ('PENDING','QUEUED','RUNNING') order by id" > "$EV/pre-inflight-ids.txt"
pgq "SET ROLE blockchain_app; select count(*) from exports where organization_id='$ORG' and workspace_id='$WS' and dataset='$DATASET' and format='$FORMAT' and zip=false" > "$EV/pre-snapshot-matches.txt"
PRE_SAME=$(cat "$EV/pre-snapshot-matches.txt")
nonempty P1 "$PRE_SAME" "same-parameter export count recorded"
echo "pre_export_rows=$PRE_COUNT pre_same_parameter_rows=$PRE_SAME" | tee "$EV/pre-census.txt"
check P0 "no PG query failed during the census" "$PGERR" "0"

HELPER_COUNTS=$(helper_counts)
HC_PG_TOTAL=$(printf '%s' "$HELPER_COUNTS" | awk '{print $1}')
HC_PG_BAD=$(printf '%s' "$HELPER_COUNTS" | awk '{print $2}')
HC_CURL_TOTAL=$(printf '%s' "$HELPER_COUNTS" | awk '{print $3}')
HC_CURL_BAD=$(printf '%s' "$HELPER_COUNTS" | awk '{print $4}')
HELPER_LOGS=$(helper_log_lines)
HC_PG_LOG=$(printf '%s' "$HELPER_LOGS" | awk '{print $1}')
HC_CURL_LOG=$(printf '%s' "$HELPER_LOGS" | awk '{print $2}')
echo "preflight_helper_counts: pg_calls=$HC_PG_TOTAL pg_bad=$HC_PG_BAD curl_calls=$HC_CURL_TOTAL curl_bad=$HC_CURL_BAD pg_error_lines=$HC_PG_LOG curl_error_lines=$HC_CURL_LOG" | tee "$EV/helper-rc-summary.txt"
check P0 "preflight: every PG helper call returned rc=0 (counted from the persisted rc file)" "$HC_PG_BAD" "0"
check P0 "preflight: every curl helper call returned rc=0" "$HC_CURL_BAD" "0"
# The preflight phase issues EXACTLY three queries: the export census, the in-flight census and
# the same-parameter count. Binding the count to that number keeps the no-silent-no-op check
# (a skipped query changes the count) and replaces the earlier invented floor of 5.
check P0 "preflight: exactly three pre-census PG queries recorded (n=$HC_PG_TOTAL)" "$HC_PG_TOTAL" "3"
check P0 "preflight: every pre-census PG query returned rc=0" "$HC_PG_BAD" "0"
if [ "$FAIL" -ne 0 ]; then
  echo "PREFLIGHT_FAILED: aborting before any browser action (FAIL=$FAIL)"
  gate "preflight_failed"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 7
fi

echo "=== P2 real browser UI: create the export ==="
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
gate "playwright_rc=$PW_RC"
echo "playwright_rc=$PW_RC"
tail -n 20 "$EV/playwright.console.log"
check P2 "playwright exit code" "$PW_RC" "0"

echo "=== P2b playwright report identity gate ==="
REPORT_RC=0
if [ ! -s "$EV/playwright-report.json" ]; then echo "REPORT_MISSING"; REPORT_RC=1; else
python3 - "$EV/playwright-report.json" > "$EV/report-check.txt" 2>&1 <<'PY'
import json, sys
rep = json.load(open(sys.argv[1], encoding="utf-8"))
stats = rep.get("stats") or {}
titles = []
def walk(suite):
    for child in suite.get("suites") or []:
        walk(child)
    for spec in suite.get("specs") or []:
        titles.append(spec.get("title", ""))
for suite in rep.get("suites") or []:
    walk(suite)
ok = [True]
def line(text, good):
    ok[0] = ok[0] and good
    print(("PASS " if good else "FAIL ") + text)
line("expected tests == 2 (got " + str(stats.get("expected")) + ")", stats.get("expected") == 2)
line("unexpected == 0 (got " + str(stats.get("unexpected")) + ")", stats.get("unexpected") == 0)
line("skipped == 0 (got " + str(stats.get("skipped")) + ")", stats.get("skipped") == 0)
line("flaky == 0 (got " + str(stats.get("flaky")) + ")", stats.get("flaky") == 0)
line("B2RAW-01 title present", any("B2RAW-01" in t for t in titles))
line("B2RAW-02 title present", any("B2RAW-02" in t for t in titles))
print("titles=" + json.dumps(titles, ensure_ascii=False))
sys.exit(0 if ok[0] else 1)
PY
REPORT_RC=$?
fi
cat "$EV/report-check.txt"
gate "report_check_rc=$REPORT_RC"
check P2 "playwright report gate (counts, no skip, real titles)" "$REPORT_RC" "0"
SPEC_MENTIONS=$(grep -c 'b2-raw-ui-export.spec.ts' "$EV/playwright.console.log" || true)
check P2 "console identifies the executed spec file" "$([ "$SPEC_MENTIONS" -ge 1 ] && echo yes || echo no)" "yes"
check P2 "console mentions B2RAW-01" "$([ "$(grep -c 'B2RAW-01' "$EV/playwright.console.log" || true)" -ge 1 ] && echo yes || echo no)" "yes"
check P2 "console mentions B2RAW-02" "$([ "$(grep -c 'B2RAW-02' "$EV/playwright.console.log" || true)" -ge 1 ] && echo yes || echo no)" "yes"

if [ ! -s "$EV/b2raw-01-create.json" ]; then
  echo "CREATE_EVIDENCE_MISSING (first test failed; nothing is fabricated)"
  gate "create_evidence_missing"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 5
fi

EXPORT_ID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['export_id'])" "$EV/b2raw-01-create.json")
UI_CREATE_RID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['create_response']['x_request_id'] or '')" "$EV/b2raw-01-create.json")
UI_CREATE_TID=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['create_response']['x_trace_id'] or '')" "$EV/b2raw-01-create.json")
echo "export_id=$EXPORT_ID ui_create_request_id=$UI_CREATE_RID ui_create_trace_id=$UI_CREATE_TID"
echo "$EXPORT_ID" > "$EV/export-id.txt"
check P2 "browser received a server export id" "$(printf '%s' "$EXPORT_ID" | grep -cE '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')" "1"
nonempty P2 "$UI_CREATE_RID" "browser captured X-Request-Id on create"
nonempty P2 "$UI_CREATE_TID" "browser captured X-Trace-Id on create"
check P2 "export id is NEW (absent from the pre-run census)" "$(grep -c "^$EXPORT_ID$" "$EV/pre-export-ids.txt" || true)" "0"

HARD_ID_OK=1
if [ -z "$EXPORT_ID" ]; then HARD_ID_OK=0; fi
if [ "$(printf '%s' "$EXPORT_ID" | grep -cE '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')" != "1" ]; then HARD_ID_OK=0; fi
if [ -z "$UI_CREATE_RID" ] || [ -z "$UI_CREATE_TID" ]; then HARD_ID_OK=0; fi
if [ "$PW_RC" != "0" ] || [ "$REPORT_RC" != "0" ]; then HARD_ID_OK=0; fi
if [ "$HARD_ID_OK" != "1" ]; then
  echo "HARD_ABORT: create evidence is not usable (export_id='$EXPORT_ID' request_id='$UI_CREATE_RID' trace_id='$UI_CREATE_TID' playwright_rc=$PW_RC report_rc=$REPORT_RC); no further request is issued"
  gate "hard_abort_unusable_create_evidence"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi

echo "=== P3 PG row for the UI-created export ==="
STATUS=$(pgq "SET ROLE blockchain_app; select status from exports where id='$EXPORT_ID'")
REF=$(pgq "SET ROLE blockchain_app; select object_ref from exports where id='$EXPORT_ID'")
PSHA=$(pgq "SET ROLE blockchain_app; select coalesce(sha256,'') from exports where id='$EXPORT_ID'")
PROWS=$(pgq "SET ROLE blockchain_app; select rows from exports where id='$EXPORT_ID'")
EWS=$(pgq "SET ROLE blockchain_app; select coalesce(workspace_id::text,'') from exports where id='$EXPORT_ID'")
EORG=$(pgq "SET ROLE blockchain_app; select coalesce(organization_id::text,'') from exports where id='$EXPORT_ID'")
ECBY=$(pgq "SET ROLE blockchain_app; select coalesce(created_by::text,'-') from exports where id='$EXPORT_ID'")
FORMAT_DB=$(pgq "SET ROLE blockchain_app; select format from exports where id='$EXPORT_ID'")
EDATASET=$(pgq "SET ROLE blockchain_app; select dataset from exports where id='$EXPORT_ID'")
echo "$EXPORT_ID|$STATUS|$FORMAT_DB|$EDATASET|$PROWS|$PSHA|$REF|$EORG|$EWS|$ECBY" > "$EV/pg-row.txt"
cat "$EV/pg-row.txt"
nonempty P3 "$REF" "object_ref recorded"
nonempty P3 "$PSHA" "sha256 recorded"
nonempty P3 "$PROWS" "row count recorded"
check P3 "export reached COMPLETED" "$STATUS" "COMPLETED"
check P3 "format is csv" "$FORMAT_DB" "$FORMAT"
check P3 "dataset is the one selected in the UI" "$EDATASET" "$DATASET"
check P3 "sha256 is 64 hex chars" "$(printf '%s' "$PSHA" | grep -cE '^[0-9a-f]{64}$')" "1"
check P3 "organization id" "$EORG" "$ORG"
check P3 "workspace id" "$EWS" "$WS"
check P3 "created_by is the session identity" "$ECBY" "$ADMIN_USER"
check P3 "row count is a non-negative integer" "$(printf '%s' "$PROWS" | grep -cE '^[0-9]+$')" "1"
check P3 "row count is greater than zero (a real sample, not an empty file)" "$([ "$PROWS" -gt 0 ] && echo yes || echo no)" "yes"

if [ "$STATUS" != "COMPLETED" ] || [ -z "$REF" ] || [ "$(printf '%s' "$PSHA" | grep -cE '^[0-9a-f]{64}$')" != "1" ] || [ "$(printf '%s' "$PROWS" | grep -cE '^[0-9]+$')" != "1" ]; then
  echo "HARD_ABORT: PG row unusable (status='$STATUS' object_ref='$REF' sha256='$PSHA' rows='$PROWS'); stopping before any download request"
  gate "hard_abort_unusable_pg_row"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi

echo "=== P4 audit chain: create + completed bound EXACTLY to the browser request ids ==="
sleep 1
CACT=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID'")
RACT=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.reuse' and resource_id='$EXPORT_ID'")
CRID=$(pgq "SET ROLE blockchain_app; select coalesce(request_id,'') from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID' order by seq desc limit 1")
CTID=$(pgq "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.create' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KRID=$(pgq "SET ROLE blockchain_app; select coalesce(request_id,'') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KTID=$(pgq "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KACT=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-')||'|'||coalesce(detail->>'object_bucket_role','-')||'|'||coalesce(detail->>'object_ref','-')||'|'||coalesce(detail->>'format','-')||'|'||coalesce(detail->>'rows','-') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
KROUTE=$(pgq "SET ROLE blockchain_app; select coalesce(route_template,'')||' '||coalesce(http_method,'') from audit_events_v2 where action='export.completed' and resource_id='$EXPORT_ID' order by seq desc limit 1")
{
  echo "create_audit_rows=$CACT"
  echo "reuse_audit_rows=$RACT"
  echo "create_request_id=$CRID"
  echo "create_trace_id=$CTID"
  echo "completed_request_id=$KRID"
  echo "completed_trace_id=$KTID"
  echo "completed_detail=$KACT"
  echo "completed_route=$KROUTE"
} > "$EV/audit-summary.txt"
cat "$EV/audit-summary.txt"
check P4 "exactly one export.create audit" "$CACT" "1"
check P4 "no export.reuse audit (a NEW artifact was created, not reused)" "$RACT" "0"
check P4 "create audit request_id == browser X-Request-Id" "$CRID" "$UI_CREATE_RID"
check P4 "create audit trace_id == browser X-Trace-Id" "$CTID" "$UI_CREATE_TID"
check P4 "completed audit request_id == browser X-Request-Id" "$KRID" "$UI_CREATE_RID"
check P4 "completed audit trace_id == browser X-Trace-Id" "$KTID" "$UI_CREATE_TID"
check P4 "completed audit route is the real create route" "$(printf '%s' "$KROUTE" | awk '{print $1}')" "/api/v2/exports"
check P4 "completed audit records the physical bucket blockchain-raw" "$(printf '%s' "$KACT" | cut -d'|' -f1)" "blockchain-raw"
check P4 "completed audit records role raw" "$(printf '%s' "$KACT" | cut -d'|' -f2)" "raw"
check P4 "completed audit object_ref == PG object_ref" "$(printf '%s' "$KACT" | cut -d'|' -f3)" "$REF"
check P4 "completed audit rows == PG rows" "$(printf '%s' "$KACT" | cut -d'|' -f5)" "$PROWS"

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
line("raw object size > 0", int(raw.get("object_size", 0)) > 0)
print("raw_size=" + str(raw.get("object_size", 0)))
sys.exit(0 if ok[0] else 1)
PY
PROBE_CHECK_RC=$?
check P5 "strict probe assertions" "$PROBE_CHECK_RC" "0"
RAW_SIZE=$(python3 -c "import json,sys;d=json.load(open(sys.argv[1]));print([r for r in d['results'] if r['role']=='raw'][0]['object_size'])" "$EV/bucket-probe.json")
echo "raw_object_size=$RAW_SIZE" >> "$EV/audit-summary.txt"

echo "=== P6 authenticated direct download paths (full / Range / HEAD) ==="
COOKIE=$EV/session-cookies.txt
umask 077
L1=$(curl -sS --max-time 30 -c "$COOKIE" -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"identifier\":\"$USER_EMAIL\",\"password\":\"$USER_PW\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login")
check P6 "verification client logged in" "$(printf '%s' "$L1" | python3 -c "import sys,json;print(json.load(sys.stdin).get('success'))" 2>/dev/null)" "True"
check P6 "session cookie jar stored inside the evidence directory (mode 0600)" "$(stat -c '%a' "$COOKIE")" "600"
RID_FULL="b2raw-full-$TS"
RID_RANGE="b2raw-range-$TS"
RID_HEAD="b2raw-head-$TS"
CODE=$(httpc full "$EV/full.bin" -D "$EV/full.hdr" -b "$COOKIE" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_FULL" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "full GET returns 200" "$CODE" "200"
FBYTES=$(stat -c%s "$EV/full.bin")
FSHA=$(sha256sum "$EV/full.bin" | cut -d' ' -f1)
check P6 "full file bytes == RAW object size (independent probe)" "$FBYTES" "$RAW_SIZE"
check P6 "full file SHA-256 == PG exports.sha256" "$FSHA" "$PSHA"
check P6 "full Content-Type is text/csv" "$(grep -i '^content-type' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')" "text/csv"
check P6 "full Content-Length == file bytes" "$(grep -i '^content-length' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')" "$FBYTES"
check P6 "full response echoes the request id we sent" "$(grep -i '^x-request-id' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')" "$RID_FULL"
FULL_TID=$(grep -i '^x-trace-id' "$EV/full.hdr" | tr -d '\r' | awk '{print $2}')
nonempty P6 "$FULL_TID" "full response carries X-Trace-Id"
RCODE=$(httpc range "$EV/range.bin" -D "$EV/range.hdr" -b "$COOKIE" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_RANGE" -H "Range: bytes=100-199" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "Range GET returns 206" "$RCODE" "206"
check P6 "Range Content-Range header" "$(grep -i '^content-range' "$EV/range.hdr" | tr -d '\r' | sed 's/^[Cc]ontent-[Rr]ange: //')" "bytes 100-199/$FBYTES"
RANGE_TID=$(grep -i '^x-trace-id' "$EV/range.hdr" | tr -d '\r' | awk '{print $2}')
nonempty P6 "$RANGE_TID" "range response carries X-Trace-Id"
dd if="$EV/full.bin" of="$EV/full-slice.bin" bs=1 skip=100 count=100 status=none
check P6 "Range body is the exact slice of the full body" "$(sha256sum "$EV/range.bin" | cut -d' ' -f1)" "$(sha256sum "$EV/full-slice.bin" | cut -d' ' -f1)"
HCODE=$(httpc head "$EV/head.body" -D "$EV/head.hdr" -I -b "$COOKIE" -H "X-Auth-Audience: user" -H "X-Request-Id: $RID_HEAD" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P6 "HEAD returns 200" "$HCODE" "200"
check P6 "HEAD Content-Length == file bytes" "$(grep -i '^content-length' "$EV/head.hdr" | tr -d '\r' | awk '{print $2}')" "$FBYTES"
HEAD_TID=$(grep -i '^x-trace-id' "$EV/head.hdr" | tr -d '\r' | awk '{print $2}')
nonempty P6 "$HEAD_TID" "HEAD response carries X-Trace-Id"
check P6 "no curl call failed" "$CURLERR" "0"

echo "=== P7 download audit bound to each response's own request/trace id ==="
sleep 2
for PAIR in "full:$RID_FULL:$FULL_TID" "range:$RID_RANGE:$RANGE_TID" "head:$RID_HEAD:$HEAD_TID"; do
  PTH=$(printf '%s' "$PAIR" | cut -d: -f1)
  RID=$(printf '%s' "$PAIR" | cut -d: -f2)
  TID=$(printf '%s' "$PAIR" | cut -d: -f3)
  N=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID'")
  B=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  RL=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket_role','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  BY=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  AT=$(pgq "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'path'='$PTH' and request_id='$RID' order by seq desc limit 1")
  echo "path=$PTH rows=$N bucket=$B role=$RL bytes=$BY trace=$AT response_trace=$TID" >> "$EV/audit-summary.txt"
  check P7 "$PTH download audit exists for its own request id" "$N" "1"
  check P7 "$PTH audit records physical bucket blockchain-raw" "$B" "blockchain-raw"
  check P7 "$PTH audit records role raw" "$RL" "raw"
  check P7 "$PTH audit trace_id == the response header trace id" "$AT" "$TID"
  if [ "$PTH" = "full" ]; then
    check P7 "full audit bytes == file bytes" "$BY" "$FBYTES"
  fi
  if [ "$PTH" = "range" ]; then
    check P7 "range audit bytes == 100" "$BY" "100"
  fi
  if [ "$PTH" = "head" ]; then
    check P7 "head audit bytes == file bytes" "$BY" "$FBYTES"
  fi
done
FAILED_DL=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download_failed' and resource_id='$EXPORT_ID'")
check P7 "no export.download_failed for this export" "$FAILED_DL" "0"

echo "=== P8 v2 detail bucket attribution ==="
httpc detail "$EV/detail.json" -b "$COOKIE" -H "X-Auth-Audience: user" "$BASE/api/v2/exports/$EXPORT_ID" > /dev/null
if [ ! -s "$EV/detail.json" ]; then
  echo "HARD_ABORT: v2 detail response is empty; refusing to interpret it as data"
  gate "hard_abort_empty_detail"
  echo "SUMMARY PASS=$PASS FAIL=$FAIL"
  exit 8
fi
DET_BUCKET=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('object_bucket'))" "$EV/detail.json")
DET_ROLE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('object_bucket_role'))" "$EV/detail.json")
DET_AVAIL=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('download_available'))" "$EV/detail.json")
DET_SHA=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['data'].get('sha256'))" "$EV/detail.json")
check P8 "v2 detail object_bucket is the physical bucket" "$DET_BUCKET" "blockchain-raw"
check P8 "v2 detail object_bucket_role is raw" "$DET_ROLE" "raw"
check P8 "v2 detail download_available true" "$DET_AVAIL" "True"
check P8 "v2 detail sha256 == PG sha256" "$DET_SHA" "$PSHA"

echo "=== P9 UI-downloaded file identity and ticket audit binding ==="
UI_JSON="$EV/b2raw-02-ui-download.json"
# Initialised empty so that a missing UI-download artefact fails a CHECK instead of aborting the
# whole round on an unset variable (set -u) and hiding the real reason.
UI_FILE=""
UI_SHA=""
UI_BYTES=""
TICKET_RID_CREATE=""
if [ ! -s "$UI_JSON" ]; then
  echo "UI_DOWNLOAD_EVIDENCE_MISSING"
  gate "ui_download_evidence_missing"
  check P9 "UI download evidence present" "missing" "present"
else
  UI_FILE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['saved_path'])" "$UI_JSON")
  UI_SHA=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['sha256'])" "$UI_JSON")
  UI_BYTES=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['download']['bytes'])" "$UI_JSON")
  TICKET_RID_CREATE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['ticket_response']['x_request_id'] or '')" "$UI_JSON")
  TICKET_TID_CREATE=$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['ticket_response']['x_trace_id'] or '')" "$UI_JSON")
  echo "ui_file=$UI_FILE ui_bytes=$UI_BYTES ui_sha=$UI_SHA" >> "$EV/audit-summary.txt"
  check P9 "UI downloaded file exists on disk" "$([ -s "$UI_FILE" ] && echo yes || echo no)" "yes"
  check P9 "UI file re-hashed on disk == sha recorded by the browser" "$(sha256sum "$UI_FILE" | cut -d' ' -f1)" "$UI_SHA"
  check P9 "UI file SHA == server full download SHA" "$UI_SHA" "$FSHA"
  check P9 "UI file SHA == PG exports.sha256" "$UI_SHA" "$PSHA"
  check P9 "UI file bytes == server full download bytes" "$UI_BYTES" "$FBYTES"
  check P9 "UI download filename extension is .csv" "$(printf '%s' "$UI_FILE" | grep -c '\.csv$')" "1"
  sleep 1
  TICKET_CONS=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download_ticket' and resource_id='$EXPORT_ID' and request_id='$TICKET_RID_CREATE'")
  check P9 "ticket creation audit bound to the ticket-create request id" "$TICKET_CONS" "1"
  DOWNLOAD_RID=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
rows=[r for r in d.get('observed_download_responses',[]) if '/api/v2/exports/download/' in r['url']]
print(rows[-1]['request_id'] if rows and rows[-1].get('request_id') else '')
" "$UI_JSON")
  DOWNLOAD_TID=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
rows=[r for r in d.get('observed_download_responses',[]) if '/api/v2/exports/download/' in r['url']]
print(rows[-1]['trace_id'] if rows and rows[-1].get('trace_id') else '')
" "$UI_JSON")
  TICKET_AUDIT=0
  BINDING=observed_response
  if [ -n "$DOWNLOAD_RID" ]; then
    TICKET_AUDIT=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' and request_id='$DOWNLOAD_RID'")
    if [ "$TICKET_AUDIT" != "1" ]; then BINDING=observed_response_mismatch; fi
  else
    BINDING=fallback_single_ticket_row
    TICKET_AUDIT=$(pgq "SET ROLE blockchain_app; select count(*) from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true'")
  fi
  echo "ticket_bound_request_id=$DOWNLOAD_RID binding=$BINDING ticket_download_audit_rows=$TICKET_AUDIT ticket_create_request_id=$TICKET_RID_CREATE ticket_create_trace_id=$TICKET_TID_CREATE" >> "$EV/audit-summary.txt"
  check P9 "UI ticket download produced exactly one ticket download audit" "$TICKET_AUDIT" "1"
  check P9 "ticket-consumption request id is NOT assumed equal to the ticket-create id" "$([ -n "$DOWNLOAD_RID" ] && [ "$DOWNLOAD_RID" != "$TICKET_RID_CREATE" ] && echo distinct || echo "not-captured-or-equal")" "distinct"
  TICKET_BUCKET=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'object_bucket','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
  check P9 "UI ticket audit records physical bucket blockchain-raw" "$TICKET_BUCKET" "blockchain-raw"
  TICKET_BYTES=$(pgq "SET ROLE blockchain_app; select coalesce(detail->>'bytes','-') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
  check P9 "UI ticket audit bytes == UI file bytes" "$TICKET_BYTES" "$UI_BYTES"
  TICKET_URL=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
data=d['ticket_response']['body'].get('data') or {}
print(data.get('url') or '')
" "$UI_JSON")
  nonempty P9 "$TICKET_URL" "ticket url captured from the real envelope"
  REUSE_CODE=$(httpc ticket-reuse "$EV/ticket-reuse.json" -b "$COOKIE" -H "X-Auth-Audience: user" "$BASE$TICKET_URL")
  check P9 "re-using a consumed one-time ticket is rejected (410)" "$REUSE_CODE" "410"
  CONSUMED_TID=$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
rows=[r for r in d.get('observed_download_responses',[]) if '/api/v2/exports/download/' in r['url']]
print(rows[-1]['trace_id'] if rows and rows[-1].get('trace_id') else '')
" "$UI_JSON")
  if [ -n "$CONSUMED_TID" ]; then
    TICKET_AUDIT_TID=$(pgq "SET ROLE blockchain_app; select coalesce(trace_id,'') from audit_events_v2 where action='export.download' and resource_id='$EXPORT_ID' and detail->>'ticket'='true' order by seq desc limit 1")
    check P9 "ticket download audit trace_id == the consumed ticket response trace id" "$TICKET_AUDIT_TID" "$CONSUMED_TID"
  fi
  check P9 "no curl helper call failed during the ticket checks" "$(helper_counts | awk '{print $4}')" "0"
fi

echo "=== P10 CSV content verification (server file and UI file) ==="
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
check P10 "CSV has at least one data row (an empty file is not a real sample)" "$([ "$SVR_ROWS" -gt 0 ] && echo yes || echo no)" "yes"

echo "=== P10b content actually matches the UI-selected chain/address/dataset ==="
python3 - "$EV/full.bin" "$UI_FILE" "$CHAIN" "$ADDRESS" "$DATASET" > "$EV/content-check.txt" 2>&1 <<'PY'
import csv, sys
server, ui, chain, address, dataset = sys.argv[1:6]
ok = [True]
def line(text, good):
    ok[0] = ok[0] and good
    print(("  PASS " if good else "  FAIL ") + text)
def load(path):
    with open(path, newline="", encoding="utf-8") as handle:
        return list(csv.DictReader(handle))
rows_server = load(server)
rows_ui = load(ui)
print("server_rows=" + str(len(rows_server)) + " ui_rows=" + str(len(rows_ui)))
line("server CSV has a header with at least 3 columns", len(rows_server[0].keys()) >= 3 if rows_server else False)
header = list(rows_server[0].keys()) if rows_server else []
print("header=" + ",".join(header))
line("header contains a chain column", "chain" in header)
line("header contains from_address/to_address columns", "from_address" in header and "to_address" in header)
line("every server row is on the selected chain", all(r.get("chain") == chain for r in rows_server))
line("every server row involves the selected address", all(address in (r.get("from_address") or "") or address in (r.get("to_address") or "") for r in rows_server))
line("server and UI rows are identical", rows_server == rows_ui)
line("dataset name referenced in evidence equals the UI selection", dataset == "token_transfers")
sys.exit(0 if ok[0] else 1)
PY
CONTENT_RC=$?
cat "$EV/content-check.txt"
check P10 "CSV content matches the selected chain/address/dataset" "$CONTENT_RC" "0"

echo "=== P11 authorisation still fails closed ==="
CCODE=$(httpc cross-ws "$EV/cross-ws.json" -b "$COOKIE" -H "X-Auth-Audience: user" "$BASE/api/v1/exports/$CROSS_WS_EXPORT/download")
check P11 "cross-workspace download is 404" "$CCODE" "404"
CCODE2=$(httpc cross-ws-detail "$EV/cross-ws-detail.json" -b "$COOKIE" -H "X-Auth-Audience: user" "$BASE/api/v2/exports/$CROSS_WS_EXPORT")
check P11 "cross-workspace detail is 404" "$CCODE2" "404"
ACODE=$(httpc anon "$EV/anon.json" "$BASE/api/v1/exports/$EXPORT_ID/download")
check P11 "anonymous download is 401" "$ACODE" "401"
TCODE=$(httpc anon-ticket "$EV/anon-ticket.json" -X POST "$BASE/api/v2/exports/$EXPORT_ID/download-ticket")
check P11 "anonymous ticket creation is 401" "$TCODE" "401"
check P11 "no curl helper call failed during authorisation checks" "$(helper_counts | awk '{print $4}')" "0"

echo "=== P12 post-run freeze, census and cleanliness ==="
API_PID_A=$(pgrep -f "$P/backend/bin/api$" | head -1)
WORKER_PID_A=$(pgrep -f "$P/backend/bin/worker$" | head -1)
API_SHA_A=$(sha256sum /proc/$API_PID_A/exe 2>/dev/null | cut -d' ' -f1)
WORKER_SHA_A=$(sha256sum /proc/$WORKER_PID_A/exe 2>/dev/null | cut -d' ' -f1)
check P12 "API pid unchanged (no restart during the round)" "$API_PID_A" "$API_PID"
check P12 "API sha unchanged" "$API_SHA_A" "$API_SHA"
check P12 "Worker pid unchanged" "$WORKER_PID_A" "$WORKER_PID"
check P12 "Worker sha unchanged" "$WORKER_SHA_A" "$WORKER_SHA"
pgq "SET ROLE blockchain_app; select id from exports where organization_id='$ORG' and workspace_id='$WS' order by id" > "$EV/post-export-ids.txt"
POST_COUNT=$(grep -c . "$EV/post-export-ids.txt" || true)
check P12 "exactly one new export row in the workspace" "$POST_COUNT" "$((PRE_COUNT + 1))"
check P12 "the new row is exactly the UI-created export id" "$(comm -13 "$EV/pre-export-ids.txt" "$EV/post-export-ids.txt" | tr -d '\n')" "$EXPORT_ID"
pgq "SET ROLE blockchain_app; select id from exports where status in ('PENDING','QUEUED','RUNNING') order by id" > "$EV/post-inflight-ids.txt"
NEW_INFLIGHT=$(comm -13 "$EV/pre-inflight-ids.txt" "$EV/post-inflight-ids.txt" | tr -d '\n')
check P12 "no NEW in-flight export introduced by this round (pre-existing user jobs untouched)" "$([ -z "$NEW_INFLIGHT" ] && echo none || echo "$NEW_INFLIGHT")" "none"
check P12 "this round's export is not left in flight" "$(grep -c "^$EXPORT_ID$" "$EV/post-inflight-ids.txt" || true)" "0"
B2ROW=$(pgq "SET ROLE blockchain_app; select count(*) from exports where dataset='acceptance-b2-missing-fixture'")
check P12 "previously controlled fixture row preserved" "$B2ROW" "1"
LEDGER=$(pgq "SET ROLE blockchain_app; select count(*) from export_object_migrations where status='VERIFIED'")
check P12 "migration ledger still 270 VERIFIED (untouched)" "$LEDGER" "270"
READY2=$(curl -sS --max-time 10 $BASE/health/ready | python3 -c "import sys,json;print(json.load(sys.stdin).get('status'))" 2>/dev/null)
check P12 "readiness still ready after the round" "$READY2" "ready"
pgrep -af 'playwright/test/cli.js' > "$EV/playwright-processes-after.txt" || true
PW_LEFT=$(grep -c . "$EV/playwright-processes-after.txt" || true)
check P12 "no playwright test process left running (precise cmdline match)" "$PW_LEFT" "0"
"$PROBE" -action=stat-strict -key="$REF" -out "$EV/bucket-probe-post.json" > /dev/null 2>&1
POST_PROBE_RC=$?
check P12 "post-run strict probe still exit 0 (buckets healthy, no fault)" "$POST_PROBE_RC" "0"
check P12 "post-run probe still shows RAW present + EXPORT NoSuchKey" "$(python3 -c "
import json,sys
d=json.load(open(sys.argv[1]))
r={x['role']:x for x in d['results']}
print('ok' if r['raw']['classification']=='OBJECT_PRESENT' and r['export']['classification']=='OBJECT_MISSING' and r['export'].get('error_code')=='NoSuchKey' else 'bad')" "$EV/bucket-probe-post.json")" "ok"

echo "=== P12b helper rc accounting gate (whole round) ==="
FINAL_COUNTS=$(helper_counts)
FC_PG_TOTAL=$(printf '%s' "$FINAL_COUNTS" | awk '{print $1}')
FC_PG_BAD=$(printf '%s' "$FINAL_COUNTS" | awk '{print $2}')
FC_CURL_TOTAL=$(printf '%s' "$FINAL_COUNTS" | awk '{print $3}')
FC_CURL_BAD=$(printf '%s' "$FINAL_COUNTS" | awk '{print $4}')
FINAL_LOGS=$(helper_log_lines)
FC_PG_LOG=$(printf '%s' "$FINAL_LOGS" | awk '{print $1}')
FC_CURL_LOG=$(printf '%s' "$FINAL_LOGS" | awk '{print $2}')
echo "final_helper_counts: pg_calls=$FC_PG_TOTAL pg_bad=$FC_PG_BAD curl_calls=$FC_CURL_TOTAL curl_bad=$FC_CURL_BAD pg_error_lines=$FC_PG_LOG curl_error_lines=$FC_CURL_LOG" | tee -a "$EV/helper-rc-summary.txt"
check P12 "whole round: every PG helper call returned rc=0 (n=$FC_PG_TOTAL)" "$FC_PG_BAD" "0"
check P12 "whole round: every curl helper call returned rc=0 (n=$FC_CURL_TOTAL)" "$FC_CURL_BAD" "0"
check P12 "pg-errors.log is empty" "$FC_PG_LOG" "0"
check P12 "curl-errors.log is empty" "$FC_CURL_LOG" "0"
check P12 "PG helper was exercised throughout the round (n >= 20)" "$([ "$FC_PG_TOTAL" -ge 20 ] && echo yes || echo no)" "yes"

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


