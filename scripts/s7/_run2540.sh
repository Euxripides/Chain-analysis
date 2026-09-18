cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login
A=TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj
echo "=== counterparties 完整响应 ==="
curl -s -b "$J" "http://127.0.0.1:8080/api/v2/explorer/addresses/tron/$A/counterparties" | head -c 600; echo
echo "=== API 日志中的错误 ==="
grep -i 'counterpart' /tmp/api.log | tail -5 | cut -c1-300
echo "=== 单请求延迟（无并发竞争） ==="
for u in "activity?limit=200" "activity?limit=50" "flow-profile" "overview" "assets"; do
  t=$(curl -s -o /dev/null -w '%{time_total}' -b "$J" "http://127.0.0.1:8080/api/v2/explorer/addresses/tron/$A/$u")
  printf '%-24s %ss\n' "$u" "$t"
done
echo SCRIPT_END
