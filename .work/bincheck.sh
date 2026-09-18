#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
echo "== running binary =="
ls -la /proc/666929/exe 2>/dev/null
sha256sum /proc/666929/exe 2>/dev/null || sha256sum "$P/backend/bin/api"
echo "== bin/api on disk =="
ls -la "$P/backend/bin/api" "$P/backend/bin/api.new" "$P/backend/bin/api.activity" 2>&1
sha256sum "$P/backend/bin/api" 2>/dev/null
sha256sum "$P/backend/bin/api.new" 2>/dev/null
sha256sum "$P/backend/bin/api.activity" 2>/dev/null
echo "== frozen backup reference =="
sha256sum "$HOME/deploy-backups/pre-perf-20260916T174927Z/api.pre-perf"
