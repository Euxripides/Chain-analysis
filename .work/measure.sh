#!/usr/bin/env bash
# S7-WAL-01 采样器：**不做任何表扫描**（避免 hint-bit 弄脏页面、污染协调方的 WAL 窗口）。
# 只用 pg_stat_* 计数器 + NATS 监控 + LSN。
# 用法：bash measure.sh <label> [秒]
set -uo pipefail
set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
LABEL="${1:-sample}"; DUR="${2:-90}"
NATS="http://127.0.0.1:${NATS_MONITOR_PORT:-8222}/subsz?subs=1"

snap() {
  local tat
  tat=$(PSQL "SELECT string_agg(relname||'='||n_tup_ins||','||n_tup_upd, ';') FROM pg_stat_user_tables WHERE relname IN ('job_event_log','count_provider_capabilities','count_provider_runtime_profiles','count_oracle_status','ankr_performance_control','count_provider_registry','ankr_scheduler_state','rpc_provider_health')")
  local nats
  nats=$(curl -s --max-time 5 "$NATS" | python3 -c "import json,sys;d=json.load(sys.stdin);print(sum(s.get('msgs',0) for s in d.get('subscriptions_list',[]) if s.get('subject')=='events.>'))")
  echo "TAT|$tat"
  echo "NATS|$nats"
  echo "LSN|$(PSQL 'SELECT pg_current_wal_lsn()')"
  echo "WALB|$(PSQL 'SELECT wal_bytes FROM pg_stat_wal')"
  echo "ACTIVE|$(PSQL "SELECT count(*) FROM download_jobs WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')")"
}
export -f snap 2>/dev/null || true

A=$(snap); sleep "$DUR"; B=$(snap)
python3 - "$LABEL" "$DUR" "$A" "$B" <<'PY'
import sys, re
label,dur = sys.argv[1], int(sys.argv[2])
def parse(blob):
    out={}
    for line in blob.strip().splitlines():
        if '|' in line:
            k,v=line.split('|',1); out[k]=v
    return out
a,b=parse(sys.argv[3]),parse(sys.argv[4])
print("=== %s (window %ds) ===" % (label,dur))
print("active_jobs = %s" % b.get('ACTIVE'))
def tab(s):
    d={}
    for part in s.split(';'):
        if '=' not in part: continue
        n,vals=part.split('='); i,u=vals.split(','); d[n]=(int(i),int(u))
    return d
ta,tb=tab(a['TAT']),tab(b['TAT'])
print("%-34s %10s %10s %10s" % ("table","ins/min","upd/min","writes/min"))
tot=0
for t in sorted(tb):
    di=(tb[t][0]-ta[t][0])*60.0/dur; du=(tb[t][1]-ta[t][1])*60.0/dur
    tot+=di+du
    print("%-34s %10.1f %10.1f %10.1f" % (t,di,du,di+du))
print("TOTAL app row-writes/min = %.1f" % tot)
n=(int(b['NATS'])-int(a['NATS']))*60.0/dur
print("NATS events.> delivery rate = %.1f msg/min  (推送到 API 的事件速率，节流不应影响它)" % n)
def lsn(s):
    x,y=s.split('/'); return int(x,16)*(2**32)+int(y,16)
mb=(lsn(b['LSN'])-lsn(a['LSN']))/1048576.0
print("WAL LSN advance   = %.2f MB in %ds => %.2f MB/min" % (mb,dur,mb*60.0/dur))
wb=(int(b['WALB'])-int(a['WALB']))/1048576.0
print("WAL wal_bytes     = %.2f MB in %ds => %.2f MB/min" % (wb,dur,wb*60.0/dur))
jel = (tb.get('job_event_log',(0,0))[0]-ta.get('job_event_log',(0,0))[0])*60.0/dur
ratio = (n/jel) if jel>0 else float('inf')
print("job_event_log INSERT rate = %.1f rows/min" % jel)
print("=> NATS 推送事件 / job_event_log 落盘行 = %.2f  （节流前应≈1，节流后应≈10）" % ratio)
PY
