#!/usr/bin/env bash
set +e
echo "=== stop nats ==="
docker stop deploy-nats-1
sleep 3
echo "=== health during outage ==="
curl -sS -w '|%{http_code}
' http://127.0.0.1:8080/health/ready
echo "=== job list still works (PG) ==="
curl -sS -w '|%{http_code}
' 'http://127.0.0.1:8080/api/v1/download-jobs?limit=2' | head -c 300
echo ""
echo "=== create small job while nats down ==="
curl -sS -w '|%{http_code}
' -X POST http://127.0.0.1:8080/api/v1/datasets/ensure -H 'Content-Type: application/json' -d '{"source":"user_download","priority":100,"subjects":["0x6808c73d7e9f1e1905c0618bb47bd6947ed4a6ae"],"chain":"bsc","datasets":["balances"],"range":{"type":"block","from_block":115394565,"to_block":115394565}}' | head -c 300
echo ""
sleep 45
echo "=== restart nats ==="
docker start deploy-nats-1
sleep 8
echo "=== health after ==="
curl -sS -w '|%{http_code}
' http://127.0.0.1:8080/health/ready
