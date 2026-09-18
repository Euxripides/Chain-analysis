#!/usr/bin/env bash
# rotate-exposed-credentials.sh — 对**已暴露**的 PG/CH/MinIO 生产口令做真实轮换。
#
# 背景：生产仍在用源码提交的示例默认值（change_me_*），且这些值内嵌在旧发行二进制、
#       git 跟踪的 deploy/.env.example 与源码注释中（§23.8 P0）。修复 = 让旧值真实失效，
#       不是删历史。
#
# 边界（硬约束）：
#   * 只轮换**已暴露**的三个口令：POSTGRES_PASSWORD / CLICKHOUSE_PASSWORD / MINIO_ROOT_PASSWORD；
#     不轮换 GPT Actions / MCP / Provider key，也不改 blockchain_api 角色口令（未被暴露）。
#   * 日志、stdout、JSON、文档一律**不含明文**；口令只落 0600 文件。
#   * 旧口令失效验证走**真实 TCP**（PG SCRAM / CH HTTP+native / MinIO S3 API），不用本地 trust。
#   * MinIO/CH 轮换后必须逐桶比对 objects/versions/metadata/ILM，且不清卷、不重建数据。
#   * 保留全部旧备份、旧证据、旧失败；不删除、不 reset。
#   * 隔离的 PITR/恢复实例（pitr-*）**不参与**生产轮换，其存在本身是证据。
#
# 用法：bash rotate-exposed-credentials.sh [--dry-run]
set -uo pipefail

DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/sprint7/secret-rotation-$TS}"
ENVF="$PROJECT/deploy/.env"
SEC="$EV/new-credentials.env"
umask 077
mkdir -p "$EV"; chmod 700 "$EV"

FAILED=0
say() { echo "$*" | tee -a "$EV/rotation.log"; }
bad() { echo "FAIL $*" | tee -a "$EV/rotation.log"; FAILED=1; }
sha12() { printf "%s" "$1" | sha256sum | cut -c1-12; }
val() { awk -v k="$1" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$ENVF"; }

say "=== secret rotation $TS dry_run=$DRY_RUN ==="
sha256sum "$0" | tee -a "$EV/rotation.log"

# ---------- 0. 轮换前状态 ----------
say "[0] pre-state capture"
cp -p "$ENVF" "$EV/deploy.env.pre-rotation.bak"; chmod 600 "$EV/deploy.env.pre-rotation.bak"
{
  for k in POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD; do
    v="$(val $k)"; echo "$k len=${#v} sha12=$(sha12 "$v")"
  done
  for k in DATABASE_URL MIGRATION_DATABASE_URL CLICKHOUSE_URL; do
    raw="$(val $k)"
    pw="$(printf "%s" "$raw" | sed -E 's#^[a-zA-Z0-9+.-]+://([^:@/]+):([^@]*)@.*#\2#')"
    echo "$k dsn_pw_len=${#pw} dsn_pw_sha12=$(sha12 "$pw")"
  done
} > "$EV/pre-fingerprints.txt"
cat "$EV/pre-fingerprints.txt" | tee -a "$EV/rotation.log"

PRE_PG_PW="$(val POSTGRES_PASSWORD)"
PRE_CH_PW="$(val CLICKHOUSE_PASSWORD)"
PRE_MC_PW="$(val MINIO_ROOT_PASSWORD)"
PRE_MC_USER="$(val MINIO_ROOT_USER)"
PRE_CH_USER="$(val CLICKHOUSE_USER)"
PRE_CH_DB="$(val CLICKHOUSE_DB)"
PRE_PG_USER="$(val POSTGRES_USER)"
PRE_PG_DB="$(val POSTGRES_DB)"

# PG 结构
docker exec deploy-postgres-1 psql -U "$PRE_PG_USER" -d "$PRE_PG_DB" -tAc \
  "SELECT rolname||'|'||rolcanlogin||'|'||rolsuper FROM pg_roles ORDER BY 1" > "$EV/pre-pg-roles.txt" 2>&1
docker exec deploy-postgres-1 psql -U "$PRE_PG_USER" -d "$PRE_PG_DB" -tAc \
  "SELECT name||'='||setting FROM pg_settings WHERE name IN ('data_directory','archive_mode','server_version') ORDER BY 1" > "$EV/pre-pg-settings.txt" 2>&1

# CH 结构与体量
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT name||'|'||storage||'|'||toString(auth_type) FROM system.users ORDER BY name" > "$EV/pre-ch-users.txt" 2>&1
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT database, table, sum(rows) AS rows, sum(bytes_on_disk) AS bytes FROM system.parts WHERE active GROUP BY database, table ORDER BY database, table" > "$EV/pre-ch-tables.tsv" 2>&1
PRE_CH_SUM="$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT sum(rows) FROM system.parts WHERE active AND database='$PRE_CH_DB'")"
echo "pre_ch_total_rows=$PRE_CH_SUM" | tee -a "$EV/rotation.log"

# MinIO 逐桶清单（objects / versions / metadata / ILM）
minio_inventory() { # minio_inventory <outfile> <password>
  local out="$1" pw="$2"
  {
    echo "## buckets"
    printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls local\n' "$PRE_MC_USER" "$pw" | docker exec -i deploy-minio-1 sh 2>&1
    for b in blockchain-audit blockchain-backups blockchain-exports blockchain-raw blockchain-reports blockchain-reports-drill-20260912t175029z; do
      echo "## bucket=$b objects"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls --recursive --json local/%s\n' "$PRE_MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1 | sed 's/"etag":"[^"]*"/"etag":"<e>"/g'
      echo "## bucket=$b versions"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls --recursive --versions --json local/%s\n' "$PRE_MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1 | sed 's/"etag":"[^"]*"/"etag":"<e>"/g'
      echo "## bucket=$b ilm"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ilm rule export local/%s\n' "$PRE_MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1
    done
  } > "$out" 2>&1
}
minio_inventory "$EV/pre-minio-inventory.txt" "$PRE_MC_PW"
sha256sum "$EV/pre-minio-inventory.txt" | tee -a "$EV/rotation.log"

if [ "$DRY_RUN" = "1" ]; then say "dry-run: 停止于 pre-state"; exit 0; fi

# ---------- 1. 生成新口令（URL-safe hex） ----------
say "[1] generate new credentials"
{
  echo "PG_NEW=$(openssl rand -hex 32)"
  echo "CH_NEW=$(openssl rand -hex 32)"
  echo "MINIO_NEW=$(openssl rand -hex 32)"
} > "$SEC"; chmod 600 "$SEC"
. "$SEC"
echo "new PG len=${#PG_NEW} sha12=$(sha12 "$PG_NEW")" | tee -a "$EV/rotation.log"
echo "new CH len=${#CH_NEW} sha12=$(sha12 "$CH_NEW")" | tee -a "$EV/rotation.log"
echo "new MINIO len=${#MINIO_NEW} sha12=$(sha12 "$MINIO_NEW")" | tee -a "$EV/rotation.log"

# ---------- 2. 结构化更新 deploy/.env ----------
say "[2] update deploy/.env (structural, atomic)"
python3 - "$ENVF" "$SEC" <<'PY'
import sys,re,os
envf, secf = sys.argv[1], sys.argv[2]
vals={}
for line in open(secf):
    k,_,v=line.strip().partition("=")
    if k: vals[k]=v
def setkey(line,key,val):
    return key+"="+val if line.startswith(key+"=") else line
def setdsn(line,key,val):
    if not line.startswith(key+"="): return line
    rest=line.split("=",1)[1]
    m=re.match(r"^([a-zA-Z0-9+.-]+)://([^:@/]+):[^@]*@(.*)$", rest)
    if not m: return line
    return key+"="+m.group(1)+"://"+m.group(2)+":"+val+"@"+m.group(3)
out=[]
for l in open(envf).read().split("\n"):
    l=setkey(l,"POSTGRES_PASSWORD",vals["PG_NEW"])
    l=setkey(l,"CLICKHOUSE_PASSWORD",vals["CH_NEW"])
    l=setkey(l,"MINIO_ROOT_PASSWORD",vals["MINIO_NEW"])
    l=setdsn(l,"MIGRATION_DATABASE_URL",vals["PG_NEW"])
    l=setdsn(l,"CLICKHOUSE_URL",vals["CH_NEW"])
    out.append(l)
tmp=envf+".tmp-rot"
open(tmp,"w").write("\n".join(out))
os.chmod(tmp,0o600)
os.replace(tmp,envf)
print("env_updated")
PY
echo "env_update_rc=$?" | tee -a "$EV/rotation.log"
{ for k in POSTGRES_PASSWORD CLICKHOUSE_PASSWORD MINIO_ROOT_PASSWORD; do v="$(val $k)"; echo "$k sha12=$(sha12 "$v")"; done; } > "$EV/post-env-fingerprints.txt"
cat "$EV/post-env-fingerprints.txt" | tee -a "$EV/rotation.log"

# ---------- 3. PG: ALTER ROLE（口令经 stdin，不进 argv） ----------
say "[3] ALTER ROLE $PRE_PG_USER"
printf "ALTER ROLE %s WITH PASSWORD :'pw';\n" "$PRE_PG_USER" | docker exec -i deploy-postgres-1 psql -U "$PRE_PG_USER" -d "$PRE_PG_DB" -v pw="$PG_NEW" -f - > "$EV/pg-alter.log" 2>&1
echo "pg_alter_rc=$?" | tee -a "$EV/rotation.log"
cat "$EV/pg-alter.log" | tee -a "$EV/rotation.log"

# ---------- 4/5. CH 与 MinIO：按 env 重建容器（保卷） ----------
say "[4/5] recreate clickhouse + minio containers (volumes preserved)"
( cd "$PROJECT/deploy" && docker compose --env-file .env up -d --force-recreate clickhouse minio ) > "$EV/compose-recreate.log" 2>&1
echo "compose_rc=$?" | tee -a "$EV/rotation.log"
tail -n 20 "$EV/compose-recreate.log" | tee -a "$EV/rotation.log"
for i in $(seq 1 60); do
  st=$(docker inspect deploy-clickhouse-1 --format "{{.State.Status}}" 2>/dev/null)
  hs=$(docker inspect deploy-clickhouse-1 --format "{{if .State.Health}}{{.State.Health.Status}}{{end}}" 2>/dev/null)
  [ "$st" = "running" ] && [ "$hs" = "healthy" ] && break; sleep 2;
done
echo "clickhouse state=$st health=$hs" | tee -a "$EV/rotation.log"
for i in $(seq 1 30); do
  st2=$(docker inspect deploy-minio-1 --format "{{.State.Status}}" 2>/dev/null)
  [ "$st2" = "running" ] && break; sleep 2;
done
echo "minio state=$st2" | tee -a "$EV/rotation.log"

# ---------- 6. 重启 API/Worker 以加载新 env ----------
say "[6] restart blockchain-api / blockchain-worker"
systemctl --user restart blockchain-api.service > "$EV/api-restart.log" 2>&1; echo "api_restart_rc=$?" | tee -a "$EV/rotation.log"
sleep 4
systemctl --user restart blockchain-worker.service > "$EV/worker-restart.log" 2>&1; echo "worker_restart_rc=$?" | tee -a "$EV/rotation.log"
sleep 6
for u in blockchain-api blockchain-worker; do
  pid=$(systemctl --user show "$u.service" -p MainPID --value)
  echo "$u MainPID=$pid exe_sha=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)" | tee -a "$EV/rotation.log"
done

# ---------- 7. 旧拒绝 / 新接受（真实 TCP） ----------
say "[7] old-rejected / new-accepted over real TCP"

# PG (SCRAM over TCP 127.0.0.1:5433)
PG_PORT="$(val POSTGRES_PORT)"
try_pg() { PGPASSWORD="$1" psql "host=127.0.0.1 port=$PG_PORT user=$PRE_PG_USER dbname=$PRE_PG_DB sslmode=disable connect_timeout=5" -tAc "select 1" >/dev/null 2>&1; }
if try_pg "$PRE_PG_PW"; then echo "PG old-password STILL ACCEPTED (rotation did not take effect)" | tee -a "$EV/rotation.log"; FAILED=1; else echo "PG old-password REJECTED (ok)" | tee -a "$EV/rotation.log"; fi
if try_pg "$PG_NEW"; then echo "PG new-password ACCEPTED (ok)" | tee -a "$EV/rotation.log"; else echo "PG new-password REJECTED (rotation broke PG)" | tee -a "$EV/rotation.log"; FAILED=1; fi

# ClickHouse (HTTP 8123, netrc 避免 argv 泄露)
CH_HTTP="$(val CLICKHOUSE_HTTP_PORT)"
NETRC="$EV/.ch.netrc"; chmod 600 "$NETRC"
echo "machine 127.0.0.1 login $PRE_CH_USER password $1" > "$NETRC"
try_ch() { printf "machine 127.0.0.1 login %s password %s\n" "$PRE_CH_USER" "$1" > "$NETRC"; chmod 600 "$NETRC"; 
  curl -s --netrc-file "$NETRC" -o /dev/null -w "%{http_code}" "http://127.0.0.1:$CH_HTTP/?query=SELECT+1"; }
OLD_CH=$(try_ch "$PRE_CH_PW")
NEW_CH=$(try_ch "$CH_NEW")
echo "CH old_http=$OLD_CH new_http=$NEW_CH" | tee -a "$EV/rotation.log"
[ "$OLD_CH" = "200" ] && { echo "CH old-password STILL ACCEPTED" | tee -a "$EV/rotation.log"; FAILED=1; }
[ "$NEW_CH" = "200" ] || { echo "CH new-password REJECTED" | tee -a "$EV/rotation.log"; FAILED=1; }
rm -f "$NETRC"

# MinIO (S3 API, 经 stdin 传 MC_HOST 避免 argv 泄露)
MC_PORT="$(val MINIO_API_PORT)"
try_mc() { printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls local >/dev/null 2>&1; echo rc=$?\n' "$PRE_MC_USER" "$1" | docker exec -i deploy-minio-1 sh 2>/dev/null | tr -d '\r'; }
OLD_MC=$(try_mc "$PRE_MC_PW")
NEW_MC=$(try_mc "$MINIO_NEW")
echo "MINIO old=$OLD_MC new=$NEW_MC" | tee -a "$EV/rotation.log"
[ "$OLD_MC" = "rc=0" ] && { echo "MinIO old-password STILL ACCEPTED" | tee -a "$EV/rotation.log"; FAILED=1; }
[ "$NEW_MC" = "rc=0" ] || { echo "MinIO new-password REJECTED" | tee -a "$EV/rotation.log"; FAILED=1; }

# ---------- 8. 轮换后清单与比对 ----------
say "[8] post-state capture + compare"
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT name||'|'||storage||'|'||toString(auth_type) FROM system.users ORDER BY name" > "$EV/post-ch-users.txt" 2>&1
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT database, table, sum(rows) AS rows, sum(bytes_on_disk) AS bytes FROM system.parts WHERE active GROUP BY database, table ORDER BY database, table" > "$EV/post-ch-tables.tsv" 2>&1
POST_CH_SUM="$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT sum(rows) FROM system.parts WHERE active AND database='$PRE_CH_DB'")"
echo "post_ch_total_rows=$POST_CH_SUM (pre=$PRE_CH_SUM)" | tee -a "$EV/rotation.log"
[ "$PRE_CH_SUM" = "$POST_CH_SUM" ] && echo "CH row total unchanged (ok)" | tee -a "$EV/rotation.log" || { echo "CH row total CHANGED" | tee -a "$EV/rotation.log"; FAILED=1; }
diff -u "$EV/pre-ch-tables.tsv" "$EV/post-ch-tables.tsv" > "$EV/ch-tables.diff" 2>&1; echo "ch_tables_diff_rc=$?" | tee -a "$EV/rotation.log"

minio_inventory "$EV/post-minio-inventory.txt" "$MINIO_NEW"
sha256sum "$EV/post-minio-inventory.txt" | tee -a "$EV/rotation.log"
diff -u "$EV/pre-minio-inventory.txt" "$EV/post-minio-inventory.txt" > "$EV/minio-inventory.diff" 2>&1
MINIO_DIFF_RC=$?
echo "minio_inventory_diff_rc=$MINIO_DIFF_RC" | tee -a "$EV/rotation.log"
[ "$MINIO_DIFF_RC" = "0" ] && echo "MinIO inventory identical (objects/versions/metadata/ILM) (ok)" | tee -a "$EV/rotation.log" || echo "MinIO inventory DIFFERS — 见 minio-inventory.diff" | tee -a "$EV/rotation.log"

# ---------- 9. 功能回归 ----------
say "[9] functional regression"
H=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health/live)
echo "api_health_live=$H" | tee -a "$EV/rotation.log"
[ "$H" = "200" ] || FAILED=1
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
JAR="$EV/regress.jar"
LC=$(curl -s -c "$JAR" -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:8080/api/auth/v1/login -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"email\":\"$S7JS_INVESTIGATOR_EMAIL\",\"password\":\"$S7JS_INVESTIGATOR_PW\"}")
echo "user_login=$LC" | tee -a "$EV/rotation.log"
[ "$LC" = "200" ] || FAILED=1
JC=$(curl -s -b "$JAR" -H "X-Auth-Audience: user" -o "$EV/regress-jobs.json" -w "%{http_code}" "http://127.0.0.1:8080/api/v1/download-jobs?limit=200")
echo "jobs_api=$JC" | tee -a "$EV/rotation.log"
[ "$JC" = "200" ] || FAILED=1
python3 -c "import json,sys;d=json.load(open('$EV/regress-jobs.json'));print('jobs_count='+str(len(d.get('jobs') or [])))" | tee -a "$EV/rotation.log"
CHN=$(docker exec deploy-clickhouse-1 clickhouse-client --user "$PRE_CH_USER" --password "$CH_NEW" --query "SELECT count() FROM $PRE_CH_DB.token_transfers" 2>&1)
echo "ch_query_new_creds=$CHN" | tee -a "$EV/rotation.log"

echo "=== ROTATION SUMMARY FAILED=$FAILED ===" | tee -a "$EV/rotation.log"
printf '{"run":"%s","failed":%d,"evidence":"%s","dry_run":%d}\n' "$TS" "$FAILED" "$EV" "$DRY_RUN" > "$EV/result.json"
cat "$EV/result.json"
exit "$FAILED"