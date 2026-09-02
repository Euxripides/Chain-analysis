#!/usr/bin/env bash
set +e
echo "=== worker pid before ==="
pgrep -af 'bin/worker'
echo "=== SIGTERM worker ==="
kill -TERM 1684126
echo "sent SIGTERM"
for i in $(seq 1 20); do
  if ! pgrep -f 'bin/worker' >/dev/null; then echo "worker exited"; break; fi
  sleep 1
done
pgrep -af 'bin/worker' || echo "no worker process"
echo "=== worker log tail ==="
tail -n 5 /tmp/worker.log
