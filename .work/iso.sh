#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
cd /home/euripides/projects/blockchain-platform/backend
echo "== does countoracle depend on any changed package? =="
go list -deps ./internal/countoracle 2>/dev/null | grep -E 'blockchain-platform/internal/(addressmeta|ingest|api|clickhouse|explorer|query)$' || echo "(none of addressmeta/ingest/api/clickhouse/explorer/query)"
echo
echo "== load =="; uptime
echo "== rerun TestProviderIsolationGate 3x =="
for i in 1 2 3; do
  go test ./internal/countoracle/ -run TestProviderIsolationGate -count=1 2>&1 | grep -E 'degradation|FAIL|ok |%' | head -6
  echo "--- run $i done ---"
done
