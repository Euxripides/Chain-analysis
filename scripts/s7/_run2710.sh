set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
gofmt -w internal/iam/session.go
go vet ./internal/iam/ ./internal/api/ 2>&1 | head -10
go build -o bin/api ./cmd/api && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform; export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
echo "=== 经 Nginx（https 语义）的 Cookie 名 ==="
curl -s -D - -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8088/api/auth/v1/login | grep -i '^set-cookie' | sed -E 's/=[^;]{6,}/=<redacted>/g'
echo SCRIPT_END
