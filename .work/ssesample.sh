#!/usr/bin/env bash
# SSE 推送频率 vs job_event_log 落盘频率的同窗口对照。
# 前者的“1 秒”是需求 1（不得下降），后者的下降是需求 2。
set -uo pipefail
P=/home/euripides/projects/blockchain-platform
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$P/deploy/.env"; set +a
[ -f /tmp/s6-e2e-env.sh ] && . /tmp/s6-e2e-env.sh
[ -f /tmp/dual-system-fixture.env ] && . /tmp/dual-system-fixture.env
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
LABEL="${1:-sample}"
DUR="${2:-30}"

JOBID=$(PSQL "SELECT id FROM download_jobs WHERE status IN ('RUNNING','QUEUED','CREATED','PLANNING','RESUMING','PAUSE_REQUESTED') ORDER BY updated_at DESC LIMIT 1")
ACTIVE=$(PSQL "SELECT count(*) FROM download_jobs WHERE status IN ('RUNNING','QUEUED','CREATED','PLANNING','RESUMING','PAUSE_REQUESTED')")
echo "== $LABEL: active_jobs=$ACTIVE sse_job=$JOBID dur=${DUR}s =="

cookieheader() { awk '/^#HttpOnly_/ { sub(/^#HttpOnly_/,""); printf "%s=%s; ", $6,$7; next } /^#/||/^$/ {next} {printf "%s=%s; ", $6,$7}' "$1"; }
J=$(mktemp)
curl -s -c "$J" -o /dev/null --max-time 15 -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"fixture-investigator@fixture.local\",\"password\":\"${INVESTIGATOR_PASSWORD}\",\"audience\":\"user\",\"workspace_id\":\"${FIXTURE_WORKSPACE_ID}\"}" \
  http://127.0.0.1:8080/api/auth/v1/login
CK=$(cookieheader "$J")

BEFORE_LOG=$(PSQL "SELECT count(*) FROM job_event_log")
BEFORE_PROG=$(PSQL "SELECT count(*) FROM job_event_log WHERE event_type='progress'")
BEFORE_LSN=$(PSQL "SELECT pg_current_wal_lsn()")
T0=$(date +%s)

curl -s -N -b "$CK" --max-time "$DUR" "http://127.0.0.1:8080/api/v2/jobs/$JOBID/events" > /tmp/sse.txt 2>/dev/null || true

T1=$(date +%s); EL=$((T1-T0))
AFTER_LOG=$(PSQL "SELECT count(*) FROM job_event_log")
AFTER_PROG=$(PSQL "SELECT count(*) FROM job_event_log WHERE event_type='progress'")
AFTER_LSN=$(PSQL "SELECT pg_current_wal_lsn()")

python3 - "$LABEL" "$EL" "$BEFORE_LOG" "$AFTER_LOG" "$BEFORE_PROG" "$AFTER_PROG" "$ACTIVE" "$BEFORE_LSN" "$AFTER_LSN" <<'PY'
import sys
label,el,bl,al,bp,ap,active,l1,l2 = sys.argv[1:10]
el=int(el)
print("window = %ds, active_jobs = %s" % (el, active))
print("job_event_log   rows: %s -> %s  = +%d  (%.1f rows/min)" % (bl,al,int(al)-int(bl),(int(al)-int(bl))*60.0/el))
print("  of which progress:  %s -> %s  = +%d  (%.1f rows/min)" % (bp,ap,int(ap)-int(bp),(int(ap)-int(bp))*60.0/el))
def lsn(s):
    a,b=s.split('/'); return int(a,16)*(2**32)+int(b,16)
print("WAL LSN advance    : %.2f MB in %ds => %.2f MB/min" % ((lsn(l2)-lsn(l1))/1048576.0, el, (lsn(l2)-lsn(l1))/1048576.0*60.0/el))
PY
echo "-- SSE frames in the same window --"
echo "total frames      : $(grep -c '^event:' /tmp/sse.txt || true)"
echo "by event type     :"
grep '^event:' /tmp/sse.txt | sort | uniq -c | sed 's/^/    /'
echo "frames with id:   : $(grep -c '^id:' /tmp/sse.txt || true)"
