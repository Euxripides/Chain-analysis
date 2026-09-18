#!/usr/bin/env bash
set -uo pipefail
LIVE=/home/euripides/projects/blockchain-platform
echo "===== backend/internal/clickhouse/ch.go ====="
diff -u "$LIVE/backend/internal/clickhouse/ch.go" "/tmp/stage/backend_internal_clickhouse_ch.go" > /tmp/d.txt; echo "diff_lines=$(wc -l < /tmp/d.txt)"; head -40 /tmp/d.txt
echo "===== backend/internal/addressmeta/service.go ====="
diff -u "$LIVE/backend/internal/addressmeta/service.go" "/tmp/stage/backend_internal_addressmeta_service.go" > /tmp/d.txt; echo "diff_lines=$(wc -l < /tmp/d.txt)"; head -40 /tmp/d.txt
echo "===== backend/internal/ingest/writer.go ====="
diff -u "$LIVE/backend/internal/ingest/writer.go" "/tmp/stage/backend_internal_ingest_writer.go" > /tmp/d.txt; echo "diff_lines=$(wc -l < /tmp/d.txt)"; head -40 /tmp/d.txt
echo "===== backend/internal/api/sprint5_explorer.go ====="
diff -u "$LIVE/backend/internal/api/sprint5_explorer.go" "/tmp/stage/backend_internal_api_sprint5_explorer.go" > /tmp/d.txt; echo "diff_lines=$(wc -l < /tmp/d.txt)"; head -40 /tmp/d.txt
echo '===== hashes of parent-owned files ====='
sha256sum $LIVE/backend/internal/explorer/service.go $LIVE/backend/internal/tron/client.go $LIVE/backend/internal/query/query.go
