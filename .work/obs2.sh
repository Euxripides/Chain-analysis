#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
echo "== process age =="
ps -o pid,etime,etimes,cmd -p 1254120 2>/dev/null || echo "(gone)"
echo "== script head =="
sed -n '1,60p' "$P/scripts/ops/observe-wal.py" 2>/dev/null || echo "(no script)"
echo "== any output files it writes =="
ls -lat "$P/acceptance-evidence/dual-system-v1/sprint7/perf/" 2>/dev/null | head -8
find "$P/.work" "$P/acceptance-evidence" -name '*baseline-clean*' -newermt '-2 hours' 2>/dev/null | head -5
