#!/usr/bin/env bash
# r10v9-strong-gate-v1.sh -- R10 strong gate (root ruling 15:16).
#
# R10 may only be called CLOSED when this gate is 0 FAIL. It never modifies the round evidence dir.
#   S1 freeze   : per-file sha256 manifest of the round dir (frozen copy of record)
#   S2 strict   : crosscheck v10 rc=0 and checks_failed=0
#                 (new export status is strictly 201; 200 only ever means reuse; every genuinely_new
#                  export must carry completed.request_id/trace_id == UI createHttp == audit export.create)
#   S3 chain    : audit chain gate v1 rc=0 (frozen-source auditverify, head == max-seq row event_hash)
#   S4 negative : tampering a COPY must make the strict crosscheck non-zero
#                 N1 change createHttp.requestId on a genuinely_new export
#                 N2 change createHttp.status 201 -> 200 on a genuinely_new export
#   S5 integrity: re-freeze after S2-S4 and require byte-identical manifest (evidence untouched)
# Usage: r10v9-strong-gate-v1.sh <round-evidence-dir> [reverify-dir]
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
EV="${1:?usage: r10v9-strong-gate-v1.sh <round-evidence-dir> [reverify-dir]}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
REV="${2:-$EV-strong-reverify-$TS}"
XCHECK="${R10_STRICT_XCHECK:-$R/scripts/s7/r10-crosscheck-v10.py}"
CHAIN_GATE="${R10_AUDIT_CHAIN_GATE:-$R/scripts/s7/r10-audit-chain-gate-v1.sh}"
EXPECT_API_SHA="${R10_EXPECT_API_SHA:-484e2707e6574cce10e41e68047176ff5255aa231d1615c4b10cddd3fe5db2e4}"
[ -d "$EV" ] || { echo "STRONG_GATE_ABORT missing evidence dir $EV"; exit 3; }
mkdir -p "$REV"; chmod 700 "$REV"
exec > >(tee "$REV/strong-gate.log") 2>&1
SP=0; SF=0
ok(){ SP=$((SP+1)); echo "PASS[$SP] $1 :: $2"; }
bad(){ SF=$((SF+1)); echo "FAIL[$SF] $1 :: $2"; }
chk(){ if [ "$1" = "$2" ]; then ok "$3" "have=$1"; else bad "$3" "have=$1 want=$2"; fi; }
echo "gate_start_utc=$(date -u +%FT%TZ)"
echo "evidence_dir=$EV"
echo "reverify_dir=$REV"
echo "crosscheck=$XCHECK sha256=$(sha256sum "$XCHECK" | cut -d' ' -f1)"
echo "chain_gate=$CHAIN_GATE sha256=$(sha256sum "$CHAIN_GATE" | cut -d' ' -f1)"

echo "=== S1 冻结轮证据（逐文件 sha256；核验过程只读） ==="
freeze(){ ( cd "$EV" && find . -type f -print0 | sort -z | xargs -0 sha256sum ); }
freeze > "$REV/frozen-manifest-before.txt"
echo "frozen_files=$(wc -l < "$REV/frozen-manifest-before.txt")" | tee "$REV/strong-gate-meta.txt"
sha256sum "$REV/frozen-manifest-before.txt" | tee -a "$REV/strong-gate-meta.txt"
chk "$([ -s "$REV/frozen-manifest-before.txt" ] && echo yes || echo no)" "yes" "S1 manifest written"

echo "=== S2 严格 crosscheck v10 ==="
R10_MINIO_ENV="${R10_MINIO_ENV:-}" python3 "$XCHECK" "$EV" > "$REV/r10-summary-crosscheck-v10.json" 2> "$REV/r10-crosscheck-v10.err"
XRC=$?
echo "crosscheck_v10_rc=$XRC" >> "$REV/strong-gate.txt"
chk "$XRC" "0" "S2.1 crosscheck v10 rc=0"
CF="$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d.get("checks_failed"))' "$REV/r10-summary-crosscheck-v10.json" 2>/dev/null || echo ERR)"
CT="$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d.get("checks_total"))' "$REV/r10-summary-crosscheck-v10.json" 2>/dev/null || echo ERR)"
echo "checks_total=$CT checks_failed=$CF" | tee -a "$REV/strong-gate-meta.txt"
chk "$CF" "0" "S2.2 checks_failed=0 (total=$CT)"
python3 - "$REV/r10-summary-crosscheck-v10.json" <<'PY' > "$REV/strict-checks.txt" 2>&1
import json, sys
d = json.load(open(sys.argv[1], encoding="utf-8"))
want = ("audit-v2-export-create-status-strict", "audit-v2-export-completed-same-occurrence-request",
        "audit-v2-export-completed-same-occurrence-trace", "legacy-and-v2-completion-same-export")
for c in d.get("checks", []):
    if any(w in c["check"] for w in want):
        print(("OK  " if c["ok"] else "FAIL"), c["check"], "::", json.dumps(c["detail"], ensure_ascii=False)[:220])
PY
cat "$REV/strict-checks.txt"
NSO="$(grep -c 'same-occurrence-request' "$REV/strict-checks.txt" || true)"
NSOOK="$(grep -c '^OK   audit-v2-export-completed-same-occurrence-request' "$REV/strict-checks.txt" || true)"
NAPP="$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(sum(1 for o in (d.get("observations") or []) if "same-occurrence-not-applicable" in str(o.get("observation",""))))' "$REV/r10-summary-crosscheck-v10.json")"
echo "same_occurrence_checks=$NSO same_occurrence_ok=$NSOOK not_applicable_observations=$NAPP" | tee -a "$REV/strong-gate-meta.txt"
chk "$NSO" "$NSOOK" "S2.3 每个同一次发生判据都真实成立（没有 201 落入 not-applicable）"

echo "=== S3 审计链独立闸门 ==="
AV="$EV/audit-chain/auditverify.build"
if [ ! -x "$AV" ]; then AV="$R/acceptance-evidence/dual-system-v1/sprint7/r10v8-auditrequest-build-20260913T060129Z/auditverify.build"; fi
if [ ! -x "$AV" ]; then bad "S3.0 frozen auditverify present" "missing"; else
  cp -p "$AV" "$REV/auditverify.build"
  bash "$CHAIN_GATE" "$REV" "$EXPECT_API_SHA" > "$REV/chain-gate.out" 2>&1
  CRC=$?
  echo "audit_chain_gate_rc=$CRC" >> "$REV/strong-gate.txt"
  tail -n 6 "$REV/chain-gate.out"
  chk "$CRC" "0" "S3.1 audit chain gate rc=0"
fi

echo "=== S4 负向校准（篡改副本必须让严格门非 0） ==="
tamper_case(){
  local tag="$1"; local field="$2"; local val="$3"
  local cp="$REV/negcal-$tag"
  rm -rf "$cp"; cp -a "$EV" "$cp"
  python3 - "$cp" "$field" "$val" <<'PY'
import json, os, sys
cp, field, val = sys.argv[1], sys.argv[2], sys.argv[3]
target = os.path.join(cp, "uc-desktop", "r10-09-evidence.json")
d = json.load(open(target, encoding="utf-8"))
ch = d.setdefault("createHttp", {})
if field == "requestId":
    ch["requestId"] = val
elif field == "status":
    ch["status"] = int(val)
else:
    raise SystemExit("unknown field " + field)
json.dump(d, open(target, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print("tampered $tag: createHttp.$field ->", ch.get(field))
PY
  R10_MINIO_ENV="${R10_MINIO_ENV:-}" python3 "$XCHECK" "$cp" > "$REV/negcal-$tag-summary.json" 2> "$REV/negcal-$tag.err"
  local rc=$?
  echo "negcal_$tag rc=$rc" >> "$REV/strong-gate.txt"
  if [ "$rc" -ne 0 ]; then ok "S4.$tag tampered $field -> non-zero" "rc=$rc"; else bad "S4.$tag tampered $field -> non-zero" "rc=$rc (篡改未被发现 = fail-open)"; fi
}
tamper_case N1 requestId "negcal-tampered-request-id-not-the-create-request"
tamper_case N2 status 200

echo "=== S5 核验后证据完整性（S1 vs 复核后） ==="
freeze > "$REV/frozen-manifest-after.txt"
if diff -q "$REV/frozen-manifest-before.txt" "$REV/frozen-manifest-after.txt" > /dev/null; then
  ok "S5 evidence untouched" "manifest identical ($(wc -l < "$REV/frozen-manifest-before.txt") files)"
else
  bad "S5 evidence untouched" "manifest CHANGED during verification"
  diff "$REV/frozen-manifest-before.txt" "$REV/frozen-manifest-after.txt" | head -20
fi

echo "=== RESULT strong_gate_PASS=$SP strong_gate_FAIL=$SF ==="
echo "strong_gate_fail_count=$SF" >> "$REV/strong-gate.txt"
if [ "$SF" -eq 0 ]; then echo "STRONG_GATE_VERDICT=PASS" | tee -a "$REV/strong-gate.txt"; exit 0; fi
echo "STRONG_GATE_VERDICT=FAIL" | tee -a "$REV/strong-gate.txt"; exit 1
