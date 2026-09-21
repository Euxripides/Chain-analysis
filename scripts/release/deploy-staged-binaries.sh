#!/usr/bin/env bash
# 受控二进制部署（API/Worker）：备份 -> 校验暂存产物 -> 原子替换 -> 重启 -> 实测。
# 不修改 .env、不做迁移；迁移另行执行并留档。
set -euo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
STAGING="${STAGING:-/tmp/staging}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_DIR="$PROJECT/backend/bin/deploy-backups-$TS"
mkdir -p "$BACKUP_DIR"

for b in api worker; do
  [ -x "$STAGING/$b" ] || { echo "缺少暂存产物 $STAGING/$b"; exit 2; }
done
echo "== 暂存产物 SHA-256 =="
sha256sum "$STAGING/api" "$STAGING/worker"

echo "== 备份当前二进制 -> $BACKUP_DIR =="
cp -p "$PROJECT/backend/bin/api" "$BACKUP_DIR/api"
cp -p "$PROJECT/backend/bin/worker" "$BACKUP_DIR/worker"
sha256sum "$BACKUP_DIR/api" "$BACKUP_DIR/worker"

echo "== 原子替换 =="
install -m 0755 "$STAGING/api" "$PROJECT/backend/bin/api.new"
install -m 0755 "$STAGING/worker" "$PROJECT/backend/bin/worker.new"
mv -f "$PROJECT/backend/bin/api.new" "$PROJECT/backend/bin/api"
mv -f "$PROJECT/backend/bin/worker.new" "$PROJECT/backend/bin/worker"

echo "== 重启服务 =="
systemctl --user restart blockchain-api.service
systemctl --user restart blockchain-worker.service
sleep 3
systemctl --user is-active blockchain-api.service blockchain-worker.service

echo "== 就绪与版本实测 =="
curl -fsS -m 10 http://127.0.0.1:8080/health/ready; echo
sha256sum "$PROJECT/backend/bin/api" "$PROJECT/backend/bin/worker"
echo "DEPLOY_DONE ts=$TS backup=$BACKUP_DIR"
