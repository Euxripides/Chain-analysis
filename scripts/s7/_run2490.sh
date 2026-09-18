cd /home/euripides/projects/blockchain-platform/acceptance-evidence/dual-system-v1/tests/sprint7/perf
python3 -c "
import json
d=json.load(open('lan_dashboard.json'))
print('status_counts:', d['status_counts'])
print('error_samples:', d.get('error_samples'))
print('avg_bytes:', d['avg_response_bytes'])
"
echo "=== 手工复核：用同样的 cookie 手工请求 ==="
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a; . /tmp/s6-e2e-env.sh
J=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\"}" http://127.0.0.1:8080/api/auth/v1/login
echo "--- jar ---"; grep -v '^#' "$J" | head -4 | awk '{print $6, "=", substr($7,1,8)"..."}'
CK=$(awk '/^#|^$/ {next} {printf "%s=%s; ", $6, $7}' "$J")
echo "--- cookie header: $(echo "$CK" | sed -E 's/=[^;]{8}[^;]*/=REDACTED/g')"
curl -s -o /dev/null -w '手工 GET /api/v2/dashboard -> %{http_code}\n' -H "Cookie: $CK" http://127.0.0.1:8080/api/v2/dashboard
echo SCRIPT_END
