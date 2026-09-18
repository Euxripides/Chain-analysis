#!/usr/bin/env bash
# S7-WAL-01：部署（备份 → mv 原子替换 → 分单元重启）+ 部署前后「应用侧写入速率」对照采样。
#
# 采样口径：pg_stat_user_tables 的 n_tup_ins / n_tup_upd / n_tup_hot_upd 差分，
# 与「同一任务量」直接对应（进度事件由活跃 job 产生，所以同时记录活跃 job 数）。
set -uo pipefail
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
P=/home/euripides/projects/blockchain-platform
BK="$HOME/deploy-backups/pre-wal-$(date -u +%Y%m%dT%H%M%SZ)"
export PATH="/usr/local/go/bin:$PATH"
set -a; . "$P/deploy/.env"; set +a
PSQL() { docker exec deploy-postgres-1 psql -U "${POSTGRES_USER:-blockchain}" -d "${POSTGRES_DB:-blockchain}" -tAc "$1"; }
Q="SELECT relname||'|'||n_tup_ins||'|'||n_tup_upd||'|'||n_tup_hot_upd FROM pg_stat_user_tables WHERE relname IN ('job_event_log','count_provider_capabilities','count_provider_runtime_profiles','count_oracle_status','ankr_performance_control','count_provider_registry','ankr_scheduler_state','rpc_provider_health')"
LOAD() { echo "active_jobs=$(PSQL "SELECT count(*) FROM download_jobs WHERE status IN ('CREATED','PLANNING','QUEUED','RUNNING','PAUSE_REQUESTED','RESUMING')") lsn=$(PSQL 'SELECT pg_current_wal_lsn()') wal_bytes=$(PSQL 'SELECT wal_bytes FROM pg_stat_wal')"; }

snap() { LOAD; Q=$(PSQL "$Q"); }

echo "======================= BEFORE 采样 120s ======================="
LOAD; A=$(PSQL "$Q"); sleep 120; B=$(PSQL "$Q"); echo "BEFORE_LSN: $(PSQL 'SELECT pg_current_wal_lsn()')"
python3 - "$A" "$B" <<'PY'
import sys
def p(s):
    d={}
    for l in s.strip().splitlines():
        x=l.split('|')
        if len(x)==4: d[x[0]]=tuple(int(v) for v in x[1:])
    return d
a,b=p(sys.argv[1]),p(sys.argv[2])
print("%-34s %10s %10s %10s" % ("table","ins/min","upd/min","hot/min"))
tot=0
for t in sorted(b):
    if t not in a: continue
    di=[(b[t][i]-a[t][i])/2 for i in range(3)]
    tot+=di[0]+di[1]
    print("%-34s %10.1f %10.1f %10.1f" % (t,di[0],di[1],di[2]))
print("TOTAL row-writes/min = %.1f" % tot)
PY

echo
echo "======================= 部署 ======================="
mkdir -p "$BK"
cp -av "$P/backend/bin/api"    "$BK/api.frozen-before"    >/dev/null
cp -av "$P/backend/bin/worker" "$BK/worker.frozen-before" >/dev/null
sha256sum "$BK"/api.frozen-before "$BK"/worker.frozen-before
echo "new api sha:    $(sha256sum "$P/backend/bin/api.new" | awk '{print $1}')"
echo "new worker sha: $(sha256sum "$P/backend/bin/worker.new" | awk '{print $1}')"
mv -v "$P/backend/bin/api.new" "$P/backend/bin/api"
mv -v "$P/backend/bin/worker.new" "$P/backend/bin/worker"
systemctl --user restart blockchain-api
for i in $(seq 1 60); do
  [ "$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 http://127.0.0.1:8080/health/live)" = "200" ] && { echo "api live after ${i}s"; break; }
  sleep 1
done
echo "api ready=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 http://127.0.0.1:8080/health/ready)"
systemctl --user stop blockchain-worker; sleep 2
systemctl --user start blockchain-worker; sleep 3
echo "worker=$(systemctl --user is-active blockchain-worker) restarts=$(systemctl --user show blockchain-worker -p NRestarts --value)"
echo "running api sha: $(sha256sum /proc/$(pgrep -f 'backend/bin/api$' | head -1)/exe | awk '{print $1}')"
echo "backup dir: $BK"

echo
echo "======================= AFTER 采样 120s ======================="
LOAD; A=$(PSQL "$Q"); sleep 120; B=$(PSQL "$Q"); echo "AFTER_LSN: $(PSQL 'SELECT pg_current_wal_lsn()')"
python3 - "$A" "$B" <<'PY'
import sys
def p(s):
    d={}
    for l in s.strip().splitlines():
        x=l.split('|')
        if len(x)==4: d[x[0]]=tuple(int(v) for v in x[1:])
    return d
a,b=p(sys.argv[1]),p(sys.argv[2])
print("%-34s %10s %10s %10s" % ("table","ins/min","upd/min","hot/min"))
tot=0
for t in sorted(b):
    if t not in a: continue
    di=[(b[t][i]-a[t][i])/2 for i in range(3)]
    tot+=di[0]+di[1]
    print("%-34s %10.1f %10.1f %10.1f" % (t,di[0],di[1],di[2]))
print("TOTAL row-writes/min = %.1f" % tot)
PY
