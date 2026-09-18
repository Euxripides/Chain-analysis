#!/usr/bin/env bash
# udl01-deploy-v3-negative-calibration.sh — 对 udl01-deploy-user-console-v3.sh 的**负向校准**（隔离环境）。
#
# 纪律：全部用例跑在隔离临时目录 + 可控服务替身（python http.server 于 127.0.0.1 非生产端口），
# **绝不**向真实用户服务（blockchain-user-console / 8448）注入故障；所有产物保留不删除。
#
# 用例与预期：
#   C1 缺 page chunk            -> rc=2 PRECHECK_FAIL，现网未动（无 new/prev/failed）
#   C2 缺 worker chunk          -> rc=2 同上
#   C3 复制失败（父目录只读）    -> rc 非 0，现网未动（切换未开始）
#   C4 重启首次失败（回滚重启成功）-> rc 非 0，rollback_attempted=1 / rollback_rc=0 / old_restored=1，
#                                   失败新目录保留，served 回到旧 SHA
#   C5 正向对照（全绿）          -> rc=0 PASS，served 真实切换为新 SHA
#   C6 重启始终失败              -> rc 非 0，rollback_attempted=1，rollback_rc 非 0（NO-GO），
#                                   磁盘内容已恢复为旧 index 且 served 一致（诚实报告，不假称回滚成功）
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
DEPLOY="$R/scripts/s7/udl01-deploy-user-console-v3.sh"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-deploy-v3-negcal-$TS}"
PORT="${NEGCAL_PORT:-18771}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
LOG="$EV/negcal.log"
echo "evidence=$EV port=$PORT deploy_sha=$(sha256sum "$DEPLOY" | cut -d" " -f1)" | tee "$LOG"
CASE_FAILS=0
CASE_ERRS=0
# 根 2026-09-13 独立复核发现的真实缺陷：旧版对 C1-C6 无条件打印 NEGCAL_PASS，
# 与同一用例的 NEGCAL_FAIL 构成矛盾证据。现在每个用例单独计数，只有 0 错误才打印 PASS。
case_begin(){ CASE_ERRS=0; }
case_fail(){ CASE_FAILS=$((CASE_FAILS+1)); CASE_ERRS=$((CASE_ERRS+1)); echo "NEGCAL_FAIL $1 :: $2" | tee -a "$LOG"; }
case_ok(){ echo "NEGCAL_PASS $1 :: $2" | tee -a "$LOG"; }
case_end(){ if [ "$CASE_ERRS" = 0 ]; then case_ok "$1" "$2"; else case_fail "$1" "该用例存在未通过断言（errs=$CASE_ERRS），不打印 PASS"; fi; }

mkfile(){ mkdir -p "$(dirname "$1")"; printf "%s" "$2" > "$1"; }
sha_of(){ if [ -f "$1" ]; then sha256sum "$1" | cut -d" " -f1; else echo ""; fi; }

mkroot(){
  local root="$1"
  mkfile "$root/live/index.html" "<!doctype html><html><body>OLD-INDEX</body></html>"
  mkfile "$root/live/assets/DownloadNewPage-AAA.js" "old page chunk; worker=import-parse.worker-DDD.js"
  mkfile "$root/live/assets/import-parse.worker-DDD.js" "old worker body"
  mkfile "$root/staged/index.html" "<!doctype html><html><body>NEW-INDEX</body></html>"
  mkfile "$root/staged/assets/DownloadNewPage-BBB.js" "new page chunk; import-parse.worker-CCC.js; 毫秒; 内没有完成"
  mkfile "$root/staged/assets/import-parse.worker-CCC.js" "new worker body"
}

# 服务替身管理（本校准自身真实缺陷：早前版本未校验端口占用，前一个用例的替身未退出时，
# 后续用例的新替身绑定失败却仍在用**旧替身**的根目录服务，导致 C5 正向对照被误判为失败）。
port_busy(){ ss -ltn 2>/dev/null | grep -q ":$PORT "; }
wait_port_free(){ local i=0; for i in $(seq 1 60); do if ! port_busy; then return 0; fi; sleep 0.25; done; return 1; }
start_standin(){
  if port_busy; then echo "PORT_BUSY_BEFORE_START $PORT" >> "$2/standin.log"; return 1; fi
  ( exec python3 "$R/scripts/s7/udl01-negcal-standin.py" "$PORT" "$1" ) > "$2/standin.log" 2>&1 &
  echo $! > "$2/standin.pid"
  local i=0
  for i in $(seq 1 40); do
    if curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:$PORT/live/" | grep -q 200; then
      if grep -q "Address already in use" "$2/standin.log"; then return 1; fi
      return 0
    fi
    sleep 0.5
  done
  return 1
}
stop_standin(){
  if [ -f "$1/standin.pid" ]; then kill "$(cat "$1/standin.pid")" 2>/dev/null || true; fi
  if ! wait_port_free; then echo "PORT_STILL_BUSY_AFTER_STOP $PORT" >> "$LOG"; CASE_FAILS=$((CASE_FAILS+1)); fi
}

mk_restart_script(){
  mkfile "$1/restart.sh" "#!/usr/bin/env bash"$'\n'"C=\"\$1\""$'\n'"if [ -f \"\$C\" ]; then exit 0; fi"$'\n'"touch \"\$C\""$'\n'"exit 1"$'\n'
  chmod 755 "$1/restart.sh"
}

run_case(){
  RESULT_NAME=""; RESULT_RC=""; RESULT_OLD=""; RESULT_AFTER=""
  RESULT_SERVED_BEFORE=""; RESULT_SERVED_AFTER=""; RESULT_EXTRAS=""; RESULT_EV=""; RESULT_ROOT=""
  local name="$1"; local restart_mode="$2"; local staged_variant="$3"
  local root="$EV/$name/root"; local cev="$EV/$name/evidence"
  mkdir -p "$EV/$name"; chmod 700 "$EV/$name"
  mkroot "$root"
  mkdir -p "$root/withheld"
  if [ "$staged_variant" = "no-page" ]; then mv "$root/staged/assets/DownloadNewPage-BBB.js" "$root/withheld/DownloadNewPage-BBB.js"; fi
  if [ "$staged_variant" = "no-worker" ]; then mv "$root/staged/assets/import-parse.worker-CCC.js" "$root/withheld/import-parse.worker-CCC.js"; fi
  local RC="true"
  if [ "$restart_mode" = "fail-once" ]; then mk_restart_script "$EV/$name"; RC="bash $EV/$name/restart.sh $EV/$name/restart.counter"; fi
  if [ "$restart_mode" = "always-fail" ]; then RC="false"; fi
  if [ "$staged_variant" = "ro-parent" ]; then chmod 500 "$root"; fi
  local old_sha; old_sha="$(sha_of "$root/live/index.html")"
  start_standin "$root" "$EV/$name" || { case_fail "$name" "服务替身未就绪"; return; }
  local served_before; served_before="$(curl -s "http://127.0.0.1:$PORT/live/" | sha256sum | cut -d" " -f1)"
  # 替身必须真的提供 CSP 头，否则 C5 的 P16 门禁无法被真实触发（假通过风险）
  if [ "$(curl -s -D - -o /dev/null "http://127.0.0.1:$PORT/live/" | grep -ci "content-security-policy")" -lt 1 ]; then case_fail "$name" "替身未提供 CSP 头，P16 无法被真实触发"; fi
  # 归属校验：替身服务的必须是**本用例自己**的 live 目录（防止旧替身串场造成假结论）
  if [ "$served_before" != "$old_sha" ]; then case_fail "$name" "替身串场：served_before=$served_before 不等于本用例 live index=$old_sha"; stop_standin "$EV/$name"; return; fi
  STAGED="$root/staged" EV="$cev" DEPLOY_DIST_DIR="$root/live" \
    DEPLOY_SERVED_URL="http://127.0.0.1:$PORT/live/" \
    DEPLOY_ASSET_BASE="http://127.0.0.1:$PORT/live/assets" \
    DEPLOY_CURL_OPTS="" DEPLOY_ACTIVE_CMD="true" DEPLOY_RESTART_CMD="$RC" DEPLOY_READY_TRIES=3 \
    bash "$DEPLOY" > "$EV/$name/stdout.txt" 2>&1
  local rc=$?
  if [ "$staged_variant" = "ro-parent" ]; then chmod 700 "$root"; fi
  local after_sha; after_sha="$(sha_of "$root/live/index.html")"
  local served_after; served_after="$(curl -s "http://127.0.0.1:$PORT/live/" | sha256sum | cut -d" " -f1)"
  local extras; extras="$(ls -d "$root"/live.new-* "$root"/live.prev-* "$root"/live.failed-* "$root"/live.bak-* 2>/dev/null | wc -l)"
  stop_standin "$EV/$name"
  printf "case=%s rc=%s restart_mode=%s variant=%s live_before=%s live_after=%s served_before=%s served_after=%s extras=%s\n" \
    "$name" "$rc" "$restart_mode" "$staged_variant" "$old_sha" "$after_sha" "$served_before" "$served_after" "$extras" >> "$LOG"
  RESULT_NAME="$name"; RESULT_RC="$rc"; RESULT_OLD="$old_sha"; RESULT_AFTER="$after_sha"
  RESULT_SERVED_BEFORE="$served_before"; RESULT_SERVED_AFTER="$served_after"; RESULT_EXTRAS="$extras"
  RESULT_EV="$cev"; RESULT_ROOT="$root"
}

echo "=== C1 缺 page chunk ==="
run_case C1-missing-page-chunk ok no-page
case_begin
if [ "$RESULT_RC" != 2 ]; then case_fail C1 "rc=$RESULT_RC != 2"; fi
if [ "$RESULT_AFTER" != "$RESULT_OLD" ]; then case_fail C1 "现网 index 被改动"; fi
if [ "$RESULT_EXTRAS" != 0 ]; then case_fail C1 "产生了 new/prev/failed/bak 目录 extras=$RESULT_EXTRAS"; fi
if [ "$RESULT_SERVED_AFTER" != "$RESULT_SERVED_BEFORE" ]; then case_fail C1 "served 被改动"; fi
grep -q "UDL01_DEPLOY_VERDICT=PRECHECK_FAIL" "$EV/C1-missing-page-chunk/stdout.txt" || case_fail C1 "缺少 PRECHECK_FAIL 判词"
case_end C1 "预检 fail-closed、现网未触碰"

echo "=== C2 缺 worker chunk ==="
run_case C2-missing-worker-chunk ok no-worker
case_begin
if [ "$RESULT_RC" != 2 ]; then case_fail C2 "rc=$RESULT_RC != 2"; fi
if [ "$RESULT_AFTER" != "$RESULT_OLD" ]; then case_fail C2 "现网 index 被改动"; fi
if [ "$RESULT_EXTRAS" != 0 ]; then case_fail C2 "产生了 new/prev/failed/bak 目录 extras=$RESULT_EXTRAS"; fi
case_end C2 "预检 fail-closed、现网未触碰"

echo "=== C3 复制失败（父目录只读） ==="
run_case C3-copy-failure ok ro-parent
case_begin
if [ "$RESULT_RC" != 3 ]; then case_fail C3 "复制失败 rc=$RESULT_RC != 3"; fi
if [ "$RESULT_AFTER" != "$RESULT_OLD" ]; then case_fail C3 "现网 index 被改动"; fi
if [ "$RESULT_SERVED_AFTER" != "$RESULT_SERVED_BEFORE" ]; then case_fail C3 "served 被改动"; fi
case_end C3 "复制失败时现网未切换（rc=$RESULT_RC）"

echo "=== C4 重启首次失败（回滚重启成功） ==="
run_case C4-restart-fail-once fail-once good
case_begin
if [ "$RESULT_RC" = 0 ]; then case_fail C4 "部署重启失败却返回 0"; fi
if [ "$RESULT_AFTER" != "$RESULT_OLD" ]; then case_fail C4 "回滚后现网 index 未恢复"; fi
if [ "$RESULT_SERVED_AFTER" != "$RESULT_SERVED_BEFORE" ]; then case_fail C4 "回滚后 served 未恢复旧 SHA"; fi
grep -q "rollback_attempted=1" "$RESULT_EV/deploy-result.txt" || case_fail C4 "未记录 rollback_attempted=1"
grep -q "rollback_rc=0" "$RESULT_EV/deploy-result.txt" || case_fail C4 "rollback_rc 非 0"
grep -q "old_restored=1" "$RESULT_EV/deploy-result.txt" || case_fail C4 "未记录 old_restored=1"
if [ "$(ls -d "$RESULT_ROOT"/live.failed-* 2>/dev/null | wc -l)" = 0 ]; then case_fail C4 "失败的新目录未保留"; fi
case_end C4 "重启失败自动回滚成功并验证旧 served SHA"

echo "=== C5 正向对照（全绿必须 PASS） ==="
run_case C5-positive-control ok good
case_begin
if [ "$RESULT_RC" != 0 ]; then case_fail C5 "全绿却 rc=$RESULT_RC"; fi
grep -q "UDL01_DEPLOY_VERDICT=PASS" "$EV/C5-positive-control/stdout.txt" || case_fail C5 "缺少 PASS 判词"
if [ "$RESULT_SERVED_AFTER" = "$RESULT_SERVED_BEFORE" ]; then case_fail C5 "切换后 served 未变化（假通过）"; fi
if [ "$RESULT_AFTER" = "$RESULT_OLD" ]; then case_fail C5 "切换后现网 index 未更新"; fi
grep -q "backup_verified=1" "$RESULT_EV/deploy-result.txt" || case_fail C5 "成功路径未记录备份逐字节校验"
grep -q "deploy_rc=0" "$RESULT_EV/deploy-result.txt" || case_fail C5 "deploy-result 未记录 deploy_rc=0"
grep -q "rollback_attempted=0" "$RESULT_EV/deploy-result.txt" || case_fail C5 "deploy-result 未记录 rollback_attempted=0"
grep -q "PASS P16 :: CSP 头在位" "$RESULT_EV/deploy.log" || case_fail C5 "P16（CSP 头）未 PASS"
case_end C5 "正向对照 PASS 且 served 真实切换"

echo "=== C6 重启始终失败（回滚也失败必须 NO-GO） ==="
run_case C6-rollback-restart-fails always-fail good
case_begin
if [ "$RESULT_RC" = 0 ]; then case_fail C6 "部署重启始终失败却返回 0"; fi
grep -q "rollback_attempted=1" "$RESULT_EV/deploy-result.txt" || case_fail C6 "未记录 rollback_attempted=1"
if grep -q "rollback_rc=0" "$RESULT_EV/deploy-result.txt"; then case_fail C6 "回滚重启失败却被判为 rollback_rc=0（假成功）"; fi
if [ "$RESULT_AFTER" != "$RESULT_OLD" ]; then case_fail C6 "磁盘内容未恢复为旧 index"; fi
if [ "$RESULT_SERVED_AFTER" != "$RESULT_SERVED_BEFORE" ]; then case_fail C6 "served 与旧 SHA 不一致"; fi
if [ "$(ls -d "$RESULT_ROOT"/live.failed-* 2>/dev/null | wc -l)" = 0 ]; then case_fail C6 "失败的新目录未保留"; fi
case_end C6 "回滚重启失败被诚实判为 NO-GO（rollback_rc 非 0）"

echo "case_fails=$CASE_FAILS" | tee -a "$LOG"
if [ "$CASE_FAILS" != 0 ]; then echo "UDL01_DEPLOY_V3_NEGCAL_VERDICT=FAIL case_fails=$CASE_FAILS"; exit 1; fi
echo "UDL01_DEPLOY_V3_NEGCAL_VERDICT=PASS cases=6 case_fails=0"
exit 0
