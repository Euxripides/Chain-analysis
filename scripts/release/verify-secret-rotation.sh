#!/usr/bin/env bash
# verify-secret-rotation.sh — 对**已完成**的轮换做独立后验（修正 rotate 脚本 v1 在 CH 验证段的引号缺陷）。
#
# 保留的失败事实：rotate-exposed-credentials.sh sha256 e36fbc88662e1186bab8e16906a4722218e0fe350ac33197a63f3c032426c1aa
# 在 [7] 段 CH 分支真实失败：chmod: cannot access .../.ch.netrc + line 190: $1: unbound variable → ROTATION_RC=1。
# 根因：NETRC 创建语句被写在函数外（顶层），其中 $1 未定义，set -u 触发退出。
# 轮换动作本身（.env 更新 / ALTER ROLE / 容器重建 / api+worker 重启）已在失败前完成；本脚本只做后验。
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:?$0}"
ENVF="$PROJECT/deploy/.env"
BAK="$EV/deploy.env.pre-rotation.bak"
umask 077
FAILED=0
say() { echo "$*" | tee -a "$EV/verify.log"; }
bad() { echo "FAIL $*" | tee -a "$EV/verify.log"; FAILED=1; }
sha12() { printf "%s" "$1" | sha256sum | cut -c1-12; }
valf() { awk -v k="$2" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$1"; }
NEW_PG_PW="$(valf "$ENVF" POSTGRES_PASSWORD)"
NEW_CH_PW="$(valf "$ENVF" CLICKHOUSE_PASSWORD)"
NEW_MC_PW="$(valf "$ENVF" MINIO_ROOT_PASSWORD)"
OLD_PG_PW="$(valf "$BAK" POSTGRES_PASSWORD)"
OLD_CH_PW="$(valf "$BAK" CLICKHOUSE_PASSWORD)"
OLD_MC_PW="$(valf "$BAK" MINIO_ROOT_PASSWORD)"
MC_USER="$(valf "$ENVF" MINIO_ROOT_USER)"
CH_USER="$(valf "$ENVF" CLICKHOUSE_USER)"
CH_DB="$(valf "$ENVF" CLICKHOUSE_DB)"
CH_HTTP="$(valf "$ENVF" CLICKHOUSE_HTTP_PORT)"
PG_USER="$(valf "$ENVF" POSTGRES_USER)"
PG_DB="$(valf "$ENVF" POSTGRES_DB)"
PG_PORT="$(valf "$ENVF" POSTGRES_PORT)"
say "=== verify-secret-rotation ==="
sha256sum "$0" | tee -a "$EV/verify.log"
for kk in NEW_PG_PW NEW_CH_PW NEW_MC_PW OLD_PG_PW OLD_CH_PW OLD_MC_PW; do
  v="${!kk}"
  echo "$kk len=${#v} sha12=$(sha12 "$v")" | tee -a "$EV/verify.log"
done
try_pg() { PGPASSWORD="$1" psql "host=127.0.0.1 port=$PG_PORT user=$PG_USER dbname=$PG_DB sslmode=disable connect_timeout=5" -tAc "select 1" >/dev/null 2>&1; }
if try_pg "$OLD_PG_PW"; then bad "PG old-password STILL ACCEPTED"; else say "PG old-password REJECTED (ok)"; fi
if try_pg "$NEW_PG_PW"; then say "PG new-password ACCEPTED (ok)"; else bad "PG new-password REJECTED"; fi
NETRC="$EV/.ch.netrc"
try_ch() { printf 'machine 127.0.0.1 login %s password %s\n' "$CH_USER" "$1" > "$NETRC"; chmod 600 "$NETRC"; curl -s --netrc-file "$NETRC" -o /dev/null -w "%{http_code}" "http://127.0.0.1:$CH_HTTP/?query=SELECT+1"; }
OLD_CH=$(try_ch "$OLD_CH_PW")
NEW_CH=$(try_ch "$NEW_CH_PW")
say "CH old_http=$OLD_CH new_http=$NEW_CH"
if [ "$OLD_CH" = "200" ]; then bad "CH old-password STILL ACCEPTED"; else say "CH old-password REJECTED (ok)"; fi
if [ "$NEW_CH" = "200" ]; then say "CH new-password ACCEPTED (ok)"; else bad "CH new-password REJECTED http=$NEW_CH"; fi
rm -f "$NETRC"
try_mc() { printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls local >/dev/null 2>&1; echo rc=$?\n' "$MC_USER" "$1" | docker exec -i deploy-minio-1 sh 2>/dev/null | tr -d '\r' | tail -1; }
OLD_MC=$(try_mc "$OLD_MC_PW")
NEW_MC=$(try_mc "$NEW_MC_PW")
say "MINIO old=$OLD_MC new=$NEW_MC"
if [ "$OLD_MC" = "rc=0" ]; then bad "MinIO old-password STILL ACCEPTED"; else say "MinIO old-password REJECTED (ok)"; fi
if [ "$NEW_MC" = "rc=0" ]; then say "MinIO new-password ACCEPTED (ok)"; else bad "MinIO new-password REJECTED"; fi
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT name||'|'||storage||'|'||toString(auth_type) FROM system.users ORDER BY name" > "$EV/post-ch-users.txt" 2>&1
docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT database, table, sum(rows) AS rows, sum(bytes_on_disk) AS bytes FROM system.parts WHERE active GROUP BY database, table ORDER BY database, table" > "$EV/post-ch-tables.tsv" 2>&1
POST_CH_SUM=$(docker exec deploy-clickhouse-1 clickhouse-client --query "SELECT sum(rows) FROM system.parts WHERE active AND database='$CH_DB'")
PRE_CH_SUM=$(cat "$EV/pre-ch-total-rows.txt" 2>/dev/null)
say "ch_total_rows pre=$PRE_CH_SUM post=$POST_CH_SUM"
if [ -n "$PRE_CH_SUM" ] && [ "$PRE_CH_SUM" = "$POST_CH_SUM" ]; then say "CH row total unchanged (ok)"; else bad "CH row total changed or pre missing"; fi
minio_inventory() {
  local out="$1" pw="$2"
  {
    echo "## buckets"; printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls local\n' "$MC_USER" "$pw" | docker exec -i deploy-minio-1 sh 2>&1
    for b in blockchain-audit blockchain-backups blockchain-exports blockchain-raw blockchain-reports blockchain-reports-drill-20260912t175029z; do
      echo "## bucket=$b objects"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls --recursive --json local/%s\n' "$MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1 | sed 's/"etag":"[^"]*"/"etag":"<e>"/g'
      echo "## bucket=$b versions"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ls --recursive --versions --json local/%s\n' "$MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1 | sed 's/"etag":"[^"]*"/"etag":"<e>"/g'
      echo "## bucket=$b ilm"
      printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\nmc ilm rule export local/%s\n' "$MC_USER" "$pw" "$b" | docker exec -i deploy-minio-1 sh 2>&1
    done
  } > "$out" 2>&1
}
minio_inventory "$EV/post-minio-inventory.txt" "$NEW_MC_PW"
diff -u "$EV/pre-minio-inventory.txt" "$EV/post-minio-inventory.txt" > "$EV/minio-inventory.diff" 2>&1
MD=$?
say "minio_inventory_diff_rc=$MD"
if [ "$MD" = "0" ]; then say "MinIO inventory identical (objects/versions/metadata/ILM) (ok)"; else bad "MinIO inventory DIFFERS"; head -c 1500 "$EV/minio-inventory.diff" | tee -a "$EV/verify.log"; fi
HC=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8080/health/live)
if [ "$HC" = "200" ]; then say "api_health_live=200 (ok)"; else bad "api_health_live=$HC"; fi
for u in blockchain-api blockchain-worker; do pid=$(systemctl --user show "$u.service" -p MainPID --value); say "$u MainPID=$pid exe_sha=$(sha256sum /proc/$pid/exe 2>/dev/null | cut -d' ' -f1)"; done
set -a; . /home/euripides/.s7jobscope-creds.env; set +a
JAR="$EV/regress.jar"
LC=$(curl -s -c "$JAR" -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:8080/api/auth/v1/login -H "Content-Type: application/json" -H "X-Auth-Audience: user" -d "{\"email\":\"$S7JS_INVESTIGATOR_EMAIL\",\"password\":\"$S7JS_INVESTIGATOR_PW\"}")
if [ "$LC" = "200" ]; then say "user_login=200 (ok)"; else bad "user_login=$LC"; fi
JC=$(curl -s -b "$JAR" -H "X-Auth-Audience: user" -o "$EV/regress-jobs.json" -w "%{http_code}" "http://127.0.0.1:8080/api/v1/download-jobs?limit=200")
if [ "$JC" = "200" ]; then say "jobs_api=200 (ok)"; else bad "jobs_api=$JC"; fi
python3 -c "import json;d=json.load(open('$EV/regress-jobs.json'));print('jobs_count='+str(len(d.get('jobs') or [])))" | tee -a "$EV/verify.log"
CHN=$(printf "SELECT count() FROM %s.token_transfers" "$CH_DB" | docker exec -i deploy-clickhouse-1 clickhouse-client --user "$CH_USER" --password "$NEW_CH_PW" 2>&1 | tail -1)
say "ch_query_new_creds=$CHN"
PGN=$(printf "select count(*) from download_jobs" | docker exec -i -e PGPASSWORD="$NEW_PG_PW" deploy-postgres-1 psql -U "$PG_USER" -d "$PG_DB" -tA 2>&1 | tail -1)
say "pg_query_new_creds=$PGN"
echo "=== VERIFY SUMMARY FAILED=$FAILED ===" | tee -a "$EV/verify.log"
printf '{"failed":%d,"evidence":"%s"}\n' "$FAILED" "$EV" > "$EV/verify-result.json"
cat "$EV/verify-result.json"
exit "$FAILED"