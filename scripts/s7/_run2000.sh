cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
BASE=http://127.0.0.1:8080
RND=$(python3 -c "import uuid;print(uuid.uuid4())")
echo "=== 未认证 DELETE /api/v1/cases/{random} ==="
curl -s -o /tmp/d1.json -w '%{http_code} ' -X DELETE "$BASE/api/v1/cases/$RND"; head -c 160 /tmp/d1.json; echo
V=$(mktemp)
curl -s -c "$V" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-viewer@fixture.local\",\"password\":\"$VIEWER_PASSWORD\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login" >/dev/null
CS=$(grep -i 'csrf' "$V" | awk '{print $7}')
echo "=== viewer(无 case:delete) DELETE /api/v1/cases/{random} ==="
curl -s -o /tmp/d2.json -w '%{http_code} ' -b "$V" -H "X-CSRF-Token: $CS" -X DELETE "$BASE/api/v1/cases/$RND"; head -c 200 /tmp/d2.json; echo
echo "=== viewer DELETE /api/v2/cases/{random} ==="
curl -s -o /tmp/d3.json -w '%{http_code} ' -b "$V" -H "X-CSRF-Token: $CS" -X DELETE "$BASE/api/v2/cases/$RND"; head -c 200 /tmp/d3.json; echo
echo SCRIPT_END
