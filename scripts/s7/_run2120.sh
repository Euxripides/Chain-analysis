set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go vet ./internal/api/ 2>&1 | head -10
go build -o bin/api ./cmd/api && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
echo "=== 直连 8080：写请求必须 200（回归验证） ==="
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8080/api/auth/v1/login
CS=$(grep -i 'csrf' "$J" | awk '{print $7}')
printf "audit/verify  -> "; curl -s -o /dev/null -w '%{http_code}\n' -b "$J" -H "X-CSRF-Token: $CS" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' http://127.0.0.1:8080/api/admin/v1/audit/verify
printf "overview      -> "; curl -s -o /dev/null -w '%{http_code}\n' -b "$J" http://127.0.0.1:8080/api/admin/v1/overview
echo "=== 经 Nginx 8088（https 语义）写请求 ==="
J2=$(mktemp)
curl -s -c "$J2" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" http://127.0.0.1:8088/api/auth/v1/login
CS2=$(grep -i 'csrf' "$J2" | awk '{print $7}')
printf "audit/verify  -> "; curl -s -o /dev/null -w '%{http_code}\n' -b "$J2" -H "X-CSRF-Token: $CS2" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' http://127.0.0.1:8088/api/admin/v1/audit/verify
echo "=== 公网 HTTPS 写请求 ==="
J3=$(mktemp)
curl -s -c "$J3" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" https://aurore.online/api/auth/v1/login
CS3=$(grep -i 'csrf' "$J3" | awk '{print $7}')
printf "audit/verify  -> "; curl -s -o /dev/null -w '%{http_code}\n' -b "$J3" -H "X-CSRF-Token: $CS3" -H 'Content-Type: application/json' -X POST -d '{"chain":"audit"}' https://aurore.online/api/admin/v1/audit/verify
echo SCRIPT_END
