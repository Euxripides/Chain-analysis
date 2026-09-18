#!/usr/bin/env bash
# =====================================================================
# §16.11 / §17「回滚 实测 PASS」：部署回滚演练
#
# 演练内容（全部是真实动作，不是"检查脚本存在"）：
#   阶段 A  记录基线：API/Worker/migrate 二进制哈希、两套前端 dist/index.html 哈希、
#           单元状态、/health/live 与 /health/ready、两个控制台与 TLS 入口的 HTTP 状态。
#   阶段 B  回滚 API + Worker：把**上一版**二进制装回 bin/，重启单元，等待健康，测回滚 RTO。
#   阶段 C  回滚前端：把 dist 换成 dist.prev-*，重启控制台单元，测回滚 RTO，并校验**服务端下发的**入口哈希等于上一版。
#   阶段 D  回滚后再前进：恢复冻结版本二进制与 dist，重启，校验哈希与基线**逐条一致**、健康恢复。
# 任何一步断言失败：标记 FAIL，并在退出前**尽力把系统恢复到冻结版本**（trap 保证）。
#
# 用法：bash scripts/release/rollback-drill.sh
# 环境变量：
#   ROLLBACK_API_SRC / ROLLBACK_WORKER_SRC  上一版二进制（默认 ~/deploy-backups/tron-address-catalog-20260909/{api,worker}）
#   ROLLBACK_UC_DIST / ROLLBACK_AC_DIST     上一版 dist 目录（默认取最新 dist.prev-*）
#   ROLLBACK_TIMEOUT                        等待健康的上限秒数（默认 180）
#   ROLLBACK_SKIP_FRONTEND                  1 = 只演练后端回滚（报告中显式标注未覆盖前端）
# 输出：acceptance-evidence/dual-system-v1/sprint7/release/rollback-drill-<TS>.md / .json / .logs/
# 退出码：0 = 回滚与回滚后再前进都实测成功且最终状态=冻结版本；1 = FAIL；2 = 前置/用法错误
# =====================================================================
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
BIN="$PROJECT/backend/bin"
FE="$PROJECT/frontend/apps"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/release}"
# 默认回滚源 = **已实测可启动**的上一版归档（见 docs/manuals/rollback-procedure.md §1）。
# 反例：~/deploy-backups/tron-address-catalog-20260909/{api,worker} 实测启动即失败
#   bootstrap failed: create schema_migrations: permission denied for schema public (42501)
# （该版本启动需 DDL，而运行时角色已按迁移 0054 降权）—— 因此不得作为默认回滚源。
API_SRC="${ROLLBACK_API_SRC:-$HOME/deploy-backups/rollback-20260916T1735Z/api.previous}"
WORKER_SRC="${ROLLBACK_WORKER_SRC:-$HOME/deploy-backups/rollback-20260916T1735Z/worker.previous}"
TIMEOUT="${ROLLBACK_TIMEOUT:-180}"
SKIP_FE="${ROLLBACK_SKIP_FRONTEND:-0}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
LOG_DIR="$EV/rollback-drill-$TS.logs"
REPORT="$EV/rollback-drill-$TS.md"
RESULT="$EV/rollback-drill-$TS.json"
WORK="$(mktemp -d /tmp/s7-rollback.XXXXXX)" || { echo "ABORT: 无法创建临时目录" >&2; exit 2; }
FAILS=0
FENCE='```'

fail() { FAILS=$((FAILS+1)); echo "FAIL: $*" | tee -a "$REPORT" >&2; }
note() { echo "$*" >> "$REPORT"; }
sha()  { sha256sum "$1" 2>/dev/null | cut -d" " -f1; }
unit_state() { systemctl --user is-active "$1" 2>/dev/null || true; }
wait_health() { # wait_health <url> <timeout_s> -> 0/1，输出耗时秒
  local url="$1" limit="$2" t0 t1 code
  t0="$(date +%s.%N)"
  while :; do
    code="$(curl -s -o /dev/null -m 3 -w "%{http_code}" "$url" 2>/dev/null || echo 000)"
    [ "$code" = "200" ] && break
    t1="$(date +%s.%N)"
    awk -v a="$t1" -v b="$t0" -v l="$limit" 'BEGIN{exit !((a-b) < l)}' || return 1
    sleep 1
  done
  t1="$(date +%s.%N)"
  printf '%.2f' "$(awk -v a="$t1" -v b="$t0" 'BEGIN{print a-b}')"
  return 0
}
served_hash() { # served_hash <url> -> sha256 of response body
  curl -s -m 10 "$1" 2>/dev/null | sha256sum | cut -d" " -f1
}

mkdir -p "$EV" "$LOG_DIR" || { echo "ABORT: 无法创建 $EV" >&2; exit 2; }
for f in "$BIN/api" "$BIN/worker" "$API_SRC" "$WORKER_SRC"; do
  [ -f "$f" ] || { echo "ABORT: 缺少 $f" >&2; exit 2; }
done
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  systemctl --user cat "$u" >/dev/null 2>&1 || { echo "ABORT: 单元不存在：$u" >&2; exit 2; }
done

# 上一版 dist（默认取最新的 dist.prev-*）
UC_DIST="${ROLLBACK_UC_DIST:-$(ls -dt "$FE"/user-console/dist.prev-* 2>/dev/null | head -n 1)}"
AC_DIST="${ROLLBACK_AC_DIST:-$(ls -dt "$FE"/admin-console/dist.prev-* 2>/dev/null | head -n 1)}"

# ---------- 阶段 A：基线 ----------
echo "== 阶段 A：记录基线 ==" > "$REPORT"
{
  echo "# 部署回滚演练（$TS）"
  echo
  echo "- 演练目标：**回滚实测可用**（§16.11/§17），且回滚后能再前进回冻结版本"
  echo "- 上一版 API/Worker：\`$API_SRC\` / \`$WORKER_SRC\`"
  echo "- 上一版前端 dist：\`${UC_DIST:-（未提供）}\` / \`${AC_DIST:-（未提供）}\`"
  echo "- 日志目录：\`$LOG_DIR\`"
  echo
  echo "## 1. 基线（冻结版本）"
  echo
  echo "| 项 | 值 |"
  echo "|---|---|"
} > "$REPORT"
BASE_API="$(sha "$BIN/api")"; BASE_WORKER="$(sha "$BIN/worker")"; BASE_MIGRATE="$(sha "$BIN/migrate")"
OLD_API="$(sha "$API_SRC")"; OLD_WORKER="$(sha "$WORKER_SRC")"
[ "$BASE_API" != "$OLD_API" ] || fail "上一版 API 与当前 API 哈希相同，演练无意义（$BASE_API）"
[ "$BASE_WORKER" != "$OLD_WORKER" ] || fail "上一版 Worker 与当前 Worker 哈希相同（$BASE_WORKER）"
BASE_UC_INDEX="$(sha "$FE/user-console/dist/index.html")"
BASE_AC_INDEX="$(sha "$FE/admin-console/dist/index.html")"
API_LIVE_RTO="$(wait_health http://127.0.0.1:8080/health/live 30 || echo timeout)"
API_READY="$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:8080/health/ready || echo 000)"
UC_CODE="$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:3001/app-v2/ || echo 000)"
AC_CODE="$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:3002/admin/ || echo 000)"
TLS_CODE="$(curl -sk -o /dev/null -m 5 -w "%{http_code}" https://127.0.0.1:8448/ || echo 000)"
[ "$API_READY" = "200" ] || fail "基线 /health/ready 非 200（$API_READY）"
[ "$UC_CODE" = "200" ] || fail "基线用户台非 200（$UC_CODE）"
[ "$AC_CODE" = "200" ] || fail "基线后台台非 200（$AC_CODE）"
{
  echo "| API 二进制 | \`$BASE_API\`（单元 $(unit_state blockchain-api)） |"
  echo "| Worker 二进制 | \`$BASE_WORKER\`（单元 $(unit_state blockchain-worker)） |"
  echo "| migrate 二进制 | \`$BASE_MIGRATE\` |"
  echo "| 用户台 / 后台台 dist 入口 | \`$BASE_UC_INDEX\` / \`$BASE_AC_INDEX\` |"
  echo "| 基线服务状态 | /health/live 200（${API_LIVE_RTO}s）、/health/ready $API_READY、用户台 $UC_CODE、后台台 $AC_CODE、TLS 入口 $TLS_CODE |"
  echo "| 上一版 API / Worker | \`$OLD_API\` / \`$OLD_WORKER\` |"
} >> "$REPORT"

# 冻结版本的现场快照（阶段 D 要用它把系统恢复回去）
cp -f "$BIN/api" "$WORK/api.frozen"; cp -f "$BIN/worker" "$WORK/worker.frozen"
RESTORED=0
restore_frozen() {
  [ "$RESTORED" = "1" ] && return 0
  cp -f "$WORK/api.frozen" "$BIN/api" 2>/dev/null || true
  cp -f "$WORK/worker.frozen" "$BIN/worker" 2>/dev/null || true
  chmod +x "$BIN/api" "$BIN/worker" 2>/dev/null || true
  # 前端也要还原：阶段 C 把 dist 移到了 $WORK，异常退出时必须放回去（否则控制台会 404）
  if [ -d "$WORK/uc.dist.frozen" ]; then
    rm -rf "$FE/user-console/dist" 2>/dev/null || true
    cp -a "$WORK/uc.dist.frozen" "$FE/user-console/dist" 2>/dev/null || true
    systemctl --user restart blockchain-user-console > "$LOG_DIR/restore-fe.log" 2>&1 || true
  fi
  if [ -d "$WORK/ac.dist.frozen" ]; then
    rm -rf "$FE/admin-console/dist" 2>/dev/null || true
    cp -a "$WORK/ac.dist.frozen" "$FE/admin-console/dist" 2>/dev/null || true
    systemctl --user restart blockchain-admin-console >> "$LOG_DIR/restore-fe.log" 2>&1 || true
  fi
  systemctl --user restart blockchain-api blockchain-worker > "$LOG_DIR/restore-restart.log" 2>&1 || true
  RESTORED=1
}
cleanup() {
  if [ "$RESTORED" != "1" ]; then
    echo "== 异常退出：尽力恢复冻结版本 ==" | tee -a "$REPORT" >&2
    restore_frozen
    wait_health http://127.0.0.1:8080/health/live 60 >/dev/null || true
  fi
  rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

# ---------- 阶段 B：后端回滚 ----------
echo "== 阶段 B：后端回滚（API + Worker）==" | tee -a "$REPORT"
echo "rollback: stop api/worker" >> "$LOG_DIR/actions.log"
systemctl --user stop blockchain-api blockchain-worker > "$LOG_DIR/stop.log" 2>&1 || fail "停止 API/Worker 失败"
T0="$(date +%s.%N)"
cp -f "$API_SRC" "$BIN/api" || fail "安装上一版 API 失败（源：$API_SRC）"
cp -f "$WORKER_SRC" "$BIN/worker" || fail "安装上一版 Worker 失败（源：$WORKER_SRC）"
chmod +x "$BIN/api" "$BIN/worker"
systemctl --user start blockchain-api blockchain-worker > "$LOG_DIR/start-old.log" 2>&1 || fail "启动上一版 API/Worker 失败"
RB_RTO="$(wait_health http://127.0.0.1:8080/health/live "$TIMEOUT" || echo timeout)"
[ "$RB_RTO" != "timeout" ] || fail "回滚后 API 在 ${TIMEOUT}s 内未恢复健康"
RB_API="$(sha "$BIN/api")"; RB_WORKER="$(sha "$BIN/worker")"
[ "$RB_API" = "$OLD_API" ] || fail "回滚后 API 哈希不符（$RB_API != $OLD_API）"
[ "$RB_WORKER" = "$OLD_WORKER" ] || fail "回滚后 Worker 哈希不符（$RB_WORKER != $OLD_WORKER）"
RB_READY="$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:8080/health/ready || echo 000)"
[ "$RB_READY" = "200" ] || fail "回滚后 /health/ready 非 200（$RB_READY）"
{
  echo
  echo "## 2. 回滚到上一版（实测）"
  echo
  echo "- 动作：\`systemctl --user stop blockchain-api blockchain-worker\` → 覆盖 \`backend/bin/{api,worker}\` → \`start\`"
  echo "- **回滚 RTO（停止到 /health/live 200）= ${RB_RTO} s**（上限 ${TIMEOUT}s）"
  echo "- 回滚后哈希：api \`$RB_API\`、worker \`$RB_WORKER\`（与上一版逐字节一致）"
  echo "- 回滚后 /health/ready：$RB_READY"
  echo "- 结论：**上一版在现网数据库/配置上可以真实拉起并对外服务**（不是「理论上可回滚」）"
} >> "$REPORT"

if [ "$SKIP_FE" = "1" ]; then
  note ""
  note "## 3. 前端回滚：**本次未覆盖**（ROLLBACK_SKIP_FRONTEND=1）——不得据此宣称前端可回滚"
else
  echo "== 阶段 C：前端回滚 ==" | tee -a "$REPORT"
  if [ -z "$UC_DIST" ] || [ -z "$AC_DIST" ] || [ ! -d "$UC_DIST" ] || [ ! -d "$AC_DIST" ]; then
    fail "找不到上一版 dist（uc=${UC_DIST:-空} / ac=${AC_DIST:-空}）：前端回滚未覆盖"
  else
    OLD_UC_INDEX="$(sha "$UC_DIST/index.html")"; OLD_AC_INDEX="$(sha "$AC_DIST/index.html")"
    [ "$OLD_UC_INDEX" != "$BASE_UC_INDEX" ] || fail "上一版用户台入口与当前相同，前端回滚演练无意义"
    systemctl --user stop blockchain-user-console blockchain-admin-console > "$LOG_DIR/fe-stop.log" 2>&1 || fail "停止控制台单元失败"
    T0="$(date +%s.%N)"
    mv "$FE/user-console/dist" "$WORK/uc.dist.frozen" && cp -a "$UC_DIST" "$FE/user-console/dist" || fail "切换用户台 dist 失败"
    mv "$FE/admin-console/dist" "$WORK/ac.dist.frozen" && cp -a "$AC_DIST" "$FE/admin-console/dist" || fail "切换后台台 dist 失败"
    systemctl --user start blockchain-user-console blockchain-admin-console > "$LOG_DIR/fe-start-old.log" 2>&1 || fail "启动控制台单元失败"
    UC_RTO="$(wait_health http://127.0.0.1:3001/app-v2/ "$TIMEOUT" || echo timeout)"
    [ "$UC_RTO" != "timeout" ] || fail "回滚后用户台在 ${TIMEOUT}s 内未恢复"
    AC_RTO="$(wait_health http://127.0.0.1:3002/admin/ "$TIMEOUT" || echo timeout)"
    [ "$AC_RTO" != "timeout" ] || fail "回滚后后台台在 ${TIMEOUT}s 内未恢复"
    SERVED_UC="$(served_hash http://127.0.0.1:3001/app-v2/)"
    SERVED_AC="$(served_hash http://127.0.0.1:3002/admin/)"
    [ "$SERVED_UC" = "$OLD_UC_INDEX" ] || fail "回滚后服务端下发的用户台入口哈希与上一版不一致（$SERVED_UC != $OLD_UC_INDEX）"
    [ "$SERVED_AC" = "$OLD_AC_INDEX" ] || fail "回滚后服务端下发的后台台入口哈希与上一版不一致（$SERVED_AC != $OLD_AC_INDEX）"
    {
      echo
      echo "## 3. 前端回滚（实测）"
      echo
      echo "- 上一版 dist：\`$UC_DIST\` / \`$AC_DIST\`"
      echo "- **回滚 RTO**：用户台 ${UC_RTO} s、后台台 ${AC_RTO} s"
      echo "- 服务端下发的入口哈希：用户台 \`$SERVED_UC\`、后台台 \`$SERVED_AC\`（等于上一版磁盘入口哈希）"
      echo "- 结论：前端**换版可回滚且可观测**（服务端实际下发 = 上一版产物）"
    } >> "$REPORT"
  fi
fi

# ---------- 阶段 D：回滚后再前进 ----------
echo "== 阶段 D：回滚后再前进（恢复冻结版本）==" | tee -a "$REPORT"
T0="$(date +%s.%N)"
systemctl --user stop blockchain-api blockchain-worker > "$LOG_DIR/stop2.log" 2>&1 || fail "停止 API/Worker 失败（前滚）"
cp -f "$WORK/api.frozen" "$BIN/api" || fail "恢复冻结 API 失败"
cp -f "$WORK/worker.frozen" "$BIN/worker" || fail "恢复冻结 Worker 失败"
chmod +x "$BIN/api" "$BIN/worker"
systemctl --user start blockchain-api blockchain-worker > "$LOG_DIR/start-frozen.log" 2>&1 || fail "启动冻结版 API/Worker 失败"
FW_RTO="$(wait_health http://127.0.0.1:8080/health/live "$TIMEOUT" || echo timeout)"
[ "$FW_RTO" != "timeout" ] || fail "前滚后 API 在 ${TIMEOUT}s 内未恢复健康"
RESTORED=1
if [ "$SKIP_FE" != "1" ] && [ -d "$WORK/uc.dist.frozen" ]; then
  systemctl --user stop blockchain-user-console blockchain-admin-console > "$LOG_DIR/fe-stop2.log" 2>&1 || fail "停止控制台失败（前滚）"
  rm -rf "$FE/user-console/dist" "$FE/admin-console/dist"
  cp -a "$WORK/uc.dist.frozen" "$FE/user-console/dist" || fail "恢复用户台 dist 失败"
  cp -a "$WORK/ac.dist.frozen" "$FE/admin-console/dist" || fail "恢复后台台 dist 失败"
  systemctl --user start blockchain-user-console blockchain-admin-console > "$LOG_DIR/fe-start-frozen.log" 2>&1 || fail "启动控制台失败（前滚）"
  wait_health http://127.0.0.1:3001/app-v2/ "$TIMEOUT" >/dev/null || fail "前滚后用户台未恢复"
  wait_health http://127.0.0.1:3002/admin/ "$TIMEOUT" >/dev/null || fail "前滚后后台台未恢复"
fi
FW_API="$(sha "$BIN/api")"; FW_WORKER="$(sha "$BIN/worker")"; FW_MIGRATE="$(sha "$BIN/migrate")"
FW_UC_INDEX="$(sha "$FE/user-console/dist/index.html")"; FW_AC_INDEX="$(sha "$FE/admin-console/dist/index.html")"
[ "$FW_API" = "$BASE_API" ] || fail "前滚后 API 哈希与基线不一致（$FW_API != $BASE_API）"
[ "$FW_WORKER" = "$BASE_WORKER" ] || fail "前滚后 Worker 哈希与基线不一致"
[ "$FW_MIGRATE" = "$BASE_MIGRATE" ] || fail "前滚后 migrate 哈希与基线不一致"
[ "$FW_UC_INDEX" = "$BASE_UC_INDEX" ] || fail "前滚后用户台入口哈希与基线不一致"
[ "$FW_AC_INDEX" = "$BASE_AC_INDEX" ] || fail "前滚后后台台入口哈希与基线不一致"
FW_READY="$(curl -s -o /dev/null -m 5 -w "%{http_code}" http://127.0.0.1:8080/health/ready || echo 000)"
FW_TLS="$(curl -sk -o /dev/null -m 5 -w "%{http_code}" https://127.0.0.1:8448/ || echo 000)"
[ "$FW_READY" = "200" ] || fail "前滚后 /health/ready 非 200（$FW_READY）"
[ "$FW_TLS" = "200" ] || fail "前滚后 TLS 入口非 200（$FW_TLS）"
{
  echo
  echo "## 4. 回滚后再前进（恢复冻结版本，实测）"
  echo
  echo "- **前滚 RTO = ${FW_RTO} s**"
  echo "- 前滚后哈希：api \`$FW_API\`、worker \`$FW_WORKER\`、migrate \`$FW_MIGRATE\`（与基线逐条一致）"
  echo "- 前滚后前端入口：用户台 \`$FW_UC_INDEX\`、后台台 \`$FW_AC_INDEX\`（与基线一致）"
  echo "- 前滚后 /health/ready $FW_READY、TLS 入口 $FW_TLS"
} >> "$REPORT"

# ---------- 汇总 ----------
if [ "$FAILS" -eq 0 ]; then VERDICT="PASS"; else VERDICT="FAIL"; fi
{
  echo
  echo "## 5. 判定"
  echo
  echo "- 回滚 RTO：后端 ${RB_RTO:-未测} s；前端 ${UC_RTO:-未测} / ${AC_RTO:-未测} s；前滚 RTO ${FW_RTO:-未测} s"
  echo "- 断言失败：**$FAILS** 条"
  echo "- **本次演练判定：$VERDICT**"
  if [ "$FAILS" -gt 0 ]; then
    echo
    echo "> 失败项见本文件中的 FAIL 行；系统已在退出前尽力恢复到冻结版本（最终哈希见 §4）。"
  fi
  echo
  echo "- 说明：演练期间 API/Worker/控制台会短暂中断（重启窗口），这是回滚演练的固有代价；中断时长即上表 RTO。"
} >> "$REPORT"

python3 - "$RESULT" "$TS" "$VERDICT" "$FAILS" "$BASE_API" "$BASE_WORKER" "$BASE_MIGRATE" "$OLD_API" "$OLD_WORKER" \
  "$RB_RTO" "$FW_RTO" "$FW_API" "$FW_WORKER" "$LOG_DIR" <<'PY'
import json, sys
(out, ts, verdict, fails, base_api, base_worker, base_migrate, old_api, old_worker, rb_rto, fw_rto, fw_api, fw_worker, log_dir) = sys.argv[1:15]
res = {"drill": "rollback-drill", "ts": ts, "verdict": verdict, "failures": int(fails),
       "frozen_api_sha256": base_api, "frozen_worker_sha256": base_worker, "frozen_migrate_sha256": base_migrate,
       "previous_api_sha256": old_api, "previous_worker_sha256": old_worker,
       "rto_seconds": {"rollback_backend": rb_rto, "rollforward_backend": fw_rto},
       "after_rollforward": {"api": fw_api, "worker": fw_worker},
       "logs_dir": log_dir}
json.dump(res, open(out, "w"), indent=2, ensure_ascii=False)
print(json.dumps(res, indent=2, ensure_ascii=False))
PY
echo "report: $REPORT"
echo "result: $RESULT"
if [ "$FAILS" -gt 0 ]; then echo "回滚演练判定：FAIL（$FAILS 条）" >&2; exit 1; fi
echo "回滚演练判定：PASS（回滚 + 前滚均实测可用，最终状态=冻结版本）"
exit 0