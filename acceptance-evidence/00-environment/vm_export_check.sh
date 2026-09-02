#!/usr/bin/env bash
set +e
echo "=== VM export_v2.go format validation ==="
grep -n 'format must be' /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
echo "=== VM export_v2.go mtime ==="
ls -la /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
echo "=== VM has parquet in export_v2? ==="
grep -c 'parquet' /home/euripides/projects/blockchain-platform/backend/internal/api/export_v2.go
echo "=== api binary md5 + start time ==="
md5sum /home/euripides/projects/blockchain-platform/backend/bin/api
ps -o pid,lstart,cmd -p $(pgrep -f 'bin/api' | head -1)
