#!/usr/bin/env bash
# udl01-runner-v6-negative-calibration.sh — 对 run-udl01-worker-isolation-v6.sh 的**执行前**负向校准。
#
# 背景：根 2026-09-13 独立审查发现，v6 runner 的 P0b0 只 grep 配置内容，
# 但真正传给 Playwright 的 --config 仍指向 v5c -> 会跑旧 v5b spec 却可能打印 PASS。
# 本校准证明两件事（都在**任何 Playwright 启动之前**失败退出）：
#   N1 把执行用 --config 改成另一个非 v6 配置（v5b，绕开旧配置串自检）-> 必须 rc=4 且判词 RUNNER_EXEC_CONFIG_NOT_V6
#   N2 把执行用 --config 改回 v5c -> 必须 rc=4（陈旧配置串自检）
# 两个用例都必须：无 playwright 进程被启动、无证据目录被当成本轮轮次。
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
RUNNER="$R/scripts/s7/run-udl01-worker-isolation-v6.sh"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$R/acceptance-evidence/dual-system-v1/sprint7/udl01-runner-v6-negcal-$TS}"
if [ -e "$EV" ]; then echo "EV_EXISTS_FAILCLOSED $EV"; exit 9; fi
mkdir -p "$EV"; chmod 700 "$EV"
LOG="$EV/negcal.log"
echo "evidence=$EV" | tee "$LOG"
echo "runner_sha256=$(sha256sum "$RUNNER" | cut -d" " -f1)" | tee -a "$LOG"
FAILS=0
bad(){ FAILS=$((FAILS+1)); echo "NEGCAL_FAIL $1 :: $2" | tee -a "$LOG"; }
good(){ echo "NEGCAL_PASS $1 :: $2" | tee -a "$LOG"; }

run_variant(){
  local name="$1"; local target="$2"
  local cand="$EV/$name.runner.sh"
  # 只改**真正执行的那一行**的 --config（保留 CONF= 预检行，才能证明是身份门禁而非路径缺失拦下的）
  sed "/cli[.]js test --config/ s/playwright[.]udl01v6[.]config[.]ts/$target/" "$RUNNER" > "$cand"
  chmod 755 "$cand"
  local before_pw; before_pw="$(pgrep -fc "playwright" || true)"
  EV="$EV/$name-ev" bash "$cand" > "$EV/$name.stdout.txt" 2>&1
  local rc=$?
  sleep 1
  local after_pw; after_pw="$(pgrep -fc "playwright" || true)"
  echo "$rc" > "$EV/$name.rc.txt"
  printf "variant=%s target=%s rc=%s playwright_before=%s playwright_after=%s\n" "$name" "$target" "$rc" "$before_pw" "$after_pw" >> "$LOG"
  VAR_RC="$rc"; VAR_AFTER_PW="$after_pw"; VAR_EV="$EV/$name-ev"; VAR_OUT="$EV/$name.stdout.txt"
}

echo "=== N1 执行配置被改成 v5b（绕开旧配置串自检） ==="
run_variant N1 playwright.udl01v5b.config.ts
if [ "$VAR_RC" != 4 ]; then bad N1 "rc=$VAR_RC != 4"; fi
grep -q "RUNNER_EXEC_CONFIG_NOT_V6" "$VAR_OUT" && good N1 "执行配置身份门禁在启动 Playwright 前拦下" || bad N1 "未出现 RUNNER_EXEC_CONFIG_NOT_V6"
if [ ! -f "$VAR_EV/playwright.rc" ] && [ ! -f "$VAR_EV/playwright.console.log" ]; then good N1 "未进入测试阶段（无 playwright.rc / playwright.console.log）"; else bad N1 "已进入测试阶段（预检被越过）"; fi
if [ "$VAR_AFTER_PW" = 0 ]; then good N1 "未启动任何 playwright 进程"; else bad N1 "启动了 playwright 进程 after=$VAR_AFTER_PW"; fi

echo "=== N2 执行配置被改回 v5c（旧配置串自检） ==="
run_variant N2 playwright.udl01v5c.config.ts
if [ "$VAR_RC" != 4 ]; then bad N2 "rc=$VAR_RC != 4"; fi
grep -q "RUNNER_REFERENCES_STALE_CONFIG" "$VAR_OUT" && good N2 "陈旧配置串自检拦下" || bad N2 "未出现 RUNNER_REFERENCES_STALE_CONFIG"
if [ "$VAR_AFTER_PW" = 0 ]; then good N2 "未启动任何 playwright 进程"; else bad N2 "启动了 playwright 进程 after=$VAR_AFTER_PW"; fi
if [ ! -f "$VAR_EV/playwright.rc" ]; then good N2 "未进入测试阶段"; else bad N2 "已进入测试阶段；rc=$(cat "$VAR_EV/playwright.rc" 2>/dev/null)"; fi

echo "fails=$FAILS" | tee -a "$LOG"
if [ "$FAILS" != 0 ]; then echo "UDL01_RUNNER_V6_NEGCAL_VERDICT=FAIL fails=$FAILS"; exit 1; fi
echo "UDL01_RUNNER_V6_NEGCAL_VERDICT=PASS variants=2 fails=0"
exit 0
