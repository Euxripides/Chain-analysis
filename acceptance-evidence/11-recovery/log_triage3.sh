#!/usr/bin/env bash
set +e
echo "=== distinct ERROR messages ==="
grep '"ERROR"' /tmp/worker.log | grep -oE '"msg":"[^"]*"' | sort | uniq -c | sort -rn
echo "=== distinct WARN messages ==="
grep '"WARN"' /tmp/worker.log | grep -oE '"msg":"[^"]*"' | sort | uniq -c | sort -rn | head -12
