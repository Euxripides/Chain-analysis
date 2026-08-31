#!/bin/bash
# 开发环境一键启动：API + Worker + 前端（需先 make infra-up && make migrate）
set -e
cd "$(dirname "$0")/.."
export PATH=$PATH:/usr/local/go/bin
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"

cd backend
set -a && . ../deploy/.env && set +a
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
pkill -9 -x api 2>/dev/null || true
pkill -9 -x worker 2>/dev/null || true
(setsid nohup ./bin/api    < /dev/null > /tmp/api.log    2>&1 &)
(setsid nohup ./bin/worker < /dev/null > /tmp/worker.log 2>&1 &)
cd ../frontend
(setsid nohup ./node_modules/.bin/vite --host < /dev/null > /tmp/vite.log 2>&1 &)

sleep 3
echo "API:    http://$(hostname -I | awk '{print $1}'):8080  (health: /health/ready)"
echo "Frontend: http://$(hostname -I | awk '{print $1}'):3000"
echo "logs: /tmp/api.log /tmp/worker.log /tmp/vite.log"
