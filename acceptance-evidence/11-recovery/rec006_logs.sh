#!/usr/bin/env bash
set +e
echo "=== api.log nats-related lines during outage window ==="
grep -iE 'nats|connect' /tmp/api.log | tail -8
echo "=== worker.log nats-related ==="
grep -iE 'nats|connect' /tmp/worker.log | tail -8
