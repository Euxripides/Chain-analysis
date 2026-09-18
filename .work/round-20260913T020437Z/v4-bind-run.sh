#!/usr/bin/env bash
# r9-v4-version-bind.sh — 记录 v3→v4 的真实版本切换（bundle/source/backend），再构建新 bundle 并跑 v4。
set -uo pipefail
export PATH=/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH
V=/home/euripides/projects/blockchain-platform
ROUND="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-20260913T021911Z"
BIND="$ROUND/version-binding"; mkdir -p "$BIND"
SRC="$V/frontend/apps/user-console/src/pages/DownloadNewPage.tsx"

echo "=== PRE-BUILD（此刻 VM 上：源码已是新 guard，bundle 仍是 v3 的旧 guard） ==="
{
  echo "captured_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "--- deployed bundle BEFORE rebuild (this is what v3 actually exercised) ---"
  sha256sum "$V/frontend/apps/user-console/dist/index.html"
  sha256sum "$V"/frontend/apps/user-console/dist/assets/index-*.js
  sha256sum "$V"/frontend/apps/user-console/dist/assets/DownloadNewPage-*.js
  echo "--- source on disk BEFORE rebuild (new real-decompression guard, NOT yet built) ---"
  sha256sum "$SRC"
  echo "--- backend artifacts + running identity (unchanged in this step) ---"
  sha256sum "$V/backend/bin/api" "$V/backend/bin/worker" "$V/backend/bin/migrate"
  for u in blockchain-api blockchain-worker; do
    pid=$(systemctl --user show $u.service -p MainPID --value)
    echo "$u pid=$pid exe_sha=$(sha256sum /proc/$pid/exe | cut -d' ' -f1)"
  done
  echo "--- served index BEFORE rebuild ---"
  curl -sk https://127.0.0.1:8448/app-v2/ | grep -o 'src="[^"]*"'
} | tee "$BIND/pre-build-version.txt"

echo "=== 安装源码 + tsc + build:user ==="
cp /tmp/r9-src/frontend/apps/user-console/src/pages/DownloadNewPage.tsx "$SRC"
cd "$V/frontend"
npx tsc -b 2>&1 | head -20; echo "tsc_rc=${PIPESTATUS[0]}"
pnpm run build:user 2>&1 | tail -4; echo "build_rc=${PIPESTATUS[0]}"

echo "=== POST-BUILD 版本绑定 ==="
{
  echo "captured_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "--- source AFTER (the version the v4 round exercises) ---"
  sha256sum "$SRC"
  echo "--- deployed bundle AFTER rebuild ---"
  sha256sum "$V/frontend/apps/user-console/dist/index.html"
  sha256sum "$V"/frontend/apps/user-console/dist/assets/index-*.js
  sha256sum "$V"/frontend/apps/user-console/dist/assets/DownloadNewPage-*.js
  echo "--- new guard strings present in built bundle ---"
  for s in "声明值不可信" "DecompressionStream" "ZIP 中央目录未被完整消费" "暂不支持 ZIP64"; do
    f=$(grep -rl -- "$s" "$V/frontend/apps/user-console/dist/assets" 2>/dev/null | head -1)
    echo "string[$s] -> ${f:-NOT_FOUND}"
  done
  echo "--- served index AFTER rebuild ---"
  curl -sk https://127.0.0.1:8448/app-v2/ | grep -o 'src="[^"]*"'
  echo "--- backend unchanged ---"
  sha256sum "$V/backend/bin/api" "$V/backend/bin/worker" "$V/backend/bin/migrate"
} | tee "$BIND/post-build-version.txt"

echo "=== 运行 v4（新唯一轮） ==="
cp /tmp/r9-src/frontend/scripts/r9-make-fixtures-v4.mjs "$V/frontend/scripts/"
cp /tmp/r9-src/frontend/tests/r9-uc-download-import-v4.spec.ts "$V/frontend/tests/"
cp /tmp/r9-src/frontend/playwright.r9v4.config.ts "$V/frontend/"
cp /tmp/r9-src/scripts/s7/run-r9-download-import-v4.sh "$V/scripts/s7/"
chmod +x "$V/scripts/s7/run-r9-download-import-v4.sh"
bash -n "$V/scripts/s7/run-r9-download-import-v4.sh" || exit 8
timeout 780 bash "$V/scripts/s7/run-r9-download-import-v4.sh" > /tmp/r9v4.out 2>&1
echo "runner_rc=$?"
tail -45 /tmp/r9v4.out
