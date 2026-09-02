#!/usr/bin/env bash
set +e
DIR=raw/ankr/bsc/token_transfers/2026/08/68ae5c6def219cace607391fc28def7af01d71cf039c60446f7e678cdc2da81f
for part in $(docker exec deploy-minio-1 mc ls local/blockchain-raw/$DIR 2>/dev/null | awk '{print $NF}' | grep '^part-' | sort); do
  docker exec deploy-minio-1 mc cat local/blockchain-raw/$DIR/$part 2>/dev/null | gunzip 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
rows = data.get('rows', [])
blocks = [r.get('blockHeight') for r in rows if r.get('blockHeight')]
tgt = any(r.get('transactionHash') == '0x4b61b5353106750a10f66d12d61eba904ce0def3c09b5d43aaa4d55700d28e2a' for r in rows)
print('rows:', len(rows), 'min:', min(blocks) if blocks else '-', 'max:', max(blocks) if blocks else '-', 'target:', tgt)
"
done
