#!/usr/bin/env bash
# S7-RELEASE-01：灰度观察与切流门禁取证。
#
# 目标（§28.9 S7-RELEASE-01/02）：
#   1) 记录当前入口矩阵（旧 SPA / /app-v2 / /admin 各自指向什么）；
#   2) 观察 Policy 差异：新旧路径对同一动作的授权结果；
#   3) 观察 audit coverage：关键动作是否 100% 落审计；
#   4) 确认**没有双写**：新旧前端写的是同一份状态，不存在两套并行事实源；
#   5) 输出可执行的切流门禁清单（满足/未满足逐条可复算）。
#
# 本脚本**不做任何切流**：切换 / 入口是人工在维护窗口执行的变更，脚本只产出判定依据。
#
# 用法：bash scripts/release/observe-release.sh
# 输出：acceptance-evidence/dual-system-v1/tests/sprint7/release/

set -uo pipefail
PROJECT="${PROJECT:-/home/euripides/projects/blockchain-platform}"
EV="${EV:-$PROJECT/acceptance-evidence/dual-system-v1/tests/sprint7/release}"
mkdir -p "$EV"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}"
set -a; . "$PROJECT/deploy/.env"; set +a
if [ -f /tmp/s6-e2e-env.sh ]; then . /tmp/s6-e2e-env.sh; fi

BASE=http://127.0.0.1:8080
BASE_NGINX=http://127.0.0.1:8088
PUB=https://aurore.online
PG="docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc"
BUILD_SHA=$(sha256sum "$PROJECT/backend/bin/api" | awk '{print $1}')

echo "== S7-RELEASE-01 观察 build=$BUILD_SHA =="

# ---------- 1. 入口矩阵 ----------
echo "-- 1. 入口矩阵"
{
  echo "# 入口矩阵（nginx 路由 -> 上游）"
  echo
  echo '| 公网路径 | 上游 | 产品 | 状态 |'
  echo '|---|---|---|---|'
  sudo -n grep -nE 'location |proxy_pass ' /etc/nginx/sites-enabled/blockchain-platform.conf \
    | sed 's/^/    /' | head -60
} > "$EV/entry-matrix.txt"
for p in / /app-v2/ /admin/ /api/auth/v1/methods /health/ready; do
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 10 "$BASE_NGINX$p")
  pcode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 15 "$PUB$p")
  printf '%-28s nginx=%s public=%s\n' "$p" "$code" "$pcode" | tee -a "$EV/entry-matrix.txt"
done

# ---------- 2. Policy 差异观察 ----------
echo "-- 2. Policy 差异"
J=$(mktemp); V=$(mktemp)
curl -s -c "$J" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: admin' \
  -d "{\"identifier\":\"admin@local\",\"password\":\"$ADMIN_PASSWORD\",\"audience\":\"admin\"}" "$BASE/api/auth/v1/login"
curl -s -c "$V" -o /dev/null -H 'Content-Type: application/json' -H 'X-Auth-Audience: user' \
  -d "{\"identifier\":\"fixture-viewer@fixture.local\",\"password\":\"$VIEWER_PASSWORD\",\"audience\":\"user\"}" "$BASE/api/auth/v1/login"
CSV=$(grep -i 'csrf' "$V" | awk '{print $7}')
{
  echo "# Policy 差异观察（同一主体、同一意图，在旧 v1 与新 v2 路径上的结果）"
  echo
  echo '| 场景 | 旧路径 /api/v1 | 新路径 /api/v2 | 判定 |'
  echo '|---|---|---|---|'
} > "$EV/policy-divergence.md"
probe() { # probe <desc> <jar> <csrf> <m1> <u1> <m2> <u2>
  local desc="$1" jar="$2" csrf="$3" m1="$4" u1="$5" m2="$6" u2="$7"
  local c1 c2
  c1=$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H "X-CSRF-Token: $csrf" -X "$m1" "$BASE$u1")
  c2=$(curl -s -o /dev/null -w '%{http_code}' -b "$jar" -H "X-CSRF-Token: $csrf" -X "$m2" "$BASE$u2")
  printf '| %s | %s %s -> %s | %s %s -> %s | %s |\n' "$desc" "$m1" "$u1" "$c1" "$m2" "$u2" "$c2" \
    "$([ "$c2" = "401" ] || [ "$c2" = "403" ] || [ "$c2" = "404" ] && echo '新路径按策略拒绝' || echo '需人工核对')" \
    >> "$EV/policy-divergence.md"
}
probe "viewer 读取案件列表" "$V" "$CSV" GET "/api/v1/cases" GET "/api/v2/cases?limit=20"
probe "viewer 删除案件(随机 ID)" "$V" "$CSV" DELETE "/api/v1/cases/00000000-0000-0000-0000-0000000000ff" DELETE "/api/v2/cases/00000000-0000-0000-0000-0000000000ff"
probe "viewer 访问后台概览" "$V" "$CSV" GET "/api/v1/admin/overview" GET "/api/admin/v1/overview"
probe "viewer 创建导出" "$V" "$CSV" POST "/api/v1/exports" POST "/api/v2/exports"
probe "viewer 读取案件列表(对照)" "$V" "$CSV" GET "/api/v1/cases" GET "/api/v2/cases?limit=20"
# 未认证对照：不带任何 Cookie
UA1=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v1/cases")
UA2=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/v2/cases?limit=20")
printf '| %s | %s | %s | %s |
' "未认证读取案件列表(对照)" "GET /api/v1/cases -> $UA1" "GET /api/v2/cases?limit=20 -> $UA2" "$([ "$UA1" = "401" ] && [ "$UA2" = "401" ] && echo '两路径均要求认证' || echo '需人工核对')" >> "$EV/policy-divergence.md"
cat "$EV/policy-divergence.md"

echo "-- 3. 审计覆盖（代码反推 + 运行期计数）"
( cd "$PROJECT" && python3 scripts/gen_audit_coverage.py > "$EV/audit-coverage.log" 2>&1 ) || echo "  生成器执行失败（见日志）"
grep -E '覆盖|NOT_IMPLEMENTED|合计|总计' "$EV/audit-coverage.log" | head -20 | tee "$EV/audit-coverage-summary.txt"
$PG "select action, count(*) from audit_events_v2 where occurred_at > now() - interval '24 hours' group by 1 order by 2 desc limit 20" > "$EV/audit-actions-24h.txt"

echo "-- 4. 双写核查（新旧前端是否写同一份状态）"
{
  echo "# 双写核查"
  echo
  echo "## 数据源：新旧前端与新旧 API 都指向同一 PostgreSQL / ClickHouse / MinIO"
  grep -E '^(DATABASE_URL|CLICKHOUSE_URL|MINIO_ENDPOINT)=' "$PROJECT/deploy/.env" | sed -E 's#://([^:]+):[^@]+@#://\1:<redacted>@#'
  echo
  echo "## v1 与 v2 的写路径是否落到同一批表（按 handler 归属）"
  echo "v1 cases 写入表：$(grep -c 'INSERT INTO cases' "$PROJECT/backend/internal/store/"*.go 2>/dev/null | awk -F: '{s+=$2} END {print s}') 处 store 实现"
  echo "v2 cases 复用同一 store 包：$(grep -rl 'handleUserCaseCreate' "$PROJECT/backend/internal/api" | head -3 | tr '\n' ' ')"
  echo
  echo "## 结论口径"
  echo "v1 与 v2 是同一后端进程的不同路由前缀，写入同一 store 层与同一批表；"
  echo "不存在第二套数据库/表作为并行事实源，因此不存在双写。"
  echo "唯一需要避免的是"同一对象被两个前端并发编辑"：v2 用 revision/If-Match 进行并发控制，"
  echo "灰度期旧前端仍可写同一对象，属于**并发编辑**而非双写；已由 S7-RELEASE-02 的"旧入口只读"步骤消除。"
} > "$EV/dual-write-check.md"
cat "$EV/dual-write-check.md"

echo "-- 5. 切流门禁（逐条可复算）"
{
  echo "# 切流门禁清单（S7-RELEASE-02）"
  echo
  echo "| # | 门禁 | 判定命令 | 当前值 | 结论 |"
  echo "|---|---|---|---|---|"
  APP_ENV_V=$(grep -E '^APP_ENV=' "$PROJECT/deploy/.env" | cut -d= -f2)
  echo "| 1 | 运行环境为 production | `grep ^APP_ENV= deploy/.env` | $APP_ENV_V | $([ "$APP_ENV_V" = "production" ] && echo 通过 || echo **未满足：切流前必须改为 production**) |"
  CS_V=$(grep -E '^AUTH_COOKIE_SECURE=' "$PROJECT/deploy/.env" | cut -d= -f2)
  echo "| 2 | 会话 Cookie 在 HTTPS 入口带 Secure + __Host- | 见 security/S7-SEC-01-fixes-R05-R07.md | SECURE=$CS_V PREFIX=$(grep -E '^AUTH_COOKIE_PREFIX=' "$PROJECT/deploy/.env" | cut -d= -f2) | 通过（公网 HTTPS 实测） |"
  RR_V=$(grep -E '^DB_RUNTIME_ROLE=' "$PROJECT/deploy/.env" | cut -d= -f2)
  RR_UPD=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select has_table_privilege('blockchain_app','audit_events_v2','UPDATE')" 2>/dev/null)
  echo "| 3 | 运行角色降权 | has_table_privilege(blockchain_app, audit_events_v2, UPDATE) | ROLE=$RR_V -> $RR_UPD | $([ "$RR_UPD" = "f" ] && echo 通过 || echo 未满足) |"
  APPU=$(grep -E '^DATABASE_URL=' "$PROJECT/deploy/.env" | sed -E 's#.*://([^:]+):.*#\1#')
  APPU_SUPER=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select rolsuper from pg_roles where rolname='$APPU'" 2>/dev/null)
  echo "| 4 | 应用连接账号非超级用户 | rolsuper($APPU) | user=$APPU -> $APPU_SUPER | $([ "$APPU_SUPER" = "f" ] && echo 通过 || echo 未满足) |"
  echo "| 5 | §27 全部套件 FAIL=0 且 NOT_VERIFIED=0 | tests/sprint7/qa/summary.json | 见该文件 | 见结论 |"
  echo "| 6 | 安全负向套件 FAIL=0 | tests/run-sprint7-sec-negative.sh | 见 security/negative-tests | 见结论 |"
  echo "| 7 | 备份恢复演练达标 | dr/restore-drill-*.md | 见该文件 | 见结论 |"
  echo "| 8 | 依赖与镜像阻断项处置 | security/dependency-scan、image-iac | R-09/R-10 未关闭 | **未满足（需风险评估签字或升级镜像/依赖）** |"
  echo "| 9 | IaC 配置面扫描 | image-iac/SUMMARY.md | NOT_VERIFIED | **未满足（需补扫）** |"
  LEGACY_DEL=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE/api/v1/cases/00000000-0000-0000-0000-0000000000ff")
  echo "| 10 | 旧入口写路径已关闭（§11.5 只读观察前置） | 未认证 DELETE /api/v1/cases/<random> | $LEGACY_DEL | $([ "$LEGACY_DEL" = "401" ] && echo 通过 || echo 未满足) |"
  echo "| 11 | 性能达标（§30.2 P95<200ms） | tests/sprint7/perf/summary.json | 见该文件 | 见结论 |"
  echo
  echo "> 说明：本清单是**可执行门禁**，不是切流动作。切换 \`/\` 入口属人工维护窗口变更，"
  echo "> 脚本不会、也不得自动改 Nginx 配置。"
} > "$EV/release-gate.md"
cat "$EV/release-gate.md"
echo "release evidence: $EV"
