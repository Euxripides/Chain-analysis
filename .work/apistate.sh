#!/usr/bin/env bash
set -uo pipefail
echo "== api unit / listener =="
systemctl --user is-active blockchain-api
ss -ltnp 2>/dev/null | grep -E ':(8080|18099)' || echo "(no listener on 8080/18099)"
echo "== restart counter =="
systemctl --user show blockchain-api -p NRestarts --value
journalctl --user -u blockchain-api --since "-25 min" --no-pager | grep -E 'Started|Stopped|api listening|api stopped' | tail -10
