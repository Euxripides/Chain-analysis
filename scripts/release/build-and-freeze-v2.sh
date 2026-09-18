#!/usr/bin/env bash
# build-and-freeze-v2.sh — 从当前源码构建 api/worker/migrate，并产出"源码 manifest ↔ 构建产物"绑定证据。
#
# v2 (2026-09-14) fixes one evidence defect in v1, nothing else:
#   v1 printed build_script_sha256=$(sha256sum "$0") AFTER cd "$PROJECT/backend".
#   With a relative invocation path $0 no longer resolves, so the manifest shipped
#   an EMPTY build_script_sha256 with no error. v1 and its empty-value manifest stay
#   on record as a failure; v2 resolves the script path once, at startup, and uses
#   that absolute path for every self-hash.
#
# 设计原则（§29.1 / §23.8）：
#   - 先冻结源码 manifest（sha256 全量），再构建，构建产物 sha256 与 manifest 同目录落盘；
#   - 构建脚本本身自记 sha256（runhash），不接受"名字相同即同一构建"；
#   - 产物必须做**按值**的明文凭据扫描（不是按 KEY= 模式），命中即失败；
#   - 绝不就地覆盖 backend/bin/*：先写 .tmp 再原子 mv，旧产物保留为 .bak-<TS>。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
GO=/usr/local/go/bin/go
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/deploy-fix-$TS}"
ENV_FILE="${ENV_FILE:-$PROJECT/deploy/.env}"

mkdir -p "$EV"
cd "$PROJECT"

echo "=== build-and-freeze $TS ==="
echo "project=$PROJECT"
echo "go=$($GO version)"
SCRIPT_PATH=$(readlink -f "$0")   # resolved BEFORE any cd: v1 hashed a stale relative $0
echo "script_sha256=$(sha256sum "$SCRIPT_PATH" | cut -d' ' -f1)"
echo "script_path=$SCRIPT_PATH"

# ---- 1. 源码 manifest -------------------------------------------------------
cd "$PROJECT/backend"
find cmd internal go.mod go.sum -type f \( -name '*.go' -o -name 'go.mod' -o -name 'go.sum' \) -print0 \
  | sort -z | xargs -0 sha256sum > "$EV/source-manifest.txt"
echo "source_files=$(wc -l < "$EV/source-manifest.txt")"
echo "source_manifest_sha256=$(sha256sum "$EV/source-manifest.txt" | cut -d' ' -f1)"

# ---- 2. 构建 ---------------------------------------------------------------
set -o pipefail
BUILD_RC=0
"$GO" build -o "$EV/api.build"     ./cmd/api     2> "$EV/build-api.err"     || BUILD_RC=1
"$GO" build -o "$EV/worker.build"  ./cmd/worker  2> "$EV/build-worker.err"  || BUILD_RC=1
"$GO" build -o "$EV/migrate.build" ./cmd/migrate 2> "$EV/build-migrate.err" || BUILD_RC=1
echo "build_rc=$BUILD_RC"
if [ "$BUILD_RC" -ne 0 ]; then
  echo "BUILD FAILED — see $EV/build-*.err"
  tail -n 20 "$EV"/build-*.err || true
  exit 2
fi

sha256sum "$EV/api.build" "$EV/worker.build" "$EV/migrate.build" | tee "$EV/artifact-sha256.txt"

# ---- 3. 按值凭据扫描（不得泄露值） -----------------------------------------
umask 077
PAT="$EV/.secret-patterns.tmp"
: > "$PAT"
awk -F= '/^(POSTGRES_PASSWORD|CLICKHOUSE_PASSWORD|MINIO_ROOT_PASSWORD)=/{print substr($0, index($0,"=")+1)}' "$ENV_FILE" >> "$PAT"
grep -E '^(DATABASE_URL|MIGRATION_DATABASE_URL|CLICKHOUSE_URL)=' "$ENV_FILE" \
  | sed -E 's#^[^=]+=##' | sed -E 's#^[a-zA-Z0-9+.-]+://([^:]+):([^@]+)@.*#\2#' >> "$PAT"
grep -E '^(GPT_ACTIONS_API_KEY|GPT_ACTIONS_ADMIN_KEY|MCP_API_KEY|ANKR_API_KEY|CHAINBASE_API_KEY|TRONGRID_API_KEY)=' "$ENV_FILE" \
  | sed -E 's#^[^=]+=##' >> "$PAT"
grep -v '^[[:space:]]*$' "$PAT" | sort -u > "$PAT.u" && mv "$PAT.u" "$PAT"
PATTERNS=$(wc -l < "$PAT")
echo "secret_patterns_checked=$PATTERNS"
SCAN_FAIL=0
{
  echo "artifact  patterns  literal_hits"
  for b in "$EV/api.build" "$EV/worker.build" "$EV/migrate.build"; do
    tot=0
    while IFS= read -r v; do
      [ -z "$v" ] && continue
      n=$(grep -a -o -F -- "$v" "$b" 2>/dev/null | wc -l)
      tot=$((tot+n))
    done < "$PAT"
    echo "$(basename "$b")  $PATTERNS  $tot"
    [ "$tot" -ne 0 ] && SCAN_FAIL=1
  done
} | tee "$EV/secret-literal-scan.txt"
rm -f "$PAT"
echo "secret_literal_scan_fail=$SCAN_FAIL"

# ---- 4. 上传/部署 manifest（源码 + 产物 + 运行身份） ------------------------
{
  echo "generated_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host=$(hostname)"
  echo "project=$PROJECT"
  echo "git_head_windows_tree=51596f75e0f9472c3e051205b0d0cf2896a3637c (rsync 部署目录无 .git；套件 build_sha=working-tree)"
  echo "build_script_sha256=$(sha256sum "$SCRIPT_PATH" | cut -d' ' -f1)"
  echo "build_script_path=$SCRIPT_PATH"
  echo "source_manifest_sha256=$(sha256sum "$EV/source-manifest.txt" | cut -d' ' -f1)"
  echo "source_manifest_files=$(wc -l < "$EV/source-manifest.txt")"
  echo "--- artifacts ---"
  cat "$EV/artifact-sha256.txt"
  echo "--- go buildinfo (module/deps) ---"
  for b in api worker migrate; do
    echo "[$b]"
    "$GO" version -m "$EV/$b.build" | sed -n '1,4p'
  done
  echo "--- currently running binaries (pre-deploy) ---"
  for b in api worker migrate; do
    [ -f "$PROJECT/backend/bin/$b" ] && sha256sum "$PROJECT/backend/bin/$b"
  done
  echo "--- secret literal scan ---"
  cat "$EV/secret-literal-scan.txt"
  echo "secret_literal_scan_fail=$SCAN_FAIL"
} > "$EV/BUILD-MANIFEST.txt"
echo "manifest=$EV/BUILD-MANIFEST.txt sha256=$(sha256sum "$EV/BUILD-MANIFEST.txt" | cut -d' ' -f1)"
echo "EVIDENCE_DIR=$EV"
[ "$SCAN_FAIL" -eq 0 ] || exit 3
exit 0
