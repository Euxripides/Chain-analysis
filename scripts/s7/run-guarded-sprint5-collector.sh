#!/usr/bin/env bash
# systemd 瞬态单元的收集器：**如实**把内部脚本的真实 rc 作为单元退出码。
# 教训（2026-09-13）：外层写 bash -lc "...; echo EXIT=$?" 时，单元退出码来自 echo，
# 于是一个 rc=2 的保护壳失败会显示成 ExecMainStatus=0，掩盖真实失败。
#
# 退出码契约（2026-09-14 复核）：本收集器的退出码 == 冻结运行器的 rc == 保护壳的结论 rc
#   （0=PASS 1=FAIL 2=REVIEW 3=INVALID）。systemd 瞬态单元的 ExecMainStatus 因此等于真实结论。
#   两条硬规则：
#     a) 最后一条命令必须是 exit "$RC"，中间不得有 echo/tee 之类会改写 $? 的收尾；
#     b) 不得写 `bash ...; echo EXIT=$?` 这种把退出码交给 echo 的形式。
set -uo pipefail
cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests || {
  # 3 = INVALID（什么都没跑，无法得出结论）；不要用 2：2 在保护壳契约里表示 REVIEW。
  echo "COLLECTOR_ERROR=cd_failed dir=tests" >&2
  exit 3
}
LOG="${1:-/tmp/guarded-run.log}"
SUITE_NAME="${SUITE_NAME:-run-sprint5-e2e.sh}"
bash run-guarded-fault-suite.sh "$SUITE_NAME" > "$LOG" 2>&1
RC=$?
case "$RC" in
  0|1|2|3) : ;;
  *) echo "WARN: rc=$RC 不在保护壳契约域(0/1/2/3) 内，仍按真实 rc 退出（不修正、不吞掉）" >&2 ;;
esac
echo "GUARDED_SUITE_RC=$RC"
echo "GUARDED_SUITE_RC=$RC" >> "$LOG"
exit "$RC"
