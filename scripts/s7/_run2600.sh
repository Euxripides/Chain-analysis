set -e
cd /home/euripides/projects/blockchain-platform/backend && export PATH=/usr/local/go/bin:$PATH
go vet ./internal/api/ 2>&1 | head -10
go build -o bin/api ./cmd/api && echo BUILD_OK
cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
echo "=== counterparties 修复后 ==="
curl -s -o /tmp/cp.json -w 'HTTP %{http_code}  %{time_total}s  %{size_download}B\n' -b "$J" "http://127.0.0.1:8080/api/v2/explorer/addresses/tron/$A/counterparties"
python3 -c "
import json
d=json.load(open('/tmp/cp.json'))
items=(d.get('data') or {}).get('items') or []
print('items:', len(items))
for it in items[:4]: print(' ', it['counterparty'], it['transaction_count'], it['in_raw'], it['out_raw'])
"
echo SCRIPT_END
