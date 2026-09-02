#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
DIR=raw/ankr/bsc/token_transfers/2026/08/68ae5c6def219cace607391fc28def7af01d71cf039c60446f7e678cdc2da81f
echo "=== DL-0019 parts ==="
docker exec deploy-minio-1 mc ls local/blockchain-raw/$DIR 2>/dev/null
echo "=== per-part block span (host-side decompress) ==="
for part in $(docker exec deploy-minio-1 mc ls local/blockchain-raw/$DIR 2>/dev/null | awk '{print $NF}' | grep '^part-'); do
  echo "--- $part ---"
  docker exec deploy-minio-1 mc cat local/blockchain-raw/$DIR/$part 2>/dev/null | gunzip 2>/dev/null | head -c 200000 | python3 -c "
import sys, json
data = json.load(sys.stdin)
rows = data.get('rows', [])
blocks = [r.get('blockHeight') for r in rows if r.get('blockHeight')]
txs = [r.get('transactionHash') for r in rows]
print('rows:', len(rows), 'block min/max:', min(blocks) if blocks else None, max(blocks) if blocks else None, 'hasTarget:', any(t == '0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a' for t in txs))
"
done
