#!/bin/bash
# 开发环境一键启动：API + Worker + 前端（需先 make infra-up && make migrate）
set -e
cd "$(dirname "$0")/.."
export PATH=$PATH:/usr/local/go/bin
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"

stop_service() {
  local name="$1" pid_file="$2" pid=""
  if [ -f "$pid_file" ]; then
    pid="$(cat "$pid_file" 2>/dev/null || true)"
  fi
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
  else
    pkill -TERM -x "$name" 2>/dev/null || true
  fi
  for _ in $(seq 1 30); do
    if { [ -z "$pid" ] || ! kill -0 "$pid" 2>/dev/null; } && ! pgrep -x "$name" >/dev/null 2>&1; then
      rm -f "$pid_file"
      return
    fi
    sleep 0.1
  done
  [ -n "$pid" ] && kill -KILL "$pid" 2>/dev/null || true
  pkill -KILL -x "$name" 2>/dev/null || true
  rm -f "$pid_file"
}

wait_http() {
  local name="$1" url="$2" log_file="$3"
  for _ in $(seq 1 30); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  echo "$name failed readiness check: $url" >&2
  tail -n 30 "$log_file" >&2 || true
  exit 1
}

cd backend
set -a && . ../deploy/.env && set +a
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
stop_service api /tmp/blockchain-platform-api.pid
stop_service worker /tmp/blockchain-platform-worker.pid
(setsid ./bin/api < /dev/null > /tmp/api.log 2>&1 & echo $! > /tmp/blockchain-platform-api.pid)
(setsid ./bin/worker < /dev/null > /tmp/worker.log 2>&1 & echo $! > /tmp/blockchain-platform-worker.pid)
cd ../frontend
# Older versions did not keep a PID file; the development frontend owns port 3000.
if [ -f /tmp/blockchain-platform-vite.pid ]; then
  vite_pid="$(cat /tmp/blockchain-platform-vite.pid 2>/dev/null || true)"
  [ -n "$vite_pid" ] && kill "$vite_pid" 2>/dev/null || true
fi
fuser -k 3000/tcp >/dev/null 2>&1 || true
(setsid ./node_modules/.bin/vite --host < /dev/null > /tmp/vite.log 2>&1 & echo $! > /tmp/blockchain-platform-vite.pid)

wait_http API http://127.0.0.1:8080/health/ready /tmp/api.log
wait_http Worker http://127.0.0.1:8081/health/live /tmp/worker.log
wait_http Frontend http://127.0.0.1:3000/ /tmp/vite.log
echo "API:    http://$(hostname -I | awk '{print $1}'):8080  (health: /health/ready)"
echo "Frontend: http://$(hostname -I | awk '{print $1}'):3000"
echo "logs: /tmp/api.log /tmp/worker.log /tmp/vite.log"
