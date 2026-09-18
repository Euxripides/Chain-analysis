
#!/usr/bin/env bash
# systemd 瞬态单元的收集器：**如实**把内部脚本的真实 rc 作为单元退出码。
# 教训（2026-09-13）：外层写 bash -lc "...; echo EXIT=$?" 时，单元退出码来自 echo，
# 于是一个 rc=2 的保护壳失败会显示成 ExecMainStatus=0，掩盖真实失败。
set -uo pipefail
cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests || exit 2
LOG="${1:-/tmp/guarded-run.log}"
bash run-guarded-fault-suite.sh run-sprint5-e2e.sh > "$LOG" 2>&1
RC=$?
echo "GUARDED_SUITE_RC=$RC" >> "$LOG"
exit "$RC"
