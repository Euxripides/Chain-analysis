#!/usr/bin/env bash
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
Q="SELECT relname||'|'||n_tup_ins||'|'||n_tup_upd||'|'||n_tup_hot_upd||'|'||n_tup_del FROM pg_stat_user_tables"
echo "== recent job_event_log payload sizes (newest 300) =="
PSQL "SELECT min(pg_column_size(payload))||' / '||round(avg(pg_column_size(payload)))||' / '||max(pg_column_size(payload)) AS min_avg_max FROM (SELECT payload FROM job_event_log ORDER BY id DESC LIMIT 300) s;"
PSQL "SELECT event_type||' n='||count()||' avg_bytes='||round(avg(pg_column_size(payload))) FROM (SELECT event_type, payload FROM job_event_log ORDER BY id DESC LIMIT 2000) s GROUP BY event_type ORDER BY count() DESC LIMIT 10;"
echo "== sampling 60s of per-table tuple writes =="
A=$(PSQL "$Q" | sort); sleep 60; B=$(PSQL "$Q" | sort)
python3 - "$A" "$B" <<'PY'
import sys
def parse(blob):
    d={}
    for line in blob.strip().splitlines():
        p=line.split('|')
        if len(p)==5: d[p[0]]=tuple(int(x) for x in p[1:])
    return d
a,b=parse(sys.argv[1]),parse(sys.argv[2])
rows=[]
for t in b:
    if t not in a: continue
    di=[b[t][i]-a[t][i] for i in range(4)]
    tot=di[0]+di[1]+di[2]
    if tot: rows.append((tot,di,t))
rows.sort(reverse=True)
print("%-34s %8s %8s %8s %8s %8s" % ("table","writes/min","ins","upd","hot_upd","del"))
for tot,di,t in rows[:18]:
    print("%-34s %8d %8d %8d %8d %8d" % (t,tot,di[0],di[1],di[2],di[3]))
PY
echo "== WAL totals (cluster) =="
PSQL "SELECT wal_records||' records, '||pg_size_pretty(wal_bytes)||' total since '||stats_reset AS wal FROM pg_stat_wal;"
