#!/usr/bin/env bash
set +e
source /home/euripides/projects/blockchain-platform/deploy/.env 2>/dev/null
api_pid=$(pgrep -f 'bin/api' | head -1)
echo "api pid=$api_pid"
kill -TERM $api_pid
sleep 3
pgrep -f 'bin/api' || echo "api stopped"
cd /home/euripides/projects/blockchain-platform/backend
set -a; source /home/euripides/projects/blockchain-platform/deploy/.env; set +a
nohup ./bin/api >> /tmp/api.log 2>&1 &
echo "api restarted pid=$!"
sleep 6
pgrep -af 'bin/api'
curl -sS -w '|%{http_code}
' http://127.0.0.1:8080/health/ready
