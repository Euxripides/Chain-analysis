#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/backend"
echo "== gofmt + build + test =="
gofmt -l internal/store/provider_v13.go
go build ./... 2>&1 | head -10; echo "build_exit=$?"
go test -count=1 ./internal/store/... ./internal/countoracle/... ./internal/api/... 2>&1 | tail -6
echo "== rebuild =="
go build -o bin/api.new ./cmd/api && go build -o bin/worker.new ./cmd/worker && echo built
sha256sum bin/api.new bin/worker.new
echo "== registry guard present =="
grep -c "IS DISTINCT FROM EXCLUDED" internal/store/provider_v13.go
