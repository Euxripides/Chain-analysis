#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
[ -f /tmp/dual-system-fixture.env ] && . /tmp/dual-system-fixture.env
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
echo "== jobs in fixture workspace =="
PSQL "SELECT count(*) FROM download_jobs WHERE workspace_id='${FIXTURE_WORKSPACE_ID}'"
echo "== jobs by workspace =="
PSQL "SELECT workspace_id||' '||count(*) FROM download_jobs GROUP BY workspace_id ORDER BY count(*) DESC LIMIT 5"
echo "== NATS subscriptions on events.> =="
curl -s --max-time 5 "http://127.0.0.1:${NATS_MONITOR_PORT:-8222}/subsz?subs=1" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin)
for s in d.get('subscriptions_list',[]):
    if 'events' in s.get('subject',''):
        print('subject=%-22s msgs=%-10s cid=%s' % (s['subject'], s.get('msgs'), s.get('cid')))
"
