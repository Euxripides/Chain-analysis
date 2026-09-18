#!/usr/bin/env bash
set -uo pipefail
export PATH=/usr/local/go/bin:$PATH
P=/home/euripides/projects/blockchain-platform
cd "$P/backend"
echo "== running go processes =="
pgrep -a -f 'go (test|build|vet)' | head -5 || echo "(none)"
echo "== source hashes (all six changed files) =="
sha256sum internal/clickhouse/ch.go internal/addressmeta/service.go internal/ingest/writer.go internal/api/sprint5_explorer.go internal/explorer/service.go internal/tron/client.go
echo "== build =="
time go build -o bin/api.new ./cmd/api 2>&1 | tail -20
echo "new sha: $(sha256sum bin/api.new | awk '{print $1}')"
echo "parent sha: 0dae50f10a7bf56180b28044dc1404eba1be5ced5ab7173f470c544600376ac7"
echo "== worker build check (unchanged) =="
go build -o /tmp/worker.check ./cmd/worker 2>&1 | tail -5
echo "worker build ok: $(sha256sum /tmp/worker.check | awk '{print $1}')"
echo "deployed worker: $(sha256sum bin/worker | awk '{print $1}')"
