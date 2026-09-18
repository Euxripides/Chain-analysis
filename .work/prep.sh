#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
echo "== backup unit =="
systemctl --user is-active blockchain-daily-backup.service
systemctl --user is-active blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console
echo "== load =="
uptime
echo "== current binaries =="
sha256sum "$P/backend/bin/api" "$P/backend/bin/worker" 2>&1
ls -la "$P/backend/bin/" | head -20
echo "== git =="
cd "$P" && git rev-parse HEAD && git status --porcelain | head -30
echo "== backup dir =="
TS=$(date -u +%Y%m%dT%H%M%SZ)
DEST="$HOME/deploy-backups/pre-perf-$TS"
mkdir -p "$DEST"
cp -av "$P/backend/bin/api" "$DEST/api.pre-perf"
cp -av "$P/backend/bin/worker" "$DEST/worker.pre-perf"
sha256sum "$DEST"/*
echo "DEST=$DEST"
echo "$DEST" > /tmp/pre-perf-backup-dir.txt
