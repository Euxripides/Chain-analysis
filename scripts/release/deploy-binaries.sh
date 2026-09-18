#!/usr/bin/env bash
# deploy-binaries.sh — 原子部署 backend/bin/{api,worker,migrate} 并记录真实运行身份。
#
# 设计原则（§29.1 / 交接硬约束）：
#   - 只接受"已冻结的构建证据目录"作为输入，拒绝按名字安装；
#   - 旧产物先备份（保留，不删除），再 .tmp + 原子 mv 安装；
#   - 部署后必须核对：systemd ExecStart、EnvironmentFile、MainPID、/proc/<pid>/exe 的 sha256；
#   - 不接受"文件 sha 相同"作为运行证据——必须 /proc 实际可执行文件；
#   - 重启前后记录 download_jobs 状态普查，用户任务不得被取消/清理。
set -uo pipefail

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${1:-}"
TS="${TS:-$(date -u +%Y%m%dT%H%M%SZ)}"
PG="docker exec deploy-postgres-1 psql -U blockchain -d blockchain_platform -tAc"

if [ -z "$EV" ] || [ ! -d "$EV" ]; then echo "usage: $0 <evidence-dir-with-*.build>"; exit 2; fi
for b in api worker migrate; do
  [ -f "$EV/$b.build" ] || { echo "missing $EV/$b.build"; exit 2; }
done

echo "=== deploy-binaries $TS from $EV ==="
echo "deploy_script_sha256=$(sha256sum "$0" | cut -d' ' -f1)"

# ---- 0. 部署前普查 ---------------------------------------------------------
PRE="$EV/deploy-prestate.txt"
{
  echo "captured_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "--- current bin sha256 ---"
  sha256sum "$PROJECT/backend/bin/api" "$PROJECT/backend/bin/worker" "$PROJECT/backend/bin/migrate"
  echo "--- current running identity ---"
  for u in blockchain-api blockchain-worker; do
    pid=$(systemctl --user show "$u.service" -p MainPID --value)
    echo "$u MainPID=$pid exe=$(readlink -f /proc/$pid/exe 2>/dev/null) exe_sha=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1) started=$(ps -o lstart= -p $pid 2>/dev/null)"
  done
  echo "--- download_jobs status census (pre) ---"
  $PG "select status||'='||count(*) from download_jobs group by status order by 1" 2>&1
  echo "--- download_jobs per org/ws census (pre) ---"
  $PG "select organization_id||'|'||coalesce(workspace_id::text,'NULL')||'|'||status||'|'||count(*) from download_jobs group by 1,2,3 order by 1,2,3" 2>&1
} | tee "$PRE"

# ---- 1. 备份旧产物（保留） --------------------------------------------------
BAK="$PROJECT/backend/bin/.bak-deploy-$TS"
mkdir -p "$BAK"
for b in api worker migrate; do
  cp -p "$PROJECT/backend/bin/$b" "$BAK/$b"
done
sha256sum "$BAK"/* > "$BAK/SHA256SUMS.txt"
echo "backup_dir=$BAK"
cat "$BAK/SHA256SUMS.txt"

# ---- 2. 原子安装 -----------------------------------------------------------
RC=0
for b in api worker migrate; do
  cp -f "$EV/$b.build" "$PROJECT/backend/bin/$b.tmp-deploy"
  chmod 755 "$PROJECT/backend/bin/$b.tmp-deploy"
  mv -f "$PROJECT/backend/bin/$b.tmp-deploy" "$PROJECT/backend/bin/$b"
done
sha256sum "$PROJECT/backend/bin/api" "$PROJECT/backend/bin/worker" "$PROJECT/backend/bin/migrate" > "$EV/deployed-bin-sha256.txt"
cat "$EV/deployed-bin-sha256.txt"

# 安装一致性：bin 必须与冻结产物逐字节一致
for b in api worker migrate; do
  a=$(sha256sum "$EV/$b.build" | cut -d' ' -f1)
  c=$(sha256sum "$PROJECT/backend/bin/$b" | cut -d' ' -f1)
  [ "$a" = "$c" ] || { echo "INSTALL MISMATCH $b: $a != $c"; RC=1; }
done
echo "install_consistent_rc=$RC"
[ "$RC" -eq 0 ] || exit 3

# ---- 3. 重启 ---------------------------------------------------------------
systemctl --user restart blockchain-api.service
sleep 4
systemctl --user restart blockchain-worker.service
sleep 6

# ---- 4. 运行身份核验 -------------------------------------------------------
POST="$EV/deploy-postrun.txt"
{
  echo "captured_utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for u in blockchain-api blockchain-worker; do
    echo "[$u]"
    systemctl --user show "$u.service" -p MainPID -p ExecStart -p EnvironmentFiles -p ActiveState -p SubState --no-pager
    pid=$(systemctl --user show "$u.service" -p MainPID --value)
    echo "  exe=$(readlink -f /proc/$pid/exe 2>/dev/null)"
    echo "  exe_sha256=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)"
    echo "  started=$(ps -o lstart= -p $pid 2>/dev/null)"
  done
  echo "--- download_jobs status census (post) ---"
  $PG "select status||'='||count(*) from download_jobs group by status order by 1" 2>&1
} | tee "$POST"

# 期望值比对
RUNFAIL=0
for pair in "api:blockchain-api" "worker:blockchain-worker"; do
  b="${pair%%:*}"; u="${pair##*:}"
  want=$(sha256sum "$EV/$b.build" | cut -d' ' -f1)
  pid=$(systemctl --user show "$u.service" -p MainPID --value)
  got=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)
  if [ "$want" = "$got" ]; then echo "RUN OK $u pid=$pid sha=$got"; else echo "RUN MISMATCH $u pid=$pid want=$want got=$got"; RUNFAIL=1; fi
done
echo "run_identity_mismatch=$RUNFAIL"
exit "$RUNFAIL"
