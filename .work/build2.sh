#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/backend"
echo "== gofmt =="; gofmt -l internal/api internal/store
echo "== build ./... =="; go build ./... 2>&1 | head -30; echo "build_exit=$?"
echo "== vet =="; go vet ./internal/api/... ./internal/store/... 2>&1 | head -30; echo "vet_exit=$?"
echo "== targeted tests -count=1 =="
go test -count=1 ./internal/api/... ./internal/store/... ./internal/countoracle/... ./internal/download/... 2>&1 | tail -20
echo "targeted_exit=${PIPESTATUS[0]}"
