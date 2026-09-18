#!/usr/bin/env bash
# r9-build-deploy.sh — 构建并部署包含 U-DL-01 导入校验的 api/worker/migrate。
# 冻结后不再修改；旧二进制由 scripts/release/deploy-binaries.sh 备份保留。
set -uo pipefail
export PATH=/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH
V=/home/euripides/projects/blockchain-platform
TS=$(date -u +%Y%m%dT%H%M%SZ)
ROUND="$V/acceptance-evidence/dual-system-v1/sprint7/uc-download-import-$TS"
BUILD="$ROUND/build-$TS"
mkdir -p "$BUILD"; chmod 700 "$ROUND"
echo "round=$ROUND"
echo "script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"

cd "$V/backend" || exit 90
go version > "$BUILD/go-version.txt" 2>&1
sha256sum go.mod go.sum > "$BUILD/go-mod-sum-sha.txt"
find . -name '*.go' -not -path './.git/*' -print0 | sort -z | xargs -0 sha256sum > "$BUILD/source-manifest.txt"
echo "source_files=$(wc -l < "$BUILD/source-manifest.txt")"
sha256sum "$BUILD/source-manifest.txt"

echo "=== stage1 go build ./... ==="
go build ./... > "$BUILD/s1-build-all.out" 2>&1; echo "rc=$?" | tee "$BUILD/s1-build-all.rc"
echo "=== stage2 go vet ./... ==="
go vet ./... > "$BUILD/s2-vet.out" 2>&1; echo "rc=$?" | tee "$BUILD/s2-vet.rc"
echo "=== stage3 go test ./... ==="
go test ./... > "$BUILD/s3-test.out" 2>&1; echo "rc=$?" | tee "$BUILD/s3-test.exitcode"
grep -c '^ok' "$BUILD/s3-test.out" > "$BUILD/s3-test-okcount.txt"
grep -E '^(FAIL|--- FAIL)' "$BUILD/s3-test.out" > "$BUILD/s3-test-failures.txt" || true
echo "ok_packages=$(cat "$BUILD/s3-test-okcount.txt") failures=$(wc -l < "$BUILD/s3-test-failures.txt")"

echo "=== stage4 build artifacts ==="
go build -o "$BUILD/api.build" ./cmd/api 2> "$BUILD/s4-build-api.err"; echo "api_rc=$?"
go build -o "$BUILD/worker.build" ./cmd/worker 2> "$BUILD/s4-build-worker.err"; echo "worker_rc=$?"
go build -o "$BUILD/migrate.build" ./cmd/migrate 2> "$BUILD/s4-build-migrate.err"; echo "migrate_rc=$?"
chmod 755 "$BUILD"/*.build
sha256sum "$BUILD"/*.build | tee "$BUILD/artifact-sha256.txt"
for b in api worker migrate; do go version -m "$BUILD/$b.build" > "$BUILD/buildinfo-$b.txt" 2>&1; done
echo "=== done build ==="
echo "EVIDENCE_ROUND=$ROUND"
echo "EVIDENCE_BUILD=$BUILD"
