#!/usr/bin/env bash
set +e
for i in $(seq 1 80); do
  btime=$(stat -c %Y /home/euripides/projects/blockchain-platform/backend/bin/worker 2>/dev/null)
  now=$(date +%s)
  if [ -n "$btime" ] && [ $((now - btime)) -lt 180 ]; then
    ok=$(curl -sS -m 3 http://127.0.0.1:8080/health/ready 2>/dev/null | grep -c '"status":"ready"')
    if [ "$ok" = "1" ]; then
      echo "DEPLOYED worker_built_seconds_ago=$((now - btime))"
      pgrep -af 'bin/api'; pgrep -af 'bin/worker'
      exit 0
    fi
  fi
  sleep 5
done
echo TIMEOUT
tail -n 10 /tmp/dev-restart3.log
exit 1
