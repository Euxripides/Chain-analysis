#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/backend"
echo "== source anchors =="
sha256sum internal/api/server.go internal/api/sprint5_jobs_control.go internal/api/job_event_throttle_test.go internal/store/provider_v13.go internal/store/v12.go internal/store/performance.go
echo "== build api.new / worker.new =="
go build -o bin/api.new ./cmd/api && echo "api built"
go build -o bin/worker.new ./cmd/worker && echo "worker built"
sha256sum bin/api.new bin/worker.new
echo "== current deployed =="
sha256sum bin/api bin/worker
