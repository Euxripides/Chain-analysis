#!/usr/bin/env bash
# 只做语法/类型校验，不构建、不部署：v4 轮版本保持冻结，新 guard 由下一轮构建证明。
set -uo pipefail
export PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
SRC="$V/frontend/apps/user-console/src/pages/DownloadNewPage.tsx"
echo "deployed_source_sha_BEFORE=$(sha256sum "$SRC" | cut -d' ' -f1)"
cp /tmp/r9-src/frontend/apps/user-console/src/pages/DownloadNewPage.tsx "$SRC"
echo "source_on_disk_sha_AFTER=$(sha256sum "$SRC" | cut -d' ' -f1)"
echo "bundle_still_v4=$(sha256sum "$V/frontend/apps/user-console/dist/index.html" | cut -d' ' -f1)"
cd "$V/frontend"
npx tsc -b 2>&1 | head -20; echo "tsc_rc=${PIPESTATUS[0]}"
echo "NOTE: 未执行 build:user，部署中的 bundle 仍是 v4 版本，源码已领先一版。"
