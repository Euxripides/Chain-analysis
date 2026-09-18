#!/usr/bin/env bash
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$P/deploy/.env"; set +a
[ -f /tmp/s6-e2e-env.sh ] && . /tmp/s6-e2e-env.sh
[ -f /tmp/dual-system-fixture.env ] && . /tmp/dual-system-fixture.env
export PATH="/home/euripides/.nvm/versions/node/v24.20.0/bin:/usr/local/go/bin:$PATH"
echo "== worker log =="
journalctl --user -u blockchain-worker --since "-5 min" --no-pager | grep -vE 'systemd\[' | tail -12
echo "worker pid=$(pgrep -f 'backend/bin/worker$' | head -1) restarts=$(systemctl --user show blockchain-worker -p NRestarts --value)"
printf 'worker 8081 /health/ready = %s\n' "$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 http://127.0.0.1:8081/health/ready)"
echo
echo "== units =="
for u in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  printf '%-28s %s\n' "$u" "$(systemctl --user is-active $u)"
done
echo
echo "== health =="
printf 'api 8080 ready=%s  nginx 8088 ready=%s  tls=%s\n' \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8080/health/ready)" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 http://127.0.0.1:8088/health/ready)" \
  "$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 https://aurore.online/health/ready)"

# 未改动端点的回归检查（用户要求：已达标的不能变慢）
cookieheader() { awk '/^#HttpOnly_/ { sub(/^#HttpOnly_/,""); printf "%s=%s; ", $6,$7; next } /^#/||/^$/ {next} {printf "%s=%s; ", $6,$7}' "$1"; }
J=$(mktemp)
curl -s -c "$J" -o /dev/null --max-time 15 -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"$INVESTIGATOR_PASSWORD\",\"audience\":\"user\",\"workspace_id\":\"$FIXTURE_WORKSPACE_ID\"}" \
  http://127.0.0.1:8080/api/auth/v1/login
CK=$(cookieheader "$J")
JA=$(mktemp)
curl -s -c "$JA" -o /dev/null --max-time 15 -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" \
  http://127.0.0.1:8080/api/auth/v1/login
CKA=$(cookieheader "$JA")

n=10
printf '\n%-24s %-9s %-9s %-6s %s\n' ENDPOINT p50_ms p95_ms err bytes
extra() {
  local ep="$1" path="$2" jar="$3" ms="" i code t bytes
  for ((i=0;i<n;i++)); do
    read -r code t bytes < <(curl -s -o /dev/null -b "$jar" --max-time 60 -w '%{http_code} %{time_total} %{size_download}' "http://127.0.0.1:8080$path")
    ms="$ms $code:$(python3 -c "print(round($t*1000,2))")"
  done
  python3 -c "
import sys
vals=[float(x.split(':')[1]) for x in '''$ms'''.split()]
errs=sum(1 for x in '''$ms'''.split() if not x.startswith('200:'))
vals.sort()
def p(q):
    k=(len(vals)-1)*q; lo=int(k); hi=min(lo+1,len(vals)-1); return vals[lo]+(vals[hi]-vals[lo])*(k-lo)
print('%-24s %-9.2f %-9.2f %-6d %d' % ('$ep', p(0.5), p(0.95), errs, $bytes if False else 0))
"
}
extra dashboard      "/api/v2/dashboard" "$CK"
extra cases_list     "/api/v2/cases?limit=20" "$CK"
extra jobs_list      "/api/v2/jobs?limit=20" "$CK"
extra notifications  "/api/v2/notifications?limit=20" "$CK"
extra admin_overview "/api/admin/v1/overview" "$CKA"
extra counterparties "/api/v2/explorer/addresses/tron/TCFNp179Lg46D16zKoumd4Poa2WFFdtqYj/counterparties" "$CK"
