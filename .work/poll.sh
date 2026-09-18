#!/usr/bin/env bash
set -uo pipefail
for i in 1 2 3 4 5 6 7 8; do
  if pgrep -f observe-wal >/dev/null; then
    echo "t=$((i*30))s: observe-wal STILL RUNNING"
  else
    echo "t=$((i*30))s: observe-wal FINISHED"
    break
  fi
  sleep 30
done
echo "--- process now ---"
pgrep -a -f observe-wal || echo "(none)"
echo "--- baseline-clean log ---"
tail -12 /tmp/wal-baseline-clean.log 2>/dev/null || echo "(no log)"
echo "--- load ---"; uptime
