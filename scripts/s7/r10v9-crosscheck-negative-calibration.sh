#!/usr/bin/env bash
# r10v9-crosscheck-negative-calibration.sh -- negative calibration for crosscheck v8.
#
# Purpose (fail-closed proof, not a green-run generator):
#   Prove that crosscheck v8's "reused export + no v2 completion row" branch really FAILS when the
#   export was created AFTER the audit-fix deploy, and that a PRE-fix export is only downgraded to an
#   observation (never recorded as PASS).
#
# Method -- the real evidence dir is READ ONLY. Every mutation happens in a COPY:
#   C0 copy <EV> -> <EV>-NEGCAL-<TS>  (original untouched; copy is a new object)
#   C1 inside the copy, repoint uc-desktop/r10-09-evidence.json at the PRE-FIX export
#      f62e4308-3a2d-4db9-b1e7-084bff3ae97d (present in the pre-ids snapshot, no v2 completion row)
#      and set createHttp.status = 200 (reuse), so the same evidence exercises the reused branch.
#   C2 run crosscheck v8 on the copy with the DEFAULT boundary (2026-09-13T05:55:08Z):
#      expect the pre-fix observation, i.e. NO "audit-v2-export-completed-present:r10-09-evidence.json"
#      failure and NO PASS line for that check name.
#   C3 run crosscheck v8 on the copy with R10_AUDIT_FIX_DEPLOY_UTC=2026-09-13T00:00:00Z:
#      same export is now "created after the fix but missing the v2 completion row" -> MUST FAIL.
#   Exit 0 only when C2 and C3 both behave exactly as above.
set -uo pipefail
R=/home/euripides/projects/blockchain-platform
EV_SRC="${1:?usage: r10v9-crosscheck-negative-calibration.sh <real-evidence-dir>}"
XCHECK="${2:-$R/scripts/s7/r10-crosscheck-v8.py}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
COPY="${EV_SRC}-NEGCAL-$TS"
PREFIX_EXPORT="f62e4308-3a2d-4db9-b1e7-084bff3ae97d"
TARGET_CHECK="audit-v2-export-completed-present:r10-09-evidence.json"

echo "== C0 copy evidence (read-only on the original) =="
cp -a "$EV_SRC" "$COPY" || { echo "NEGCAL_ABORT copy failed"; exit 3; }
echo "src=$EV_SRC"
echo "copy=$COPY"
echo "copy_bytes=$(du -sb "$COPY" | cut -f1)"

echo "== C1 tamper the copy only =="
python3 - "$COPY" "$PREFIX_EXPORT" <<'PY'
import json, os, sys
copy, prefix = sys.argv[1], sys.argv[2]
path = os.path.join(copy, "uc-desktop", "r10-09-evidence.json")
d = json.load(open(path, encoding="utf-8"))
orig_export = (d.get("export") or {}).get("id")
orig_status = (d.get("createHttp") or {}).get("status")
d.setdefault("export", {})["id"] = prefix
d.setdefault("createHttp", {})["status"] = 200
d["negcal"] = {"tampered": True, "original_export_id": orig_export, "original_create_status": orig_status,
               "reason": "negative calibration: force the reused/pre-fix branch for the completion assertion"}
json.dump(d, open(path, "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print("tampered export.id %s -> %s ; createHttp.status %s -> 200" % (orig_export, prefix, orig_status))
PY
echo "tamper_rc=$?"

run_case() {
  local tag="$1"; shift
  local out="$COPY/negcal-$tag"
  mkdir -p "$out"
  env "$@" python3 "$XCHECK" "$COPY" > "$out/r10-summary.json" 2> "$out/r10-crosscheck.err"
  local rc=$?
  echo "$rc" > "$out/rc.txt"
  python3 - "$out" "$TARGET_CHECK" "$rc" <<'PY'
import json, os, sys
out, target, rc = sys.argv[1], sys.argv[2], int(sys.argv[3])
d = json.load(open(os.path.join(out, "r10-summary.json"), encoding="utf-8"))
checks = d.get("checks", []) or []
hit_fail = [c for c in checks if c["check"] == target and not c["ok"]]
hit_pass = [c for c in checks if c["check"] == target and c["ok"]]
obs = [o for o in (d.get("observations") or []) if "not-applicable-prefix" in str(o.get("observation", ""))]
res = {"crosscheck_rc": rc, "checks_total": d.get("checks_total"), "checks_failed": d.get("checks_failed"),
       "target_check": target, "target_FAIL_count": len(hit_fail), "target_PASS_count": len(hit_pass),
       "prefix_observation_count": len(obs),
       "failed_checks": sorted(c["check"] for c in checks if not c["ok"])[:12]}
json.dump(res, open(os.path.join(out, "calibration-result.json"), "w", encoding="utf-8"), ensure_ascii=False, indent=1)
print(json.dumps(res, ensure_ascii=False, indent=1))
PY
  echo "case_rc_saved=$rc tag=$tag"
}

echo "== C2 default boundary (expect: pre-fix observation, no target FAIL) =="
run_case default
echo "== C3 boundary forced earlier than the pre-fix export (expect: target FAIL) =="
run_case forced R10_AUDIT_FIX_DEPLOY_UTC=2026-09-13T00:00:00Z

echo "== C4 verdict =="
python3 - "$COPY" "$TARGET_CHECK" <<'PY'
import json, os, sys
copy, target = sys.argv[1], sys.argv[2]
d = json.load(open(os.path.join(copy, "negcal-default", "calibration-result.json"), encoding="utf-8"))
f = json.load(open(os.path.join(copy, "negcal-forced", "calibration-result.json"), encoding="utf-8"))
ok_default = d["target_FAIL_count"] == 0 and d["target_PASS_count"] == 0 and d["prefix_observation_count"] >= 1
ok_forced = f["target_FAIL_count"] >= 1 and f["target_PASS_count"] == 0
print("default_boundary_pre_fix_is_observation_only=%s (fail=%d pass=%d obs=%d)" % (
    ok_default, d["target_FAIL_count"], d["target_PASS_count"], d["prefix_observation_count"]))
print("forced_boundary_post_fix_missing_v2_is_hard_fail=%s (fail=%d pass=%d)" % (
    ok_forced, f["target_FAIL_count"], f["target_PASS_count"]))
print("NEGCAL_VERDICT=%s" % ("PASS" if (ok_default and ok_forced) else "FAIL"))
sys.exit(0 if (ok_default and ok_forced) else 1)
PY
echo "NEGCAL_DIR=$COPY"
