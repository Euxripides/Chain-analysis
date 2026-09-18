#!/usr/bin/env bash
# audit-chain-gap-check.sh — 审计哈希链空洞巡检（已知空洞基线 + 新增空洞即告警）。
#
# 背景（2026-09-16 事故）：chain='login' 的 login_events 有 8 个 seq 从未写入（3791、3799
# 来自 2026-09-13；5911/5914/5915/5918/5919/5920 来自 2026-09-16 的 MFA 挑战事件越界）。
# 根因（链头先推进、行后写、且两步不同事务）已修，但这 8 个空洞**无法在系统设计内修复**：
#   * login_events 是 append-only（0043 触发器 + 0054 权限回收），改写需要维护模式；
#   * 完整性事件只能封存**连续**缺口（VerifyChain 会拒绝区间内仍有行的声明，见 anti-backfill）；
#   * 稀疏空洞按设计属于"真正篡改"一类，不允许被边界掩盖。
# 因此现实可做的是：**把已知空洞固化成基线，任何新增空洞立即失败**。
#
# 用法：
#   bash scripts/s7/audit-chain-gap-check.sh                 # 巡检（新增空洞 -> 退出码 1）
#   bash scripts/s7/audit-chain-gap-check.sh --update-baseline  # 人工确认后接受当前空洞集合
#
# 依赖：psql（容器内）或 docker exec；DSN 从 deploy/.env 读取（MIGRATION_DATABASE_URL）。
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
BASELINE="${BASELINE:-$R/deploy/audit-chain-gap-baseline.tsv}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/audit-gap-$TS}"
UPDATE=0
[ "${1:-}" = "--update-baseline" ] && UPDATE=1
mkdir -p "$EV"

psqlq(){ docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc "$1"; }

current="$EV/current-gaps.tsv"
: > "$current"
for chain in audit login; do
  table="login_events"; [ "$chain" = "audit" ] && table="audit_events_v2"
  psqlq "SELECT '$chain' || E'\\t' || g.seq FROM generate_series((SELECT min(seq) FROM $table WHERE chain='$chain'), (SELECT max(seq) FROM $table WHERE chain='$chain')) g(seq) LEFT JOIN $table e ON e.chain='$chain' AND e.seq=g.seq WHERE e.seq IS NULL ORDER BY 1" >> "$current"
done
sort -o "$current" "$current"
total=$(wc -l < "$current" | tr -d ' ')
echo "chain gaps now: $total  (evidence $EV)"

if [ "$UPDATE" = "1" ]; then
  cp "$current" "$BASELINE"
  echo "baseline updated: $BASELINE ($total 行)"
  exit 0
fi

if [ ! -f "$BASELINE" ]; then
  echo "BASELINE_MISSING $BASELINE —— 请先人工核对再用 --update-baseline 固化"
  exit 2
fi

new=$(comm -13 "$BASELINE" "$current" || true)
resolved=$(comm -23 "$BASELINE" "$current" || true)
known=$(comm -12 "$BASELINE" "$current" | wc -l | tr -d ' ')

[ -n "$resolved" ] && { echo "INFO 已消失的空洞（可能被修复或回填，需要人工确认）:"; echo "$resolved"; }
echo "known=$known"
if [ -n "$new" ]; then
  echo "FAIL 新增审计链空洞（审计完整性事件，必须立即定位写入路径）:"
  echo "$new"
  # 链校验（只读）作为第二证据
  ( cd "$R" && set -a && . deploy/.env 2>/dev/null; set +a; \
    cd backend && PATH=/usr/local/go/bin:$PATH DATABASE_URL="$MIGRATION_DATABASE_URL" \
    go run ./cmd/auditverify ) > "$EV/auditverify.json" 2>&1 || true
  echo "GAP_CHECK=FAIL"
  exit 1
fi
echo "PASS 未发现新增空洞（已知 $known 处，基线 $BASELINE）"
echo "GAP_CHECK=PASS"
exit 0
