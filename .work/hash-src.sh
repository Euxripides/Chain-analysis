#!/usr/bin/env bash
# 生成本地源码清单（排除 .git/node_modules/bin/artifacts/evidence）
set -u
cd "$(dirname "$0")/.." || exit 1
OUT="${1:-/tmp/local-src-manifest.txt}"
find . -type f \
  -not -path './.git/*' -not -path './node_modules/*' -not -path '*/node_modules/*' \
  -not -path './backend/bin/*' -not -path './artifacts/*' -not -path './acceptance-evidence/*' \
  -not -path './.work/*' -not -path './.sync/*' -not -path './.codex-deploy-actions/*' \
  -not -path '*/dist/*' -not -name '*.tar.gz' -not -name '*.zip' -not -name '*.log' -not -name '*.png' \
  | LC_ALL=C sort | while read -r f; do printf '%s  %s\n' "$(sha256sum "$f" | cut -d' ' -f1)" "$f"; done > "$OUT"
wc -l < "$OUT"
sha256sum "$OUT"
