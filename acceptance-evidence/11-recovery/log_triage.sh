#!/usr/bin/env bash
set +e
echo "=== api.log WARN/ERROR last 2h (test window) ==="
awk -v now="$(date -u +%s)" '{ ts=$0; gsub(/^.*"time":"/,"",ts); gsub(/".*$/,"",ts); } /"level":"(WARN|ERROR)"/' /tmp/api.log | tail -20
echo "--- counts ---"
echo "api WARN: $(grep -c '"level":"WARN"' /tmp/api.log)"
echo "api ERROR: $(grep -c '"level":"ERROR"' /tmp/api.log)"
echo "worker WARN: $(grep -c '"level":"WARN"' /tmp/worker.log)"
echo "worker ERROR: $(grep -c '"level":"ERROR"' /tmp/worker.log)"
echo "=== worker WARN/ERROR last 25 ==="
grep '"level":"(WARN|ERROR)"' /tmp/worker.log | tail -25
