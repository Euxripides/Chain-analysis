#!/usr/bin/env bash
# ENV-003 config mode & secret hygiene (values redacted)
set +e
echo "=== sudo check ==="
sudo -n true 2>&1 && echo SUDO_OK || echo SUDO_NEEDS_PASSWORD
echo "=== api pid ==="
api_pid=$(pgrep -f 'bin/api' | head -1); echo "api_pid=$api_pid"
worker_pid=$(pgrep -f 'bin/worker' | head -1); echo "worker_pid=$worker_pid"
echo "=== api env (names only) ==="
tr '\0' '\n' < /proc/$api_pid/environ | grep -E '^(ANKR_MODE|RPC_ENABLED|RPC_CHAINS|ANKR_RPC_ENABLED|API_PORT|WORKER_METRICS_PORT|POSTGRES|CLICKHOUSE|MINIO|NATS)' | sed -E 's/=.*/=[SET]/'
echo "=== worker env (names only) ==="
tr '\0' '\n' < /proc/$worker_pid/environ | grep -E '^(ANKR_MODE|RPC_ENABLED|RPC_CHAINS|ANKR_RPC_ENABLED|POSTGRES|CLICKHOUSE|MINIO|NATS)' | sed -E 's/=.*/=[SET]/'
echo "=== api cmdline ==="
tr '\0' ' ' < /proc/$api_pid/cmdline; echo
echo "=== worker cmdline ==="
tr '\0' ' ' < /proc/$worker_pid/cmdline; echo
echo "=== secret scan in cmdline/env for API keys ==="
tr '\0' ' ' < /proc/$api_pid/cmdline | grep -Eio 'ankr|api[_-]?key|bearer' || echo "no key patterns in api cmdline"
echo "=== api.log tail ==="
tail -n 5 /tmp/api.log
echo "=== worker.log tail ==="
tail -n 5 /tmp/worker.log
