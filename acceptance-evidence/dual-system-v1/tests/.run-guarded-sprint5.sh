#!/usr/bin/env bash
# 一次性编排：带"用户在工作"保护的 sprint5 故障注入套件复跑（2026-09-13）。
set -uo pipefail
PROJECT=/home/euripides/projects/blockchain-platform
EV="$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7"
TESTS="$PROJECT/acceptance-evidence/dual-system-v1/tests"
QA="$EV/qa"
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a
[ -f /tmp/s6-e2e-env.sh ] && . /tmp/s6-e2e-env.sh
# 保留旧证据（不覆盖、不删除）
PRES="$QA/sprint5/preserved-original-20260913T0148Z"
mkdir -p "$PRES"
for f in run.log console.log result.json ids.env; do
  [ -f "$QA/sprint5/$f" ] && cp -p "$QA/sprint5/$f" "$PRES/$f"
done
echo "preserved_at=$(date -Is)" > "$PRES/PRESERVATION-NOTE.txt"
echo "reason=保留 S7 首轮 sprint5 证据（DATA-007 因看门狗未持有而假失败）" >> "$PRES/PRESERVATION-NOTE.txt"
(cd "$PRES" && sha256sum run.log result.json >> PRESERVATION-NOTE.txt) 2>/dev/null || true
export EVIDENCE_DIR="$QA/sprint5"
export DRILL_OUT="$QA/drill-guard-$(date -u +%Y%m%dT%H%M%SZ)"
export PROJECT
cd "$TESTS"
bash run-fault-drill-guarded.sh "$TESTS/run-sprint5-e2e.sh"
echo "GUARDED_SUITE_RC=$?"
