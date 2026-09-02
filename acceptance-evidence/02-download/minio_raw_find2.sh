#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
echo "=== raw objects matching a41c1bc3 (new download) ==="
docker exec deploy-minio-1 mc find local/blockchain-raw --name '*' 2>/dev/null | grep a41c1bc32c5020574c24db78cb823ea61c8bd9cc937
echo "=== raw objects matching 19604ae2 (LABUBU 0-row download) ==="
docker exec deploy-minio-1 mc find local/blockchain-raw --name '*' 2>/dev/null | grep 19604ae2164ba6be74364b04b91943ea011c883a78a
echo "=== latest 3 bsc token_transfers raw dirs ==="
docker exec deploy-minio-1 mc find local/blockchain-raw/raw/ankr/bsc/token_transfers --name 'manifest.json.gz' 2>/dev/null | tail -3
