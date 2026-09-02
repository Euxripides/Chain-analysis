#!/usr/bin/env bash
set +e
echo "=== worker.log WARN/ERROR tail (direct grep) ==="
grep -E '"(WARN|ERROR)"' /tmp/worker.log | tail -25
echo "=== counts ==="
echo "WARN: $(grep -c '"WARN"' /tmp/worker.log)"
echo "ERROR: $(grep -c '"ERROR"' /tmp/worker.log)"
