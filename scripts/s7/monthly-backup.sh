#!/usr/bin/env bash
# 月度备份（用户决定：一个月一次）。
#
# 内容：PostgreSQL 全量（owner 角色，应用角色没有 dump 权限）+ ClickHouse 全库
#       + MinIO 对象（增量 rsync）+ 配置/二进制 + 清单与校验。
# 去向：本机 /data/blockchain-platform/backup/<YYYY-MM>/ ，随后同步到宿主机共享
#       //192.168.31.224/data_bak/monthly/<YYYY-MM>/（凭据在 ~/.smb224.auth，0600）。
# 保留：本机与宿主机各保留最近 KEEP 个月（默认 3），删除动作逐条写进日志。
#
# 幂等：同一个月重复执行会覆盖当月目录（先写 .tmp 再改名，避免半成品被当成完整备份）。
set -uo pipefail

REPO=/home/euripides/projects/blockchain-platform
ROOT=/data/blockchain-platform/backup
MONTH=$(date -u +%Y-%m)
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
SET="$ROOT/$MONTH"
TMP="$SET.tmp"
KEEP="${KEEP:-3}"
SMB_AUTH=/home/euripides/.smb224.auth
SMB_MOUNT=/mnt/host224
SMB_HOST=192.168.31.224
SMB_SHARE=data_bak
LOG="$ROOT/backup-$STAMP.log"
STATUS="$ROOT/backup-$STAMP.status"

mkdir -p "$ROOT"
exec > >(tee -a "$LOG") 2>&1
echo "=== monthly backup start $STAMP month=$MONTH ==="

fail() { echo "FATAL: $1"; echo "FAILED $1" > "$STATUS"; rm -rf "$TMP"; exit 1; }
step() { echo; echo "--- $1"; }

rm -rf "$TMP"; mkdir -p "$TMP"

step "1/6 PostgreSQL 全量"
python3 "$REPO/scripts/s7/mk-pg-env.py" "$REPO/deploy/.env" "$TMP/.pg-env" >/dev/null || fail "pg env"
chmod 600 "$TMP/.pg-env"
# shellcheck disable=SC1091
set -a; . "$TMP/.pg-env"; set +a
docker exec -e PGPASSWORD="$PGPASSWORD" deploy-postgres-1 \
  pg_dump -U "$PGUSER" -d "$PGDATABASE" -Fc > "$TMP/postgres.dump" || fail "pg_dump"
rm -f "$TMP/.pg-env"; unset PGPASSWORD
docker exec -i deploy-postgres-1 pg_restore -l < "$TMP/postgres.dump" > "$TMP/postgres.toc" || fail "pg_restore -l"
TABLES=$(grep -c 'TABLE DATA' "$TMP/postgres.toc")
[ "$TABLES" -gt 0 ] || fail "dump 里没有 TABLE DATA"
echo "postgres.dump bytes=$(stat -c%s "$TMP/postgres.dump") tables=$TABLES"

step "2/6 ClickHouse 全库"
CHNAME="monthly-$STAMP"
docker exec deploy-clickhouse-1 clickhouse-client \
  --query "BACKUP DATABASE blockchain TO File('/var/lib/clickhouse/backups/$CHNAME')" || fail "clickhouse backup"
mkdir -p "$TMP/clickhouse"
sudo mv "/data/blockchain-platform/clickhouse/backups/$CHNAME" "$TMP/clickhouse/" || fail "move ch backup"
sudo chown -R euripides:euripides "$TMP/clickhouse"
du -sh "$TMP/clickhouse"

step "3/6 MinIO 对象（增量）"
for bucket in blockchain-exports blockchain-audit blockchain-reports blockchain-backups; do
  src="/data/blockchain-platform/minio/$bucket"
  [ -d "$src" ] || { echo "skip $bucket (不存在)"; continue; }
  mkdir -p "$TMP/minio/$bucket"
  rsync -a "$src/" "$TMP/minio/$bucket/" || fail "rsync $bucket"
  echo "$bucket files=$(find "$TMP/minio/$bucket" -type f | wc -l)"
done

step "4/6 配置与二进制"
mkdir -p "$TMP/configs" "$TMP/bin"
cp "$REPO/deploy/.env" "$TMP/configs/deploy.env" 2>/dev/null
cp "$REPO/deploy/legacy-console.env" "$TMP/configs/legacy-console.env" 2>/dev/null
sudo cp /etc/nginx/sites-enabled/blockchain-platform.conf "$TMP/configs/nginx-blockchain-platform.conf" 2>/dev/null
sudo cp /etc/nginx/conf.d/legacy-console.conf "$TMP/configs/nginx-legacy-console.conf" 2>/dev/null
cp /home/euripides/.config/systemd/user/blockchain-*.service "$TMP/configs/" 2>/dev/null
cp "$REPO/frontend/vite.config.ts" "$TMP/configs/vite.config.ts" 2>/dev/null
# sudo cp 保留源文件属主（nginx 配置是 root:600），会让清单算不出哈希 —— 统一改回本用户。
sudo chown "$(id -u):$(id -g)" "$TMP/configs"/*.conf 2>/dev/null || true
cp "$REPO/backend/bin/api" "$REPO/backend/bin/worker" "$REPO/backend/bin/migrate" "$TMP/bin/" 2>/dev/null
(cd "$TMP" && find . -type f -size -50M -print0 | sort -z | xargs -0 sha256sum > MANIFEST.sha256)
echo "manifest lines=$(wc -l < "$TMP/MANIFEST.sha256")"

step "5/6 发布当月目录"
rm -rf "$SET"
mv "$TMP" "$SET" || fail "发布目录"
echo "set=$SET size=$(du -sh "$SET" | cut -f1)"

step "6/6 同步到宿主机 $SMB_HOST:$SMB_SHARE"
HOST_SYNC=skipped
if mountpoint -q "$SMB_MOUNT"; then
  ok=1
else
  sudo mkdir -p "$SMB_MOUNT"
  sudo mount -t cifs "//$SMB_HOST/$SMB_SHARE" "$SMB_MOUNT" \
    -o "credentials=$SMB_AUTH,uid=$(id -u),gid=$(id -g),vers=3.0,iocharset=utf8" || ok=0
fi
if [ "${ok:-1}" = "1" ]; then
  sudo mkdir -p "$SMB_MOUNT/monthly/$MONTH"
  sudo chown "$(id -u):$(id -g)" "$SMB_MOUNT/monthly/$MONTH"
  if rsync -a "$SET/" "$SMB_MOUNT/monthly/$MONTH/"; then
    HOST_SYNC=ok
    cp "$SET/MANIFEST.sha256" "$SMB_MOUNT/monthly/$MONTH/" 2>/dev/null
  else
    HOST_SYNC=failed
  fi
fi
echo "host_sync=$HOST_SYNC"

step "保留策略：各留最近 $KEEP 个月"
for dir in "$ROOT"/*; do
  [ -d "$dir" ] || continue
  case "$dir" in *.tmp) continue;; esac
done
ls -1d "$ROOT"/[0-9][0-9][0-9][0-9]-[0-9][0-9] 2>/dev/null | sort | head -n "-$KEEP" | while read -r old; do
  echo "retention: 删除本机旧备份 $old"; rm -rf "$old"
done
if mountpoint -q "$SMB_MOUNT" && [ -d "$SMB_MOUNT/monthly" ]; then
  ls -1d "$SMB_MOUNT/monthly"/[0-9][0-9][0-9][0-9]-[0-9][0-9] 2>/dev/null | sort | head -n "-$KEEP" | while read -r old; do
    echo "retention: 删除宿主机旧备份 $old"; rm -rf "$old"
  done
fi

echo "=== done month=$MONTH set=$SET host_sync=$HOST_SYNC ==="
echo "OK $MONTH $SET host_sync=$HOST_SYNC" > "$STATUS"
