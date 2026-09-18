#!/usr/bin/env bash
set -uo pipefail
for i in $(seq 1 10); do
  if pgrep -f observe-wal >/dev/null; then
    echo "t=$((i*20))s: still running"
  else
    echo "t=$((i*20))s: FINISHED"
    break
  fi
  sleep 20
done
echo "--- final state ---"
pgrep -a -f observe-wal || echo "(process gone)"
ls -lat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/perf/ | head -5
echo "--- clean baseline csv ---"
cat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/sprint7/perf/wal-observation-baseline-clean-*.csv 2>/dev/null | tail -15
