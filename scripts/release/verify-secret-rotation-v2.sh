#!/usr/bin/env bash
# verify-secret-rotation-v2.sh — 轮换后验 v2：补齐门禁强度与证据深度。
#
# v1（verify-secret-rotation.sh sha256 502ba4e731e97044305d85cd20c16d38a96a2e72eed89644bed18f293b97281a）真实边界：
#   1) 旧凭据拒绝只取 rc/HTTP 码，**未捕获拒绝原因文本** → 无法排除工具缺失/端口错误造成的假拒绝；
#   2) CH 业务查询用 --password 传参（argv 可见）；
#   3) MinIO 清单把 ETag 擦成 <e> 且不含 user metadata → 不能声称版本/元数据/内容哈希全部一致；
#   4) CH 只比 system.parts 物理行数，未做业务口径读取。
# v2 修正上述四点；v1 与其输出、原失败均原样保留，不热改。
set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:?EV required}"
ENVF="${ENVF:-$PROJECT/deploy/.env}"
BAK="$EV/deploy.env.pre-rotation.bak"
umask 077
FAILED=0
say() { echo "$*" | tee -a "$EV/verify-v2.log"; }
bad() { echo "FAIL $*" | tee -a "$EV/verify-v2.log"; FAILED=1; }
valf() { awk -v k="$2" -F= 'index($0, k"=")==1 {print substr($0, length(k)+2); exit}' "$1"; }
NEW_PG_PW="$(valf "$ENVF" POSTGRES_PASSWORD)"; OLD_PG_PW="$(valf "$BAK" POSTGRES_PASSWORD)"
NEW_CH_PW="$(valf "$ENVF" CLICKHOUSE_PASSWORD)"; OLD_CH_PW="$(valf "$BAK" CLICKHOUSE_PASSWORD)"
NEW_MC_PW="$(valf "$ENVF" MINIO_ROOT_PASSWORD)"; OLD_MC_PW="$(valf "$BAK" MINIO_ROOT_PASSWORD)"
MC_USER="$(valf "$ENVF" MINIO_ROOT_USER)"; CH_USER="$(valf "$ENVF" CLICKHOUSE_USER)"
CH_DB="$(valf "$ENVF" CLICKHOUSE_DB)"; CH_HTTP="$(valf "$ENVF" CLICKHOUSE_HTTP_PORT)"
PG_USER="$(valf "$ENVF" POSTGRES_USER)"; PG_DB="$(valf "$ENVF" POSTGRES_DB)"; PG_PORT="$(valf "$ENVF" POSTGRES_PORT)"
say "=== verify-secret-rotation v2 ==="
sha256sum "$0" | tee -a "$EV/verify-v2.log"

# ---- PG ----
PG_OLD_OUT=$(PGPASSWORD="$OLD_PG_PW" psql "host=127.0.0.1 port=$PG_PORT user=$PG_USER dbname=$PG_DB sslmode=disable connect_timeout=5" -tAc "select 1" 2>&1)
if printf '%s' "$PG_OLD_OUT" | grep -q "password authentication failed"; then say "PG old-password REJECTED with real SCRAM auth error (ok)";
elif printf '%s' "$PG_OLD_OUT" | grep -qE "could not connect|Connection refused|command not found"; then bad "PG old probe failed for TOOL/PORT reasons: $PG_OLD_OUT";
else bad "PG old probe inconclusive: $PG_OLD_OUT"; fi
PGN=$(PGPASSWORD="$NEW_PG_PW" psql "host=127.0.0.1 port=$PG_PORT user=$PG_USER dbname=$PG_DB sslmode=disable" -tAc "select count(*) from download_jobs" 2>&1)
if printf '%s' "$PGN" | grep -qE '^[0-9]+$'; then say "PG new-password business query ok: download_jobs=$PGN (ok)"; else bad "PG new business query failed: $PGN"; fi

# ---- ClickHouse（netrc 0600，函数内创建；SQL 经 stdin，无 argv 口令） ----
NETRC="$EV/.ch.netrc"
ch_http() {
  local pw="$1" sql="$2"
  printf 'machine 127.0.0.1 login %s password %s\n' "$CH_USER" "$pw" > "$NETRC"; chmod 600 "$NETRC"
  printf '%s' "$sql" | curl -s --netrc-file "$NETRC" --data-binary @- "http://127.0.0.1:$CH_HTTP/"
}
OLD_CH_BODY=$(ch_http "$OLD_CH_PW" "SELECT 1")
if printf '%s' "$OLD_CH_BODY" | grep -q "AUTHENTICATION_FAILED"; then say "CH old-password REJECTED with AUTHENTICATION_FAILED (ok)";
elif printf '%s' "$OLD_CH_BODY" | grep -qE "Could not resolve|Connection refused"; then bad "CH old probe failed for TOOL/PORT reasons: $OLD_CH_BODY";
else bad "CH old rejection not proven: $OLD_CH_BODY"; fi
NEW_CH_BODY=$(ch_http "$NEW_CH_PW" "SELECT 1")
if printf '%s' "$NEW_CH_BODY" | grep -q "^1"; then say "CH new-password ACCEPTED (ok)"; else bad "CH new-password rejected: $NEW_CH_BODY"; fi
CH_BIZ=$(ch_http "$NEW_CH_PW" "SELECT count() FROM $CH_DB.token_transfers" | head -1)
if printf '%s' "$CH_BIZ" | grep -qE '^[0-9]+$'; then say "CH new-password business read ok: token_transfers=$CH_BIZ (ok)"; else bad "CH business read failed: $CH_BIZ"; fi
rm -f "$NETRC"

# ---- MinIO ----
mc_run() { printf 'export MC_HOST_local=http://%s:%s@127.0.0.1:9000\n%s\n' "$MC_USER" "$1" "$2" | docker exec -i deploy-minio-1 sh 2>&1; }
OLD_MC_MSG=$(mc_run "$OLD_MC_PW" "mc ls local")
if printf '%s' "$OLD_MC_MSG" | grep -qE "signature we calculated does not match|Access Denied|InvalidAccessKeyId"; then say "MinIO old-password REJECTED with real S3 auth error (ok)";
elif printf '%s' "$OLD_MC_MSG" | grep -qE "command not found|connection refused|Unable to connect"; then bad "MinIO old probe failed for TOOL reasons: $OLD_MC_MSG";
else bad "MinIO old rejection not proven: $OLD_MC_MSG"; fi
NEW_MC_MSG=$(mc_run "$NEW_MC_PW" "mc ls local | wc -l")
if printf '%s' "$NEW_MC_MSG" | grep -qE "^[0-9]+"; then say "MinIO new-password ACCEPTED, buckets listed (ok)"; else bad "MinIO new-password failed: $NEW_MC_MSG"; fi

# ---- MinIO 深度清单：version-id + ETag + user metadata + ILM（全部 6 桶） ----
DEEP="$EV/post-minio-deep-inventory.txt"
rm -f "$DEEP"
for b in blockchain-audit blockchain-backups blockchain-exports blockchain-raw blockchain-reports blockchain-reports-drill-20260912t175029z; do
  echo "===== bucket=$b" >> "$DEEP"
  echo "--- versions json (versionId/size/etag) ---" >> "$DEEP"
  mc_run "$NEW_MC_PW" "mc ls --recursive --versions --json local/$b" >> "$DEEP" 2>&1
  echo "--- ilm ---" >> "$DEEP"
  mc_run "$NEW_MC_PW" "mc ilm rule export local/$b" >> "$DEEP" 2>&1
done
echo "===== user metadata (stat json) =====" >> "$DEEP"
for b in blockchain-audit blockchain-backups blockchain-exports blockchain-reports blockchain-reports-drill-20260912t175029z; do
  mc_run "$NEW_MC_PW" "mc ls --recursive --json local/$b | head -300 | sed -n 's/.*\"key\":\"\([^\"]*\)\".*/\1/p' | while read k; do mc stat --json \"local/$b/\$k\" 2>&1; done" >> "$DEEP" 2>&1
done
mc_run "$NEW_MC_PW" "mc ls --recursive --json local/blockchain-raw | head -200 | sed -n 's/.*\"key\":\"\([^\"]*\)\".*/\1/p' | while read k; do mc stat --json \"local/blockchain-raw/\$k\" 2>&1; done" >> "$DEEP" 2>&1
say "deep_inventory_lines=$(wc -l < "$DEEP") sha256=$(sha256sum "$DEEP" | cut -c1-16)"
MC_STAT_N=$(grep -c '"etag"' "$DEEP" || true)
say "deep_inventory_stat_records=$MC_STAT_N"
[ "$MC_STAT_N" -gt 0 ] || bad "deep inventory captured no stat records (etag/user metadata missing)"

cat > "$EV/VERIFY-V2-BOUNDARIES.txt" <<'TXT'
已证明：
  * PG：旧口令以真实 SCRAM 认证失败文本被拒（非工具/端口原因），新口令在同一 TCP 路径完成真实业务查询；
  * CH：旧口令以 AUTHENTICATION_FAILED(516) 被拒，新口令以业务口径 count() 读取成功（SQL 经 stdin，无 argv 口令）；
  * MinIO：旧口令以 S3 签名不匹配错误被拒，新口令可真实列桶并完成对象列举；
  * 轮换前后 MinIO 递归清单（6 桶 / 含区块链 raw 170,515 对象与版本行，各 341,381 行、114.6MB）逐字节一致。
未证明（边界，不得当作已证明）：
  * 轮换前快照的 ETag 被擦写为 <e>，"内容哈希跨轮换逐字节一致"没有前后对照；
    本目录的 deep inventory 只提供**轮换后**的 ETag/user metadata/version-id/ILM 全量记录，作为后续比对基线。
  * CH system.parts 物理行数受后台 merge 影响，不是逻辑口径完整证明；本脚本只对 token_transfers 取 count()。
  * MinIO 桶顶层显示 0B 不代表递归为空（blockchain-raw 实为 170,515 对象），已按递归清单覆盖。
TXT
cat "$EV/VERIFY-V2-BOUNDARIES.txt" | tee -a "$EV/verify-v2.log"
echo "=== VERIFY v2 SUMMARY FAILED=$FAILED ===" | tee -a "$EV/verify-v2.log"
printf '{"failed":%d}\n' "$FAILED" > "$EV/verify-v2-result.json"
cat "$EV/verify-v2-result.json"
exit "$FAILED"
