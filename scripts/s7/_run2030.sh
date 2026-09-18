cd /home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user restart blockchain-api.service
for i in $(seq 1 60); do curl -sf -o /dev/null --max-time 2 http://127.0.0.1:8080/health/ready && { echo "READY ${i}s"; break; }; sleep 1; done
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
BASE=http://127.0.0.1:8080
RND=$(python3 -c "import uuid;print(uuid.uuid4())")
echo "=== 未认证 DELETE /api/v1/cases/{random}  (期望 401) ==="
curl -s -o /tmp/x1 -w '%{http_code} ' -X DELETE "$BASE/api/v1/cases/$RND"; head -c 120 /tmp/x1; echo
echo "=== 未认证 PATCH /api/v1/api-control/control/kill-switch (期望 401) ==="
curl -s -o /tmp/x2 -w '%{http_code} ' -X PATCH -H 'Content-Type: application/json' -d '{}' "$BASE/api/v1/api-control/control/kill-switch"; head -c 120 /tmp/x2; echo
echo "=== 未认证 POST /api/v1/exports (期望 401) ==="
curl -s -o /tmp/x3 -w '%{http_code} ' -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/v1/exports"; head -c 120 /tmp/x3; echo
V=$(mktemp)
curl -s -c "$V" -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-viewer@fixture.local\",\"password\":\"$VIEWER_PASSWORD\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login" >/dev/null
CS=$(grep -i 'csrf' "$V" | awk '{print $7}')
echo "=== viewer DELETE /api/v1/cases (期望 403) ==="
curl -s -o /tmp/x4 -w '%{http_code} ' -b "$V" -H "X-CSRF-Token: $CS" -X DELETE "$BASE/api/v1/cases/$RND"; head -c 160 /tmp/x4; echo
echo "=== 未认证 GET /api/v1/cases (读路径仍需可用, 期望 200) ==="
curl -s -o /dev/null -w '%{http_code}\n' "$BASE/api/v1/cases"
echo "=== v2 仍正常 ==="
curl -s -o /dev/null -w 'v2 dashboard %{http_code}\n' -b "$V" "$BASE/api/v2/dashboard"
echo SCRIPT_END
