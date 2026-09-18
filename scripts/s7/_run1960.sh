set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go vet ./internal/api/ 2>&1 | head -20
go build -o bin/api ./cmd/api
echo BUILD_OK
sha256sum bin/api
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY after ${i}s"; break; }; sleep 1; done
echo SCRIPT_END
