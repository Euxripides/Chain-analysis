#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
cd /home/euripides/projects/blockchain-platform/backend
echo "== go version =="; go version
echo "== build =="; go build ./... 2>&1 | head -40
echo "build_exit=$?"
echo "== vet (changed pkgs) =="; go vet ./internal/clickhouse/... ./internal/addressmeta/... ./internal/ingest/... ./internal/api/... 2>&1 | head -40
echo "== hashes parent-owned files =="
sha256sum internal/explorer/service.go internal/tron/client.go internal/query/query.go
