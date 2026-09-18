#!/usr/bin/env bash
set -uo pipefail
export PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
cd "$V/frontend" || exit 90
echo "=== backup existing user-console dist (additive, kept) ==="
cp -a apps/user-console/dist "apps/user-console/dist.bak-r9-$TS"
echo "backup=apps/user-console/dist.bak-r9-$TS"
sha256sum apps/user-console/dist/index.html
echo "=== build:user ==="
pnpm run build:user 2>&1 | tail -20; echo "build_rc=${PIPESTATUS[0]}"
echo "=== new dist ==="
ls -la apps/user-console/dist apps/user-console/dist/assets | head -20
sha256sum apps/user-console/dist/index.html
echo "=== new bundle contains the new UI strings? ==="
for s in "导入 CSV / XLSX" "服务端地址校验结果" "downloads/validate-addresses" "部分创建成功"; do
  n=$(grep -rl -- "$s" apps/user-console/dist/assets 2>/dev/null | head -1)
  echo "string[$s] -> ${n:-NOT_FOUND}"
done
echo "=== served index after build ==="
curl -sk https://127.0.0.1:8448/app-v2/ | grep -o 'src="[^"]*"'
echo "=== done ==="
