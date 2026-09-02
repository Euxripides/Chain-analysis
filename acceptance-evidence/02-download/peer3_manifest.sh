#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== PEER3 manifest content ==="
docker exec deploy-minio-1 mc cat local/blockchain-raw/raw/ankr/bsc/token_transfers/2026/09/56fcc28e7f21b45b3352e15e7a70ff3164e1a2c776b32e11f2562d7c7d745853/manifest.json.gz 2>/dev/null | gunzip 2>/dev/null
echo ""
echo "=== parts in that dir ==="
docker exec deploy-minio-1 mc ls local/blockchain-raw/raw/ankr/bsc/token_transfers/2026/09/56fcc28e7f21b45b3352e15e7a70ff3164e1a2c776b32e11f2562d7c7d745853 2>/dev/null
