#!/usr/bin/env bash
set -uo pipefail
export PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
cp /tmp/r9-src/frontend/apps/user-console/src/pages/DownloadNewPage.tsx "$V/frontend/apps/user-console/src/pages/DownloadNewPage.tsx"
cd "$V/frontend"
echo "=== tsc ==="
npx tsc -b 2>&1 | head -25; echo "tsc_rc=${PIPESTATUS[0]}"
echo "=== build:user ==="
pnpm run build:user 2>&1 | tail -6; echo "build_rc=${PIPESTATUS[0]}"
echo "=== new bundle has the guard? ==="
for s in "压缩炸弹" "不是合法的 OOXML 工作簿" "预览 Coverage 与成本"; do
  f=$(grep -rl -- "$s" apps/user-console/dist/assets 2>/dev/null | head -1)
  echo "[$s] -> ${f:-NOT_FOUND}"
done
echo "=== served index ==="
curl -sk https://127.0.0.1:8448/app-v2/ | grep -o 'src="[^"]*"'
