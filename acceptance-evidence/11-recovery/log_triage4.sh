#!/usr/bin/env bash
set +e
echo "=== stats sync failed samples ==="
grep '"workspace address stats sync failed"' /tmp/worker.log | tail -4
echo "=== logo localization failed ==="
grep '"token logo localization failed"' /tmp/worker.log | tail -2
