#!/usr/bin/env bash
# §16.2 发布候选冻结清单：把"当前部署版本"钉成可复算的哈希。
#
# 为什么需要它：验收标准 §16.2 要求"为当前部署版本重新冻结 build SHA、二进制和两套前端资源哈希"，
# 而 2026-09-13 的旧清单（section27-conformance.md 头部）引用的是已经不存在的构建。
# 本脚本每次运行都**重新测量**：进程实际在跑的二进制（/proc/<pid>/exe）、磁盘产物、
# 两套前端的 dist 与服务端实际下发的入口哈希、迁移头、配置键清单、健康与审计链。
#
# 只读：不重启服务、不改配置、不输出任何密钥值（配置只列键名）。
set -uo pipefail
R="${R:-/home/euripides/projects/blockchain-platform}"
TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${OUT:-$R/acceptance-evidence/dual-system-v1/release-$TS}"
mkdir -p "$OUT"; chmod 700 "$OUT"
LOG="$OUT/manifest.txt"
exec > "$LOG" 2>&1

echo "# §16.2 发布候选冻结清单"
echo "generated_at_utc=$TS"
echo "generated_at_local=$(date -Is)"
echo "host=$(hostname) kernel=$(uname -srm)"
echo

echo "## 1. 进程与二进制（部署实例实际在跑的）"
for svc in blockchain-api blockchain-worker blockchain-user-console blockchain-admin-console; do
  pid=$(systemctl --user show "$svc" -p MainPID --value)
  state=$(systemctl --user is-active "$svc")
  exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null || echo "-")
  sha="-"
  [ -r "/proc/$pid/exe" ] && sha=$(sha256sum "/proc/$pid/exe" 2>/dev/null | cut -d' ' -f1)
  printf '%s state=%s pid=%s exe=%s sha256=%s\n' "$svc" "$state" "$pid" "$exe" "$sha"
done
echo
echo "## 2. 磁盘产物哈希"
sha256sum "$R/backend/bin/api" "$R/backend/bin/worker" "$R/backend/bin/migrate" 2>/dev/null
echo
echo "## 3. 两套前端（磁盘 dist + 入口 chunk）"
for app in user-console admin-console; do
  d="$R/frontend/apps/$app/dist"
  echo "# $app"
  sha256sum "$d/index.html" 2>/dev/null
  entry=$(sed -n 's/.*src="\([^"]*assets\/[^"]*\)".*/\1/p' "$d/index.html" 2>/dev/null | head -1)
  [ -n "$entry" ] && sha256sum "$d${entry}" 2>/dev/null
done
echo
echo "## 4. 服务端实际下发的入口哈希（与磁盘比对）"
printf 'user_console_served_sha256=%s\n' "$(curl -sk https://127.0.0.1:8448/app-v2/ | sha256sum | cut -d' ' -f1)"
printf 'admin_console_served_sha256=%s\n' "$(curl -sk https://127.0.0.1:8448/admin/ | sha256sum | cut -d' ' -f1)"
echo
echo "## 5. 迁移头（清单查询自身用的是运维账号，不是运行期角色）"
docker exec -i deploy-postgres-1 psql -U blockchain -d blockchain_platform -q -tA -c "select 'migration_head='||max(name) from schema_migrations;" 2>/dev/null
echo "runtime_role_configured=$(grep -oP '^DB_RUNTIME_ROLE=\K.*' "$R/deploy/.env" 2>/dev/null)"
echo
echo "## 6. 运行配置键（只列键，不列值）"
grep -oE '^[A-Z0-9_]+=' "$R/deploy/.env" | tr -d '=' | sort | tr '\n' ' '
echo; echo
echo "## 7. 健康与审计链"
curl -s http://127.0.0.1:8080/health/ready; echo
set -a; . "$R/deploy/.env"; set +a
"$R/backend/bin/auditverify" 2>/dev/null | python3 -c "import sys,json;d=json.load(sys.stdin);print('auditverify_ok=',d['ok']);[print('  chain=',r['chain'],'valid=',r['valid'],'integrity=',r['integrity_status'],'head_hash=',r['head_hash']) for r in d['results']]"
echo
echo "MANIFEST_DONE dir=$OUT"
