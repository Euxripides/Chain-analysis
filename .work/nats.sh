#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
echo "NATS_MONITOR_PORT=${NATS_MONITOR_PORT:-unset}"
P="${NATS_MONITOR_PORT:-8222}"
echo "== nats monitoring root =="
curl -s --max-time 5 "http://127.0.0.1:$P/varz" | head -c 600; echo
echo "== per-subject message counts (events.>) =="
curl -s --max-time 5 "http://127.0.0.1:$P/subsz?subs=1" | head -c 1500; echo
