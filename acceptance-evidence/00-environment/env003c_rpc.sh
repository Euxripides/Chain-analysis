#!/usr/bin/env bash
set +e
echo "=== RPC config keys in deploy/.env ==="
grep -E '^RPC_CHAINS=|^RPC_ENABLED=|^ANKR_RPC_ENABLED=' /home/euripides/projects/blockchain-platform/deploy/.env | cut -d= -f1
echo "=== RPC_CHAINS value (non-secret chain list) ==="
grep -E '^RPC_CHAINS=' /home/euripides/projects/blockchain-platform/deploy/.env | cut -d= -f2-
echo "=== backend .env RPC keys ==="
grep -E '^RPC_CHAINS=|^RPC_ENABLED=|^ANKR_RPC_ENABLED=' /home/euripides/projects/blockchain-platform/backend/.env | cut -d= -f1
echo "=== backend .env RPC_CHAINS value ==="
grep -E '^RPC_CHAINS=' /home/euripides/projects/blockchain-platform/backend/.env | cut -d= -f2-
