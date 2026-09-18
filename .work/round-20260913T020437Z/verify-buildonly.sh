#!/usr/bin/env bash
set -uo pipefail
V=/home/euripides/projects/blockchain-platform
echo "=== r9-build-deploy.sh: does it deploy/restart? (expect only build) ==="
grep -nE "systemctl|restart|bin/api|bin/worker|deploy-binaries|mv -f|cp -f" /tmp/r9-build-deploy.sh || echo "NO deploy/restart/mv constructs found (build-only)"
echo "=== its sha ==="
sha256sum /tmp/r9-build-deploy.sh
echo "=== find stage5 deploy scripts ==="
find "$V" -maxdepth 6 -name "*stage5*" -o -maxdepth 6 -name "*deploy*v2*.sh" 2>/dev/null | grep -v node_modules | head -20
echo "=== done ==="
