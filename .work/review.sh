#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/backend"
echo "== gofmt on ONLY the files changed this round =="
gofmt -l internal/api/server.go internal/api/sprint5_jobs_control.go internal/api/job_event_throttle_test.go internal/store/provider_v13.go internal/store/v12.go internal/store/performance.go
echo "(empty above = all clean)"
echo
echo "== guard clauses actually present in the compiled source =="
grep -c "IS DISTINCT FROM EXCLUDED" internal/store/provider_v13.go internal/store/v12.go
grep -n "IS DISTINCT FROM \$" internal/store/performance.go
echo
echo "== throttle wiring =="
grep -n "progressEventType\|progressEventPersistInterval\|jobEvents\." internal/api/sprint5_jobs_control.go internal/api/server.go | head -20
