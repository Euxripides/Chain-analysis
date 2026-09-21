#!/usr/bin/env bash
# B3 isolated negative calibration v2 -- assertion-complete and rc-propagating.
#
# WHY v2 EXISTS (preserved defect, calibration side only):
#   The v1 round acceptance-evidence/dual-system-v1/sprint7/b3-isolated-negcal-20260913T143718Z
#   (negcal3.sh sha256 651f2fdb68b5f4c858772c2fc10840c44fd082f7a2f499bef95a83fb933cad58)
#   printed evidence lines but asserted almost nothing, and its C2 probe died with
#       TypeError: object of type 'NoneType' has no len()
#   because the migrator report serialises an empty failure set as JSON null
#   (exportmigrate.Report.Failures is a nil []string). The script had no rc propagation,
#   so that crash was invisible and the round still looked green. A round whose failures
#   cannot produce a nonzero exit is not proof. v1 stays on disk, unmodified, as FAIL.
#
# v2 adds:
#   1. a strict report parser: "failures": null is an EXPLICIT EMPTY failure set; a missing
#      key or a non-list type is a hard SCHEMA_ERROR (never a silent pass);
#   2. named assertions with IDs, an assertion-coverage gate proving every required ID ran
#      exactly once and no unexpected ID ran, and an explicit nonzero exit when ANY
#      assertion fails (no "|| true" is ever placed on a check);
#   3. full isolation: a unique object prefix inside the EXISTING scratch buckets
#      negcal-src / negcal-dst. Production buckets are never read for data, never created,
#      never written, never deleted; nothing is deleted anywhere (scratch objects are kept);
#   4. a --selfcheck mode proving the harness reports FAIL and exits nonzero when an
#      expectation is deliberately violated (the real round must then exit 0).
#
# ASCII-only by policy. Uploaded via sftp_write_text and verified by sha256 on both sides.
set -u
set -o pipefail

SELFCHECK=0
if [ "${1:-}" = "--selfcheck" ]; then SELFCHECK=1; fi

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BASE=/tmp/fr10b-negcal3v2-$STAMP
WC=$BASE
mkdir -p "$WC"
LOG=$WC/round.log
: > "$LOG"
SEEN=$WC/seen.txt
: > "$SEEN"
PASS_N=0
FAIL_N=0
BAD=0

say() { printf '%s\n' "$*" >> "$LOG"; printf '%s\n' "$*"; }
pass() { PASS_N=$((PASS_N + 1)); printf '%s\n' "$1" >> "$SEEN"; }
fail() { FAIL_N=$((FAIL_N + 1)); BAD=1; printf '%s\n' "$1" >> "$SEEN"; say "FAIL $1 :: $2"; }
chk() {
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1" "expected=[$2] actual=[$3]"; fi
}
chkne() {
  if [ "$2" != "$3" ]; then pass "$1"; else fail "$1" "expected not [$3], got [$3]"; fi
}

if [ "$SELFCHECK" = "1" ]; then
  say "== B3 v2 harness self-check (deliberate violation) =="
  chk SC_TRUE same same
  chk SC_FALSE same different
  if [ "$PASS_N" -eq 1 ] && [ "$FAIL_N" -eq 1 ] && [ "$BAD" -eq 1 ]; then
    say "SELFCHECK_TRUE_PASS ok (PASS_N=1)"
    say "SELFCHECK_FALSE_DETECTED ok (FAIL_N=1 BAD=1)"
  else
    say "SELFCHECK_FALSE_NOT_DETECTED PASS_N=$PASS_N FAIL_N=$FAIL_N BAD=$BAD"
    say "SELFCHECK_RESULT=BROKEN"
    exit 8
  fi
  say "SELFCHECK_RESULT=NONZERO_PROPAGATION_CONFIRMED"
  exit 7
fi

P=/home/euripides/projects/blockchain-platform
cd "$P/backend" || { say "FATAL cannot cd backend"; exit 1; }
set -a
. ../deploy/.env
set +a

SRC_BUCKET=negcal-src
DST_BUCKET=negcal-dst
PFX="exports/negcal-v2-$STAMP"
REP="$P/scripts/s7/b3-negcal-v2-rep.py"

say "== B3 v2 round stamp=$STAMP =="
say "WC=$WC"
say "object_prefix=$PFX"
say "scratch_buckets=$SRC_BUCKET,$DST_BUCKET (existing; nothing created or deleted)"
say "api_bin_sha=$(sha256sum bin/api 2>/dev/null | cut -d' ' -f1)"
say "worker_bin_sha=$(sha256sum bin/worker 2>/dev/null | cut -d' ' -f1)"
say "exportmigrate_next_sha=$(sha256sum bin/exportmigrate.next 2>/dev/null | cut -d' ' -f1)"
say "objectprobe_sha=$(sha256sum bin/objectprobe 2>/dev/null | cut -d' ' -f1)"
say "rep_parser_sha=$(sha256sum "$REP" 2>/dev/null | cut -d' ' -f1)"

if [ ! -x ./bin/exportmigrate.next ] || [ ! -x ./bin/objectprobe ] || [ ! -f "$REP" ]; then
  say "FATAL required binary or parser missing"
  exit 1
fi

# ---------------------------------------------------------------- parser controls
printf '%s' '{"failures": null}' > "$WC/p-null.json"
printf '%s' '{"failures": []}' > "$WC/p-empty.json"
printf '%s' '{"failures": ["boom"]}' > "$WC/p-one.json"
printf '%s' '{"total": 1}' > "$WC/p-missing.json"
printf '%s' '{"failures": "abc"}' > "$WC/p-badtype.json"

v=$(python3 "$REP" failures "$WC/p-null.json"); rc=$?
chk P1_null_is_explicit_empty_failure_set "0|0" "$v|$rc"
v=$(python3 "$REP" failures "$WC/p-empty.json"); rc=$?
chk P2_empty_list_is_empty_failure_set "0|0" "$v|$rc"
v=$(python3 "$REP" failures "$WC/p-one.json"); rc=$?
chk P3_nonempty_failure_set_detected "1|0" "$v|$rc"
v=$(python3 "$REP" failures "$WC/p-missing.json" 2>/dev/null); rc=$?
chkne P4_missing_key_is_schema_error "0" "$rc"
v=$(python3 "$REP" failures "$WC/p-badtype.json" 2>/dev/null); rc=$?
chkne P5_wrong_type_is_schema_error "0" "$rc"
REP_TMPDIR="$WC" python3 "$REP" selftest >> "$LOG" 2>&1
chk P6_parser_selftest_all_controls "0" "$?"

# ---------------------------------------------------------------- fixtures
mk() { printf '%s' "$2" > "$WC/$1"; }
mk ok.csv 'alpha-body-001'
mk conflict.csv 'conflict-source-body'
mk same.csv 'identical-body'
mk nosha.csv 'no-pg-sha-body'
mk badsha.csv 'pg-sha-mismatch-body'
mk conflict-dst.csv 'DIFFERENT-TARGET-BODY'
for i in $(seq 1 12); do mk "c$i.csv" "clean-body-$i"; done
sha_file() { sha256sum "$WC/$1" | cut -d' ' -f1; }
sha_obj() {
  ./bin/objectprobe -action backup -bucket "$1" -key "$2" -file "$WC/probe.bin" > /dev/null 2>&1 || { printf 'ERR'; return; }
  sha256sum "$WC/probe.bin" | cut -d' ' -f1
}
exists_obj() { ./bin/objectprobe -bucket "$1" -key "$2" 2>&1 | tail -1; }

printf '[]' > "$WC/items-empty.json"
./bin/exportmigrate.next -mode migrate -items-file "$WC/items-empty.json" -from-bucket "$SRC_BUCKET" -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/pre.jsonl" -report "$WC/pre.json" >> "$LOG" 2>&1
chk S0_scratch_buckets_exist_preflight "0" "$?"
if [ "$BAD" -ne 0 ]; then
  say "ABORT_FAIL_CLOSED: pre-seed assertions (P*/S0) failed; refusing to touch any bucket"
  say "B3V2_PASS=$PASS_N B3V2_FAIL=$FAIL_N"
  say "B3V2_RESULT=FAIL_ABORTED"
  exit 4
fi

for f in ok conflict same nosha badsha; do
  ./bin/objectprobe -action restore -bucket "$SRC_BUCKET" -key "$PFX/$f.csv" -file "$WC/$f.csv" >> "$LOG" 2>&1
done
for i in $(seq 1 12); do
  ./bin/objectprobe -action restore -bucket "$SRC_BUCKET" -key "$PFX/clean/c$i.csv" -file "$WC/c$i.csv" >> "$LOG" 2>&1
done
./bin/objectprobe -action restore -bucket "$DST_BUCKET" -key "$PFX/conflict.csv" -file "$WC/conflict-dst.csv" >> "$LOG" 2>&1
./bin/objectprobe -action restore -bucket "$DST_BUCKET" -key "$PFX/same.csv" -file "$WC/same.csv" >> "$LOG" 2>&1

chk S0A_src_seed_ok "exists=true" "$(exists_obj "$SRC_BUCKET" "$PFX/ok.csv" | cut -d' ' -f1)"
chk S0B_dst_conflict_seeded_different "$(sha_file conflict-dst.csv)" "$(sha_obj "$DST_BUCKET" "$PFX/conflict.csv")"
chk S0C_dst_same_seeded_identical "$(sha_file same.csv)" "$(sha_obj "$DST_BUCKET" "$PFX/same.csv")"

cat > "$WC/items.json" <<EOF
[
 {"export_id":"neg-ok-$STAMP","object_ref":"$PFX/ok.csv","pg_sha256":"$(sha_file ok.csv)"},
 {"export_id":"neg-conflict-$STAMP","object_ref":"$PFX/conflict.csv","pg_sha256":"$(sha_file conflict.csv)"},
 {"export_id":"neg-same-$STAMP","object_ref":"$PFX/same.csv","pg_sha256":"$(sha_file same.csv)"},
 {"export_id":"neg-nosha-$STAMP","object_ref":"$PFX/nosha.csv","pg_sha256":""},
 {"export_id":"neg-badsha-$STAMP","object_ref":"$PFX/badsha.csv","pg_sha256":"0000000000000000000000000000000000000000000000000000000000000000"}
]
EOF
{
  echo '['
  for i in $(seq 1 12); do
    c=$( [ "$i" -lt 12 ] && echo , || echo '' )
    echo " {\"export_id\":\"clean-$STAMP-$i\",\"object_ref\":\"$PFX/clean/c$i.csv\",\"pg_sha256\":\"$(sha_file c$i.csv)\"}$c"
  done
  echo ']'
} > "$WC/clean.json"

RUN() { ./bin/exportmigrate.next "$@" > "$WC/last.out" 2>&1; echo $? > "$WC/last.rc"; }
last_rc() { cat "$WC/last.rc"; }

# ---------------------------------------------------------------- A) dry-run
say "== A) dry-run on NEGATIVE fixture: zero side effects =="
CONFLICT_BEFORE=$(sha_obj "$DST_BUCKET" "$PFX/conflict.csv")
RUN -mode migrate -items-file "$WC/items.json" -from-bucket "$SRC_BUCKET" -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/led.jsonl" -report "$WC/dry.json"
chk A1_dryrun_rc_nonzero "2" "$(last_rc)"
chk A2_dryrun_no_ledger_side_effect "no" "$([ -f "$WC/led.jsonl" ] && echo yes || echo no)"
chk A3_dryrun_by_action "already_verified=1,conflict=1,planned_copy=2,source_pg_sha_mismatch=1" "$(python3 "$REP" map "$WC/dry.json" by_action)"
chk A4_dryrun_by_status "ALREADY_VERIFIED=1,CONFLICT=1,FAILED=1,PENDING=2" "$(python3 "$REP" map "$WC/dry.json" by_status)"
chk A5_dryrun_failures_count "2" "$(python3 "$REP" failures "$WC/dry.json")"
chk A6_dryrun_execute_false "false" "$(python3 "$REP" get "$WC/dry.json" execute)"
chk A7_dryrun_no_dst_ok "exists=false" "$(exists_obj "$DST_BUCKET" "$PFX/ok.csv" | cut -d' ' -f1)"
chk A8_dryrun_no_dst_badsha "exists=false" "$(exists_obj "$DST_BUCKET" "$PFX/badsha.csv" | cut -d' ' -f1)"
chk A9_dryrun_conflict_target_untouched "$CONFLICT_BEFORE" "$(sha_obj "$DST_BUCKET" "$PFX/conflict.csv")"
chk A10_dryrun_src_ok_untouched "$(sha_file ok.csv)" "$(sha_obj "$SRC_BUCKET" "$PFX/ok.csv")"

# ---------------------------------------------------------------- B) execute
say "== B) execute NEGATIVE fixture =="
RUN -mode migrate -items-file "$WC/items.json" -from-bucket "$SRC_BUCKET" -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/led.jsonl" -execute -report "$WC/exec.json"
chk B1_execute_rc_nonzero "2" "$(last_rc)"
chk B2_execute_by_action "already_verified=1,conflict=1,copied=2,source_pg_sha_mismatch=1" "$(python3 "$REP" map "$WC/exec.json" by_action)"
chk B3_execute_by_status "ALREADY_VERIFIED=1,CONFLICT=1,FAILED=1,VERIFIED=2" "$(python3 "$REP" map "$WC/exec.json" by_status)"
chk B4_execute_failures_count "2" "$(python3 "$REP" failures "$WC/exec.json")"
chk B5_conflict_target_unchanged "$CONFLICT_BEFORE" "$(sha_obj "$DST_BUCKET" "$PFX/conflict.csv")"
chk B6_badsha_not_copied "exists=false" "$(exists_obj "$DST_BUCKET" "$PFX/badsha.csv" | cut -d' ' -f1)"
KEPT=""
for f in ok conflict same nosha badsha; do
  r=$(exists_obj "$SRC_BUCKET" "$PFX/$f.csv" | cut -d' ' -f1)
  if [ "$r" = "exists=true" ]; then KEPT="$KEPT $f=kept"; else KEPT="$KEPT $f=LOST"; fi
done
chk B7_all_sources_kept " ok=kept conflict=kept same=kept nosha=kept badsha=kept" "$KEPT"
chk B8_copied_ok_bytes_match_source "$(sha_file ok.csv)" "$(sha_obj "$DST_BUCKET" "$PFX/ok.csv")"
chk B9_copied_nosha_bytes_match_source "$(sha_file nosha.csv)" "$(sha_obj "$DST_BUCKET" "$PFX/nosha.csv")"
chk B10_execute_report_allverified_false "false" "$(python3 "$REP" allverified "$WC/exec.json")"

# ---------------------------------------------------------------- C) resume
say "== C) resume: -limit 5 then full run (clean fixture) =="
RUN -mode migrate -items-file "$WC/clean.json" -from-bucket "$SRC_BUCKET" -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/led2.jsonl" -limit 5 -execute -report "$WC/part.json"
chk C1_limit_rc_zero "0" "$(last_rc)"
chk C2_limit_ledger_distinct_ids "5" "$(python3 -c "import json;rows=[json.loads(l) for l in open('$WC/led2.jsonl') if l.strip()];print(len({r['export_id'] for r in rows}))")"
chk C2B_limit_ledger_statuses "COPIED=5,VERIFIED=5" "$(python3 -c "import json;from collections import Counter;rows=[json.loads(l) for l in open('$WC/led2.jsonl') if l.strip()];c=Counter(r['status'] for r in rows);print(','.join('%s=%s'%(k,c[k]) for k in sorted(c)))")"
chk C3_limit_by_status "VERIFIED=5" "$(python3 "$REP" map "$WC/part.json" by_status)"
chk C4_limit_total "5" "$(python3 "$REP" get "$WC/part.json" total)"
chk C5_limit_failures_count "0" "$(python3 "$REP" failures "$WC/part.json")"
RUN -mode migrate -items-file "$WC/clean.json" -from-bucket "$SRC_BUCKET" -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/led2.jsonl" -execute -report "$WC/full.json"
chk C6_full_rc_zero "0" "$(last_rc)"
chk C7_full_by_action "copied=7,skipped_verified=5" "$(python3 "$REP" map "$WC/full.json" by_action)"
chk C8_full_by_status "VERIFIED=12" "$(python3 "$REP" map "$WC/full.json" by_status)"
chk C9_full_total "12" "$(python3 "$REP" get "$WC/full.json" total)"
chk C10_full_failures_null_parsed_as_empty "0" "$(python3 "$REP" failures "$WC/full.json")"
chk C11_full_allverified_true "true" "$(python3 "$REP" allverified "$WC/full.json")"
MISMATCH=""
for i in $(seq 1 12); do
  d=$(sha_obj "$DST_BUCKET" "$PFX/clean/c$i.csv")
  if [ "$d" != "$(sha_file c$i.csv)" ]; then MISMATCH="$MISMATCH c$i"; fi
done
chk C12_all_dst_bytes_match_src "none" "${MISMATCH:-none}"
RUN -mode verify -to-bucket "$DST_BUCKET" -ledger jsonl -ledger-file "$WC/led2.jsonl" -report "$WC/ver.json"
chk C13_verify_rc_zero "0" "$(last_rc)"
chk C14_verify_ledger_rows "12" "$(python3 "$REP" get "$WC/ver.json" ledger_rows)"
chk C15_verify_verified "12" "$(python3 "$REP" get "$WC/ver.json" verified)"
chk C16_verify_drift_empty "0" "$(python3 -c "import json;d=json.load(open('$WC/ver.json'));print(len(d.get('drift') or []))")"
chk C17_verify_not_verified_empty "0" "$(python3 -c "import json;d=json.load(open('$WC/ver.json'));print(len(d.get('not_verified') or []))")"
chk C18_final_ledger_distinct_ids "12" "$(python3 -c "import json;rows=[json.loads(l) for l in open('$WC/led2.jsonl') if l.strip()];print(len({r['export_id'] for r in rows}))")"
chk C19_final_ledger_verified_rows_match_source "0" "$(python3 -c "import json;rows=[json.loads(l) for l in open('$WC/led2.jsonl') if l.strip()];print(sum(1 for r in rows if r['status']=='VERIFIED' and r['destination_sha256']!=r['source_sha256']))")"

# ---------------------------------------------------------------- S) isolation
say "== S) production-bucket isolation =="
PROD_HITS=$(grep -l -E 'blockchain-raw|blockchain-exports' "$WC"/*.json 2>/dev/null | tr '\n' ' ')
chk S1_reports_contain_no_production_bucket "" "$PROD_HITS"
chk S2_report_from_to_buckets "\"$SRC_BUCKET\"|\"$DST_BUCKET\"" "$(python3 "$REP" get "$WC/full.json" from_bucket)|$(python3 "$REP" get "$WC/full.json" to_bucket)"

# ---------------------------------------------------------------- D) coverage gate
say "== D) assertion coverage gate =="
# COVERED holds the 52 substantive assertion IDs; GATE holds this gate's own 5 IDs.
# They are kept apart because D1..D5 are recorded while the gate itself is running:
# counting them inside their own expected set would make the gate self-referential.
COVERED="P1_null_is_explicit_empty_failure_set P2_empty_list_is_empty_failure_set P3_nonempty_failure_set_detected P4_missing_key_is_schema_error P5_wrong_type_is_schema_error P6_parser_selftest_all_controls S0_scratch_buckets_exist_preflight S0A_src_seed_ok S0B_dst_conflict_seeded_different S0C_dst_same_seeded_identical A1_dryrun_rc_nonzero A2_dryrun_no_ledger_side_effect A3_dryrun_by_action A4_dryrun_by_status A5_dryrun_failures_count A6_dryrun_execute_false A7_dryrun_no_dst_ok A8_dryrun_no_dst_badsha A9_dryrun_conflict_target_untouched A10_dryrun_src_ok_untouched B1_execute_rc_nonzero B2_execute_by_action B3_execute_by_status B4_execute_failures_count B5_conflict_target_unchanged B6_badsha_not_copied B7_all_sources_kept B8_copied_ok_bytes_match_source B9_copied_nosha_bytes_match_source B10_execute_report_allverified_false C1_limit_rc_zero C2_limit_ledger_distinct_ids C2B_limit_ledger_statuses C3_limit_by_status C4_limit_total C5_limit_failures_count C6_full_rc_zero C7_full_by_action C8_full_by_status C9_full_total C10_full_failures_null_parsed_as_empty C11_full_allverified_true C12_all_dst_bytes_match_src C13_verify_rc_zero C14_verify_ledger_rows C15_verify_verified C16_verify_drift_empty C17_verify_not_verified_empty C18_final_ledger_distinct_ids C19_final_ledger_verified_rows_match_source S1_reports_contain_no_production_bucket S2_report_from_to_buckets"
GATE="D1_no_missing_required_assertion D2_no_duplicate_assertion D3_no_unexpected_assertion D4_required_assertion_count D5_executed_assertion_count"
MISSING=""
DUP=""
for id in $COVERED; do
  n=$(grep -c "^$id$" "$SEEN" || true)
  if [ "$n" -eq 0 ]; then MISSING="$MISSING $id"; fi
  if [ "$n" -gt 1 ]; then DUP="$DUP $id($n)"; fi
done
EXTRA=""
for id in $(sort -u "$SEEN"); do
  case " $COVERED $GATE " in
    *" $id "*) ;;
    *) EXTRA="$EXTRA $id" ;;
  esac
done
chk D1_no_missing_required_assertion "none" "${MISSING:-none}"
chk D2_no_duplicate_assertion "none" "${DUP:-none}"
chk D3_no_unexpected_assertion "none" "${EXTRA:-none}"
chk D4_required_assertion_count "52" "$(printf '%s\n' $COVERED | wc -l | tr -d ' ')"
# +1 counts this assertion itself, which can only be recorded after it has been evaluated.
GATE_EXEC_N=$(sort -u "$SEEN" | wc -l | tr -d ' ')
chk D5_executed_assertion_count "57" "$((GATE_EXEC_N + 1))"

say "B3V2_ASSERTION_IDS=$(sort -u "$SEEN" | wc -l | tr -d ' ')"
say "B3V2_PASS=$PASS_N B3V2_FAIL=$FAIL_N"
say "B3V2_OBJECT_PREFIX=$PFX"
say "B3V2_WC=$WC"
if [ "$BAD" -ne 0 ]; then
  say "B3V2_RESULT=FAIL"
  exit 3
fi
say "B3V2_RESULT=PASS"
exit 0
