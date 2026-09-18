set -x
export XDG_RUNTIME_DIR=/run/user/$(id -u)
cd /home/euripides/projects/blockchain-platform
cp backend/bin/api backend/bin/api.bak-s7-$(date -u +%Y%m%dT%H%M%SZ)
systemctl --user restart blockchain-api.service
sleep 3
systemctl --user is-active blockchain-api.service
for i in $(seq 1 30); do
  code=$(curl -s -o /tmp/ready.json -w '%{http_code}' --max-time 3 http://127.0.0.1:8080/health/ready || true)
  if [ "$code" = "200" ]; then echo "READY after $i tries: $(cat /tmp/ready.json)"; break; fi
  sleep 1
done
echo "=== version endpoint ==="
curl -s --max-time 5 http://127.0.0.1:8080/health/live; echo
ps -o pid,lstart,args -p $(pgrep -f 'bin/api' | head -1)