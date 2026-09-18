#!/usr/bin/env bash
# =====================================================================
# S7-DR 聚合门禁（scripts/s7/dr-gate.sh）
#
# 目的：把"局部 PASS"与"整体验收通过"彻底分开。
#   * 单个 drill 脚本（restore-drill.sh / kill9-drill.sh）只做局部核对，
#     允许把 NOT_VERIFIED 标成 structural（脚本层结构性无法证明）以便诊断；
#   * 本 gate 对**计划要求范围内的任何 NV（structural 或 check）与任何 PARTIAL 一律阻断**，
#     不提供豁免开关、不支持"脚本还不能证所以放过"。
#     要让 gate 归零，只能把对应能力真实补齐（另配工具 / 产品侧改造 / 真实演练），
#     不允许靠改写分级、删条目或放宽容差。
#
# 计划依据：docs/dual-system-development-plan.md §27 DATA-006/007/009、§30.2/§30.3、
#           §31「证据 → 性能分层结果 / 容量预测 / RPO/RTO 演练」、
#           acceptance-audit.md 第 10/11 条（全桶恢复、FINAL 语义、RPO≤15min、RTO≤4h、幂等）。
#
# 退出码：0 = 全部在范围内证据齐备且无 NV/PARTIAL；1 = 存在阻断项；2 = 前置/用法错误。
# 本脚本**只读**：只读取已有证据文件，不执行备份/恢复/演练，不删除任何东西。
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/dr}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_JSON="$EV/dr-gate-$TS.json"
OUT_MD="$EV/dr-gate-$TS.md"
RPO_LIMIT_S="${DR_GATE_RPO_LIMIT_S:-900}"        # 15 分钟
RTO_LIMIT_S="${DR_GATE_RTO_LIMIT_S:-14400}"      # 4 小时
REQUIRED_BUCKETS="${DR_GATE_REQUIRED_BUCKETS:-blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-raw}"

# [GATE-03] 证据新鲜度 + 生效配置复核（2026-09-16 夜场新增）：
#   历史缺陷：本 gate 只读"最新的"证据文件，**完全不看产出时间**。实测后果 —— 2026-09-12 的
#   PITR 结果（archive_mode=on、RPO 51 s）被当作当前有效证据而放行，而当前实例实测
#   archive_mode=off，也就是 RPO 实际不受控。两条硬约束补上：
#     ① 所有证据的 ts 必须 ≥ 本次冻结批次时间戳（acceptance-evidence/dual-system-v1/release-<TS>），
#        否则判为旧构建/旧配置的证据并阻断；
#     ② PITR 项额外**现场复核**当前实例的 archive_mode，必须实测为 on（证据里的值只代表取证当时）。
FREEZE_TS="${DR_GATE_FREEZE_TS:-}"
if [ -z "$FREEZE_TS" ]; then
  FREEZE_TS="$(ls -d "$PROJECT"/acceptance-evidence/dual-system-v1/release-*/ 2>/dev/null | sed -e 's#.*/release-##' -e 's#/$##' | sort | tail -n 1)"
fi
echo "冻结批次时间戳（证据新鲜度基准）：${FREEZE_TS:-（未取得）}"
if [ -f "$PROJECT/deploy/.env" ]; then set -a; . "$PROJECT/deploy/.env"; set +a; fi
ev_ts_from_file() { # 优先取 JSON 的 ts 字段，退化为文件名里的 YYYYMMDDTHHMMSSZ
  local f="$1" t
  t="$(python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));print(d.get("ts") or "")' "$f" 2>/dev/null)"
  [ -n "$t" ] || t="$(basename "$f" | grep -oE '[0-9]{8}T[0-9]{6}Z' | head -n 1)"
  printf '%s' "$t"
}
stale_check() { # stale_check <label> <file>
  local label="$1" f="$2" t
  [ -n "$FREEZE_TS" ] || return 0
  t="$(ev_ts_from_file "$f")"
  if [ -z "$t" ]; then
    block "$label 证据缺少可解析的时间戳（$f）：无法证明它属于本次冻结版本"
  elif [ "$t" \< "$FREEZE_TS" ]; then
    block "$label 证据 ts=$t 早于本次冻结批次 $FREEZE_TS：属于旧构建/旧配置的证据，不得计入"
  else
    ok "$label 证据 ts=$t ≥ 冻结批次 $FREEZE_TS"
  fi
}

BLOCK_REASONS=()
note() { printf '%s\n' "$*" >&2; }
block() { BLOCK_REASONS+=("$1"); note "BLOCK: $1"; }
ok()    { note "  ok: $1"; }

[ -d "$EV" ] || { echo "ABORT: 证据目录不存在：$EV" >&2; exit 2; }

latest() { # latest <glob>  -> 最新（按名/按 mtime）匹配文件
  local pat="$1"
  # shellcheck disable=SC2086
  ls -1t $pat 2>/dev/null | head -n 1
}

echo "=== S7-DR 聚合门禁 $TS ==="
echo "证据目录：$EV"
echo "RPO 上限：${RPO_LIMIT_S}s；RTO 上限：${RTO_LIMIT_S}s"
echo "必需桶：$REQUIRED_BUCKETS"
echo

# ---------- 1) restore-drill ----------
RD="${DR_GATE_RESTORE_RESULT:-}"
[ -n "$RD" ] || RD="$(latest "$EV/restore-drill-*.json")"
echo "[1] restore-drill 结果：${RD:-（未找到）}"
if [ -z "$RD" ] || [ ! -s "$RD" ]; then
  block "找不到 restore-drill 的机器可读结果（$EV/restore-drill-*.json）：没有执行过的恢复演练，不能判 DR 通过"
else
  python3 - "$RD" <<'PY' > "$EV/.dr-gate-rd.env" 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1]))
def esc(s):
    return str(s).replace("\n", " ").replace("'", "’")
print("RD_VERDICT='%s'" % esc(d.get("verdict", "MISSING")))
print("RD_NV_N='%d'" % len(d.get("not_verified") or []))
print("RD_PARTIAL_N='%d'" % len(d.get("partial") or []))
print("RD_FAIL_N='%d'" % len(d.get("failures") or []))
nvc = d.get("nv_class") or {}
print("RD_NV_STRUCT_N='%d'" % len(nvc.get("structural") or []))
print("RD_NV_CHECK_N='%d'" % len(nvc.get("check") or []))
print("RD_NV_FIRST='%s'" % esc(((d.get("not_verified") or [""])[0])[:200]))
rto = d.get("rto_seconds") or {}
pg = rto.get("postgres"); ch = rto.get("clickhouse")
print("RD_RTO_PG='%s'" % esc(pg))
print("RD_RTO_CH='%s'" % esc(ch))
tgt = ((d.get("targets") or {}).get("minio") or {})
print("RD_MINIO_BUCKETS='%s'" % esc(" ".join(sorted(tgt.keys()))))
PY
  # shellcheck disable=SC1091
  . "$EV/.dr-gate-rd.env" 2>/dev/null || true
  echo "  verdict=${RD_VERDICT:-?} NV=${RD_NV_N:-?}（structural ${RD_NV_STRUCT_N:-?} / check ${RD_NV_CHECK_N:-?}）PARTIAL=${RD_PARTIAL_N:-?} FAIL=${RD_FAIL_N:-?}"
  [ "${RD_VERDICT:-}" = "PASS" ] || block "restore-drill verdict=${RD_VERDICT:-?}（必须为 PASS；INCOMPLETE 表示仍有 NV 或 PARTIAL）"
  [ "${RD_NV_N:-1}" = "0" ] || block "restore-drill 仍有 NOT_VERIFIED ${RD_NV_N:-?} 条（structural ${RD_NV_STRUCT_N:-?} / check ${RD_NV_CHECK_N:-?}）——计划范围内不允许任何 NV"
  [ "${RD_PARTIAL_N:-1}" = "0" ] || block "restore-drill 仍有 PARTIAL ${RD_PARTIAL_N:-?} 条"
  [ "${RD_FAIL_N:-1}" = "0" ] || block "restore-drill 仍有 FAIL ${RD_FAIL_N:-?} 条"
  # 全桶覆盖
  for bk in $REQUIRED_BUCKETS; do
    case " ${RD_MINIO_BUCKETS:-} " in *" $bk "*) ok "MinIO 桶已恢复并比对：$bk" ;; *) block "MinIO 桶未覆盖：$bk（恢复演练 targets 中缺失）" ;; esac
  done
  # RTO
  if [ -n "${RD_RTO_PG:-}" ] && awk -v v="${RD_RTO_PG}" -v l="$RTO_LIMIT_S" 'BEGIN{exit !(v+0<=l+0)}'; then ok "PG RTO=${RD_RTO_PG}s ≤ ${RTO_LIMIT_S}s"; else block "PG RTO 缺失或超限：${RD_RTO_PG:-（缺失）}s"; fi
  if [ -n "${RD_RTO_CH:-}" ] && awk -v v="${RD_RTO_CH}" -v l="$RTO_LIMIT_S" 'BEGIN{exit !(v+0<=l+0)}'; then ok "CH RTO=${RD_RTO_CH}s ≤ ${RTO_LIMIT_S}s"; else block "CH RTO 缺失或超限：${RD_RTO_CH:-（缺失）}s"; fi
  stale_check "restore-drill" "$RD"
fi
echo

# ---------- 2) kill9-drill ----------
K9="${DR_GATE_KILL9_RESULT:-}"
[ -n "$K9" ] || K9="$(latest "$EV/kill9-*.json")"
echo "[2] kill9-drill 结果：${K9:-（未找到）}"
if [ -z "$K9" ] || [ ! -s "$K9" ]; then
  block "找不到 kill9-drill 的机器可读结果（$EV/kill9-*.json）：没有执行过的 Kill-9 演练，不能判 DATA-006 通过"
else
  python3 - "$K9" <<'PY' > "$EV/.dr-gate-k9.env" 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1]))
def esc(s):
    return str(s).replace("\n", " ").replace("'", "’")
print("K9_VERDICT='%s'" % esc(d.get("verdict", "MISSING")))
print("K9_NV_N='%d'" % len(d.get("not_verified") or []))
print("K9_PARTIAL_N='%d'" % len(d.get("partial") or []))
print("K9_FAIL_N='%d'" % len(d.get("failures") or []))
print("K9_TRUNC_N='%d'" % len(d.get("truncated_datasets") or []))
PY
  . "$EV/.dr-gate-k9.env" 2>/dev/null || true
  echo "  verdict=${K9_VERDICT:-?} NV=${K9_NV_N:-?} PARTIAL=${K9_PARTIAL_N:-?} FAIL=${K9_FAIL_N:-?} 截断数据集=${K9_TRUNC_N:-?}"
  [ "${K9_VERDICT:-}" = "PASS" ] || block "kill9-drill verdict=${K9_VERDICT:-?}（必须为 PASS）"
  [ "${K9_NV_N:-1}" = "0" ] || block "kill9-drill 仍有 NOT_VERIFIED ${K9_NV_N:-?} 条（含消费者幂等）——计划范围内不允许任何 NV"
  [ "${K9_PARTIAL_N:-1}" = "0" ] || block "kill9-drill 仍有 PARTIAL ${K9_PARTIAL_N:-?} 条（基线被截断的核对不得计为通过）"
  [ "${K9_FAIL_N:-1}" = "0" ] || block "kill9-drill 仍有 FAIL ${K9_FAIL_N:-?} 条"
  [ "${K9_TRUNC_N:-1}" = "0" ] || block "kill9-drill 有 ${K9_TRUNC_N:-?} 个数据集基线被截断"
  stale_check "kill9-drill" "$K9"
fi
echo

# ---------- 3) PITR / RPO ----------
echo "[3] PITR / RPO 证据"
PITR="${DR_GATE_PITR_RESULT:-}"
[ -n "$PITR" ] || PITR="$(latest "$EV/pitr-*.json")"
if [ -z "$PITR" ] || [ ! -s "$PITR" ]; then
  block "找不到 PITR 演练结果（$EV/pitr-*.json）：无 WAL 归档/PITR 就没有受控 RPO，DATA-009 的 RPO≤15min 无法成立"
else
  python3 - "$PITR" "$RPO_LIMIT_S" <<'PY' > "$EV/.dr-gate-pitr.env" 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1])); limit = float(sys.argv[2])
print("PITR_VERDICT='%s'" % d.get("verdict", "MISSING"))
print("PITR_ARCHIVE_MODE='%s'" % d.get("archive_mode", "MISSING"))
print("PITR_RPO_S='%s'" % d.get("rpo_seconds", "MISSING"))
print("PITR_TARGET_LSN_OK='%s'" % d.get("reached_target", "MISSING"))
print("PITR_LIMIT='%s'" % limit)
PY
  . "$EV/.dr-gate-pitr.env" 2>/dev/null || true
  echo "  verdict=${PITR_VERDICT:-?} archive_mode=${PITR_ARCHIVE_MODE:-?} rpo=${PITR_RPO_S:-?}s reached_target=${PITR_TARGET_LSN_OK:-?}"
  [ "${PITR_VERDICT:-}" = "PASS" ] || block "PITR 演练 verdict=${PITR_VERDICT:-?}（必须为 PASS）"
  [ "${PITR_ARCHIVE_MODE:-}" = "on" ] || block "archive_mode=${PITR_ARCHIVE_MODE:-?}（必须为 on 才有受控 RPO）"
  [ "${PITR_TARGET_LSN_OK:-}" = "True" ] || block "PITR 未证明恢复到目标时间点（reached_target=${PITR_TARGET_LSN_OK:-?}）"
  if awk -v v="${PITR_RPO_S:-999999}" -v l="${PITR_LIMIT:-900}" 'BEGIN{exit !(v+0<=l+0)}'; then
    ok "实测 RPO=${PITR_RPO_S}s ≤ ${PITR_LIMIT}s"
  else
    block "实测 RPO=${PITR_RPO_S:-?}s 超过上限 ${PITR_LIMIT:-900}s"
  fi
  stale_check "pitr" "$PITR"
  # [GATE-03] 现场复核：证据里的 archive_mode 只代表取证当时，必须与**当前**实例一致
  # 现场复核命令可用 DR_GATE_LIVE_ARCHIVE_MODE_CMD 覆盖（离线自测注入合成值用；**默认就是真实探测**，不是豁免开关）
  LIVE_ARCHIVE_MODE_CMD="${DR_GATE_LIVE_ARCHIVE_MODE_CMD:-docker exec -e "PGPASSWORD=${POSTGRES_PASSWORD:-}" deploy-postgres-1 psql -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-postgres}" -Atc 'show archive_mode'}"
  LIVE_ARCHIVE_MODE="$(eval "$LIVE_ARCHIVE_MODE_CMD" 2>/dev/null | tr -d '[:space:]')"
  echo "  当前实例 archive_mode（实测）：${LIVE_ARCHIVE_MODE:-未取得}"
  if [ "${LIVE_ARCHIVE_MODE:-}" != "on" ]; then
    block "当前实例 archive_mode=${LIVE_ARCHIVE_MODE:-未取得}（必须实测为 on 才有受控 RPO；PITR 证据只能证明取证当时）"
  fi
fi
echo

# ---------- 4) MinIO 版本 / 生命周期 / 对象元数据 ----------
echo "[4] MinIO 版本 / ILM / 对象元数据证据"
META="${DR_GATE_MINIO_META_RESULT:-}"
[ -n "$META" ] || META="$(latest "$EV/minio-object-metadata-*.json")"
if [ -z "$META" ] || [ ! -s "$META" ]; then
  block "找不到 MinIO 版本/ILM/元数据比对结果（$EV/minio-object-metadata-*.json）：mc mirror 结构性不含这些维度，必须另配工具单独取证"
else
  python3 - "$META" <<'PY' > "$EV/.dr-gate-meta.env" 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1]))
print("META_VERDICT='%s'" % d.get("verdict", "MISSING"))
print("META_BUCKETS='%s'" % " ".join(sorted((d.get("buckets") or {}).keys())))
print("META_DIMS='%s'" % " ".join(sorted((d.get("dimensions") or {}).keys())))
PY
  . "$EV/.dr-gate-meta.env" 2>/dev/null || true
  echo "  verdict=${META_VERDICT:-?} 桶=${META_BUCKETS:-?} 维度=${META_DIMS:-?}"
  [ "${META_VERDICT:-}" = "PASS" ] || block "MinIO 元数据比对 verdict=${META_VERDICT:-?}（必须为 PASS）"
  stale_check "minio-object-metadata" "$META"
  for dim in versions ilm tags metadata; do
    case " ${META_DIMS:-} " in *" $dim "*) ok "MinIO 维度已取证：$dim" ;; *) block "MinIO 维度缺失：$dim" ;; esac
  done
  for bk in $REQUIRED_BUCKETS; do
    case " ${META_BUCKETS:-} " in *" $bk "*) ;; *) block "MinIO 元数据比对未覆盖桶：$bk" ;; esac
  done
fi
echo

# ---------- 5) 消费者幂等 ----------
echo "[5] 消费者幂等证据（真实 event id / Nats-Msg-Id 维度）"
IDEM="${DR_GATE_IDEMPOTENCY_RESULT:-}"
[ -n "$IDEM" ] || IDEM="$(latest "$EV/consumer-idempotency-*.json")"
if [ -z "$IDEM" ] || [ ! -s "$IDEM" ]; then
  block "找不到消费者幂等结果（$EV/consumer-idempotency-*.json）：库里没有去重/投递维度时**不得**用主键唯一性冒充"
else
  python3 - "$IDEM" <<'PY' > "$EV/.dr-gate-idem.env" 2>/dev/null
import json, sys
d = json.load(open(sys.argv[1]))
print("IDEM_VERDICT='%s'" % d.get("verdict", "MISSING"))
print("IDEM_DIM='%s'" % d.get("dimension", "MISSING"))
print("IDEM_TABLE='%s'" % d.get("table", "MISSING"))
print("IDEM_DUP='%s'" % d.get("duplicates_after", "MISSING"))
PY
  . "$EV/.dr-gate-idem.env" 2>/dev/null || true
  echo "  verdict=${IDEM_VERDICT:-?} 维度=${IDEM_DIM:-?} 表=${IDEM_TABLE:-?} 重复=${IDEM_DUP:-?}"
  [ "${IDEM_VERDICT:-}" = "PASS" ] || block "消费者幂等 verdict=${IDEM_VERDICT:-?}（必须为 PASS）"
  stale_check "consumer-idempotency" "$IDEM"
  case "${IDEM_TABLE:-}" in
    ""|"MISSING"|"none") block "消费者幂等没有真实落库维度（table=${IDEM_TABLE:-?}）：需产品侧先落 inbox/offset" ;;
    *) ok "消费者幂等维度已落库：${IDEM_TABLE}.${IDEM_DIM}" ;;
  esac
  [ "${IDEM_DUP:-1}" = "0" ] || block "消费者幂等存在重复处理记录：duplicates_after=${IDEM_DUP:-?}"
fi
echo

# ---------- 汇总 ----------
N="${#BLOCK_REASONS[@]}"
{
  echo "# S7-DR 聚合门禁结果（$TS）"
  echo
  echo "- 证据目录：$EV"
  echo "- 计划依据：§27 DATA-006/007/009、§30.2/§30.3、§31 证据段；acceptance-audit.md 第 10/11 条"
  echo "- 口径：**计划要求范围内的任何 NOT_VERIFIED（structural 或 check）与任何 PARTIAL 一律阻断**，不提供豁免"
  echo
  if [ "$N" -eq 0 ]; then
    echo "## 判定：PASS（0 条阻断项）"
  else
    echo "## 判定：BLOCKED（$N 条阻断项）—— S7-DR 未达验收"
    echo
    for r in "${BLOCK_REASONS[@]}"; do echo "- $r"; done
  fi
  echo
  echo "## 逐项明细"
  echo
  echo "| 项 | 值 |"
  echo "|---|---|"
  echo "| restore-drill verdict | ${RD_VERDICT:-（缺失）} |"
  echo "| restore-drill NV | ${RD_NV_N:-（缺失）}（structural ${RD_NV_STRUCT_N:-?} / check ${RD_NV_CHECK_N:-?}） |"
  echo "| restore-drill PARTIAL | ${RD_PARTIAL_N:-（缺失）} |"
  echo "| MinIO 已覆盖桶 | ${RD_MINIO_BUCKETS:-（缺失）} |"
  echo "| kill9-drill verdict | ${K9_VERDICT:-（缺失）} |"
  echo "| kill9-drill NV | ${K9_NV_N:-（缺失）} |"
  echo "| kill9-drill PARTIAL | ${K9_PARTIAL_N:-（缺失）} |"
  echo "| PITR verdict / archive_mode / RPO | ${PITR_VERDICT:-（缺失）} / ${PITR_ARCHIVE_MODE:-（缺失）} / ${PITR_RPO_S:-（缺失）}s |"
  echo "| MinIO 元数据 verdict / 维度 | ${META_VERDICT:-（缺失）} / ${META_DIMS:-（缺失）} |"
  echo "| 消费者幂等 verdict / 维度 | ${IDEM_VERDICT:-（缺失）} / ${IDEM_TABLE:-（缺失）}.${IDEM_DIM:-（缺失）} |"
} > "$OUT_MD"

python3 - "$OUT_JSON" "$OUT_MD" "$TS" "$N" "${BLOCK_REASONS[@]:-}" <<'PY'
import json, sys
out, md, ts, n = sys.argv[1:5]
reasons = [a for a in sys.argv[5:] if a]
json.dump({"gate": "s7-dr-gate", "ts": ts, "verdict": "PASS" if int(n) == 0 else "BLOCKED",
           "block_count": int(n), "block_reasons": reasons, "report": md,
           "policy": "计划要求范围内的任何 NOT_VERIFIED(任何分级) 与任何 PARTIAL 一律阻断，不提供豁免"},
          open(out, "w"), indent=2, ensure_ascii=False)
PY
rm -f "$EV/.dr-gate-rd.env" "$EV/.dr-gate-k9.env" "$EV/.dr-gate-pitr.env" "$EV/.dr-gate-meta.env" "$EV/.dr-gate-idem.env" 2>/dev/null || true

echo "gate report: $OUT_MD"
echo "gate result: $OUT_JSON"
if [ "$N" -gt 0 ]; then
  echo "S7-DR gate：BLOCKED（$N 条阻断项）—— 不得以任何局部 PASS 冒称 S7-DR 完成" >&2
  exit 1
fi
echo "S7-DR gate：PASS（0 条阻断项）"
exit 0
