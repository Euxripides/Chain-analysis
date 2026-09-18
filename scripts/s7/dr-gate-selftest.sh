#!/usr/bin/env bash
# dr-gate.sh 离线自测：用合成证据证明 gate 会阻断、且只有在范围内全闭合时才 PASS。
# 只读/自包含：全部在临时目录内，不触碰 acceptance-evidence 与任何服务。
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
GATE="$HERE/dr-gate.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAILS=0
pass() { echo "  SELFTEST-OK  $1"; }
bad()  { echo "  SELFTEST-FAIL $1"; FAILS=$((FAILS+1)); }
expect() { # expect <期望rc> <标签>
  local want="$1" label="$2" rc="$3"
  if [ "$rc" = "$want" ]; then pass "$label (rc=$rc)"; else bad "$label 期望 rc=$want 实得 rc=$rc"; fi
}

mk_restore() { # mk_restore <文件> <verdict> <nv_structural_n> <partial_n> <buckets>
  python3 - "$@" <<'PY'
import json, sys
path, verdict, nvs, part, buckets = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), sys.argv[5]
nv = ["结构性项%d" % i for i in range(nvs)]
json.dump({
  "drill": "restore-drill", "ts": "20260913T000000Z", "backup_dir": "/tmp/b",
  "verdict": verdict, "failures": [],
  "not_verified": nv, "nv_class": {"structural": nv, "check": []},
  "nv_counts": {"total": len(nv), "structural": len(nv), "check": 0},
  "partial": ["p"] * part, "partial_count": part,
  "targets": {"postgres": {"database": "d"}, "clickhouse": {"database": "c"},
              "minio": {b: b + "-drill" for b in buckets.split()}},
  "rto_seconds": {"postgres": "5.81", "clickhouse": "561.36", "minio": {}},
}, open(path, "w"), ensure_ascii=False)
PY
}
mk_kill9() { # mk_kill9 <文件> <verdict> <nv_n> <partial_n>
  python3 - "$@" <<'PY'
import json, sys
path, verdict, nv, part = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4])
nvl = ["消费者幂等维度不存在"] * nv
json.dump({"drill": "kill9-drill", "ts": "20260913T000000Z", "verdict": verdict,
           "failures": [], "not_verified": nvl,
           "nv_class": {"structural": nvl, "check": []},
           "partial": ["p"] * part, "partial_count": part,
           "truncated_datasets": []}, open(path, "w"), ensure_ascii=False)
PY
}
mk_pitr() { # mk_pitr <文件> <verdict> <archive_mode> <rpo>
  python3 - "$@" <<'PY'
import json, sys
p, v, am, rpo = sys.argv[1:5]
json.dump({"drill": "pitr", "ts": "20260913T000000Z", "verdict": v, "archive_mode": am,
           "rpo_seconds": float(rpo), "reached_target": True}, open(p, "w"))
PY
}
mk_meta() { # mk_meta <文件> <verdict> <维度>
  python3 - "$@" <<'PY'
import json, sys
p, v, dims = sys.argv[1], sys.argv[2], sys.argv[3]
B = "blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw".split()
json.dump({"drill": "minio-object-metadata", "ts": "20260913T000000Z", "verdict": v,
           "dimensions": {d: True for d in dims.split()},
           "buckets": {b: {"ok": True} for b in B}}, open(p, "w"))
PY
}
mk_idem() { # mk_idem <文件> <verdict> <table> <dup>
  python3 - "$@" <<'PY'
import json, sys
p, v, t, dup = sys.argv[1:5]
json.dump({"drill": "consumer-idempotency", "ts": "20260913T000000Z", "verdict": v, "table": t,
           "dimension": "event_id", "duplicates_after": int(dup)}, open(p, "w"))
PY
}

BKS="blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw"

# [GATE-03 自测接缝] 冻结批次时间戳与现场 archive_mode 在离线自测里注入合成值：
#   DR_GATE_FREEZE_TS 让合成证据（ts=20260913T000000Z）被视为"本次冻结之后产出"；
#   DR_GATE_LIVE_ARCHIVE_MODE_CMD 让现场复核返回合成值（生产默认是真实探测）。
export DR_GATE_FREEZE_TS="20200101T000000Z"
export DR_GATE_LIVE_ARCHIVE_MODE_CMD="echo on"

echo "== 用例 A：完全无证据（期望 BLOCKED）=="
A="$TMP/a"; mkdir -p "$A"; EV="$A" bash "$GATE" >/dev/null 2>&1; expect 1 "A 无任何证据必须阻断" "$?"

echo "== 用例 B：restore PASS 但仍有 structural NV（期望 BLOCKED）=="
B="$TMP/b"; mkdir -p "$B"
mk_restore "$B/restore-drill-1.json" PASS 1 0 "$BKS"
mk_kill9   "$B/kill9-1.json" PASS 0 0
mk_pitr    "$B/pitr-1.json" PASS on 60
mk_meta    "$B/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$B/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$B" bash "$GATE" >/dev/null 2>&1; expect 1 "B structural NV 必须阻断" "$?"

echo "== 用例 C：全部闭合（期望 PASS）=="
C="$TMP/c"; mkdir -p "$C"
mk_restore "$C/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$C/kill9-1.json" PASS 0 0
mk_pitr    "$C/pitr-1.json" PASS on 60
mk_meta    "$C/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$C/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$C" bash "$GATE" >/dev/null 2>&1; expect 0 "C 全闭合应 PASS" "$?"

echo "== 用例 D：PARTIAL>0（期望 BLOCKED）=="
D="$TMP/d"; mkdir -p "$D"
mk_restore "$D/restore-drill-1.json" PASS 0 2 "$BKS"
mk_kill9   "$D/kill9-1.json" PASS 0 0
mk_pitr    "$D/pitr-1.json" PASS on 60
mk_meta    "$D/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$D/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$D" bash "$GATE" >/dev/null 2>&1; expect 1 "D PARTIAL 必须阻断" "$?"

echo "== 用例 E：RPO 超限 + raw 桶缺失（期望 BLOCKED）=="
E="$TMP/e"; mkdir -p "$E"
mk_restore "$E/restore-drill-1.json" PASS 0 0 "blockchain-audit blockchain-reports"
mk_kill9   "$E/kill9-1.json" PASS 0 0
mk_pitr    "$E/pitr-1.json" PASS on 3600
mk_meta    "$E/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$E/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$E" bash "$GATE" >/dev/null 2>&1; expect 1 "E RPO 超限/桶缺失必须阻断" "$?"

echo "== 用例 F：MinIO 元数据维度缺失（期望 BLOCKED）=="
F="$TMP/f"; mkdir -p "$F"
mk_restore "$F/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$F/kill9-1.json" PASS 0 0
mk_pitr    "$F/pitr-1.json" PASS on 60
mk_meta    "$F/minio-object-metadata-1.json" PASS "versions"
mk_idem    "$F/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$F" bash "$GATE" >/dev/null 2>&1; expect 1 "F 维度缺失必须阻断" "$?"

echo "== 用例 G：消费者幂等无真实维度表（期望 BLOCKED）=="
G="$TMP/g"; mkdir -p "$G"
mk_restore "$G/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$G/kill9-1.json" PASS 0 0
mk_pitr    "$G/pitr-1.json" PASS on 60
mk_meta    "$G/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$G/consumer-idempotency-1.json" PASS none 0
EV="$G" bash "$GATE" >/dev/null 2>&1; expect 1 "G 无真实幂等维度必须阻断" "$?"

echo "== 用例 H：kill9 verdict=INCOMPLETE（期望 BLOCKED）=="
H="$TMP/h"; mkdir -p "$H"
mk_restore "$H/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$H/kill9-1.json" INCOMPLETE 1 0
mk_pitr    "$H/pitr-1.json" PASS on 60
mk_meta    "$H/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$H/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$H" bash "$GATE" >/dev/null 2>&1; expect 1 "H INCOMPLETE 必须阻断" "$?"

echo "== 用例 I：证据全闭合，但现场 archive_mode=off（期望 BLOCKED）=="
I="$TMP/i"; mkdir -p "$I"
mk_restore "$I/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$I/kill9-1.json" PASS 0 0
mk_pitr    "$I/pitr-1.json" PASS on 60
mk_meta    "$I/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$I/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$I" DR_GATE_LIVE_ARCHIVE_MODE_CMD="echo off" bash "$GATE" >/dev/null 2>&1; expect 1 "I 现场 archive_mode=off 必须阻断" "$?"

echo "== 用例 J：证据 ts 早于冻结批次（期望 BLOCKED）=="
J="$TMP/j"; mkdir -p "$J"
mk_restore "$J/restore-drill-1.json" PASS 0 0 "$BKS"
mk_kill9   "$J/kill9-1.json" PASS 0 0
mk_pitr    "$J/pitr-1.json" PASS on 60
mk_meta    "$J/minio-object-metadata-1.json" PASS "versions ilm tags metadata"
mk_idem    "$J/consumer-idempotency-1.json" PASS consumer_inbox 0
EV="$J" DR_GATE_FREEZE_TS="20261231T000000Z" bash "$GATE" >/dev/null 2>&1; expect 1 "J 旧证据必须阻断" "$?"

echo
if [ "$FAILS" -eq 0 ]; then echo "dr-gate 自测：全部 10 个用例符合预期"; exit 0; fi
echo "dr-gate 自测：$FAILS 个用例不符合预期" >&2; exit 1
