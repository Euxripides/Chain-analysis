#!/usr/bin/env bash
# 双系统验收测试环境引导（可重复执行，幂等）。
# 作用：重置夹具账号与 Owner 口令，写出 /tmp/s6-e2e-env.sh 与 /tmp/dual-system-fixture.env。
# 安全：口令一律现场随机生成，只写入 0600 的 /tmp 文件，不落仓库、不进证据。
set -euo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
export PATH="/usr/local/go/bin:/home/euripides/.nvm/versions/node/v24.20.0/bin:$PATH"
cd "$PROJECT"
set -a; . deploy/.env; set +a

echo "=== APP_ENV=$APP_ENV AUTH_MODE=$AUTH_MODE ==="
mkdir -p /tmp/e2e-bin

echo "=== 构建夹具工具（/tmp/e2e-bin，不覆盖部署二进制） ==="
(cd backend && go build -o /tmp/e2e-bin/admin-init ./cmd/admin-init && go build -o /tmp/e2e-bin/admin-fixture ./cmd/admin-fixture)

echo "=== 重置夹具账号 ==="
FIXOUT=$(/tmp/e2e-bin/admin-fixture -org acme -workspace acme-lab \
  -users viewer,analyst,auditor,investigator,reviewer,platform_admin 2>&1) || { echo "$FIXOUT"; exit 1; }
echo "$FIXOUT" | grep -vE '^\s+(viewer|analyst|auditor|investigator|reviewer|platform_admin)\s' || true

# 解析 "  role  email  password" 行
declare -A PW
while read -r role email password; do
  [ -n "${role:-}" ] || continue
  case "$role" in
    viewer|analyst|auditor|investigator|reviewer|platform_admin) PW[$role]="$password" ;;
  esac
done < <(echo "$FIXOUT" | awk '/^  [a-z_]+ +[a-z0-9._-]+@/{print $1, $2, $3}')

for r in viewer analyst auditor investigator reviewer platform_admin; do
  if [ -z "${PW[$r]:-}" ]; then echo "缺少 $r 的一次性口令"; exit 1; fi
done

echo "=== 重置 Owner（admin@local）口令 ==="
OWNER_PW="Owner-$(openssl rand -hex 12)"
printf '%s\n' "$OWNER_PW" | /tmp/e2e-bin/admin-init -email admin@local -stdin -force >/dev/null

echo "=== 写出 /tmp/s6-e2e-env.sh ==="
umask 077
cat > /tmp/s6-e2e-env.sh <<ENVEOF
# 双系统验收临时口令（由 tests/bootstrap-e2e-env.sh 生成，勿入库）
ADMIN_PASSWORD='$OWNER_PW'
VIEWER_PASSWORD='${PW[viewer]}'
ANALYST_PASSWORD='${PW[analyst]}'
AUDITOR_PASSWORD='${PW[auditor]}'
INVESTIGATOR_PASSWORD='${PW[investigator]}'
REVIEWER_PASSWORD='${PW[reviewer]}'
PLATFORM_ADMIN_PASSWORD='${PW[platform_admin]}'
ENVEOF
chmod 600 /tmp/s6-e2e-env.sh
echo "键: $(cut -d= -f1 /tmp/s6-e2e-env.sh | tr '\n' ' ')"
[ -f /tmp/dual-system-fixture.env ] && echo "夹具对象: /tmp/dual-system-fixture.env ($(wc -l < /tmp/dual-system-fixture.env) 行)" || echo "警告：夹具 env 未生成"
echo "=== 引导完成 ==="
