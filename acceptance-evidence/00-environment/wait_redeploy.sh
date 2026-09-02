#!/usr/bin/env bash
set +e
# wait until new api binary appears and health is ready
for i in $(seq 1 60); do
  ls -la /home/euripides/projects/blockchain-platform/backend/bin/api >/dev/null 2>&1
  btime=$(stat -c %Y /home/euripides/projects/blockchain-platform/backend/bin/api 2>/dev/null)
  now=$(date +%s)
  if [ -n "$btime" ] && [ $((now - btime)) -lt 120 ]; then
    ok=$(curl -sS -m 3 http://127.0.0.1:8080/health/ready 2>/dev/null | grep -c '"status":"ready"')
    if [ "$ok" = "1" ]; then
      echo "DEPLOYED binary_time=$btime now=$now"
      pgrep -af 'bin/api'
      pgrep -af 'bin/worker'
      exit 0
    fi
  fi
  sleep 5
done
echo "TIMEOUT waiting for redeploy"
tail -n 10 /tmp/dev-restart2.log 2>/dev/null
exit 1
