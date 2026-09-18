#!/usr/bin/env bash
set -uo pipefail
echo "== coordinator's WAL observation running? =="
pgrep -a -f 'observe-wal' || echo "(no observe-wal process)"
echo "== recent evidence dirs (final-*) =="
ls -lat /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/ 2>/dev/null | head -6
echo "== any wal observation output files =="
ls -lat /tmp/*wal* 2>/dev/null | head -8
ls -lat /home/euripides/projects/blockchain-platform/.work/*wal* 2>/dev/null | head -8
echo "== load =="; uptime
