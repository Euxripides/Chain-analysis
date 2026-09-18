set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH && go build -o bin/api ./cmd/api && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform; export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && break; sleep 1; done
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login
curl -s -o /tmp/cp.json -w 'HTTP %{http_code} %{time_total}s\n' -b "$J" "http://127.0.0.1:8080/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/counterparties"
head -c 300 /tmp/cp.json; echo
echo "=== api.log 中的 scan 错误 ==="
grep -i 'counterparties scan failed' /tmp/api.log | tail -2 | cut -c1-400
echo SCRIPT_END
