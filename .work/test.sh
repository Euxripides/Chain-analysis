#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
cd /home/euripides/projects/blockchain-platform/backend
echo '== go test ./... =='
go test ./... 2>&1 | tail -60
echo "test_exit=${PIPESTATUS[0]}"
