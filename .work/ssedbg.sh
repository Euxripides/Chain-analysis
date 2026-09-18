#!/usr/bin/env bash
set -uo pipefail
echo "== what did the SSE endpoint return? =="
head -c 600 /tmp/sse.txt; echo
echo "== retry with verbose status =="
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
[ -f /tmp/s6-e2e-env.sh ] && . /tmp/s6-e2e-env.sh
[ -f /tmp/dual-system-fixture.env ] && . /tmp/dual-system-fixture.env
echo "INVESTIGATOR_PASSWORD set: $([ -n "${INVESTIGATOR_PASSWORD:-}" ] && echo yes || echo NO)"
echo "FIXTURE_WORKSPACE_ID: ${FIXTURE_WORKSPACE_ID:-unset}"
cookieheader() { awk '/^#HttpOnly_/ { sub(/^#HttpOnly_/,""); printf "%s=%s; ", $6,$7; next } /^#/||/^$/ {next} {printf "%s=%s; ", $6,$7}' "$1"; }
J=$(mktemp)
curl -s -c "$J" -o /tmp/login.out -w 'login_http=%{http_code}\n' --max-time 15 -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"${INVESTIGATOR_PASSWORD:-}\",\"audience\":\"user\",\"workspace_id\":\"${FIXTURE_WORKSPACE_ID:-}\"}" \
  http://127.0.0.1:8080/api/auth/v1/login
head -c 300 /tmp/login.out; echo
CK=$(cookieheader "$J")
JOBID=$(docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "SELECT id FROM download_jobs ORDER BY updated_at DESC LIMIT 1")
echo "job=$JOBID"
curl -s -o /tmp/sse2.txt -w 'sse_http=%{http_code} time=%{time_total}\n' -b "$CK" --max-time 8 "http://127.0.0.1:8080/api/v2/jobs/$JOBID/events"
head -c 400 /tmp/sse2.txt; echo
