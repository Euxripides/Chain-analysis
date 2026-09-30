#!/usr/bin/env bash
set -euo pipefail
exec /usr/bin/python3 /home/euripides/projects/blockchain-platform/scripts/backup/personal_backup.py "$@"
