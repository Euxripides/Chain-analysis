#!/usr/bin/env bash
# DATA-010：Schema migration 中断后可重入，且不破坏旧服务读取。
#
# 做法（全部在一次性数据库上进行，绝不触碰生产库）：
#   1. 建空库，跑完整迁移 -> 应成功；
#   2. 再跑一次 -> 应幂等（全部跳过）；
#   3. 人为让一条迁移失败（在事务内报错），验证该迁移整体回滚、
#      schema_migrations 不出现半成品记录，且再次运行仍能成功；
#   4. 验证旧服务仍能读取：对"迁移前就存在的表"做一次 SELECT。
#
# 用法（在 VM 上执行）：bash scripts/test_migration_reentrancy.sh

set -uo pipefail
cd "$(dirname "$0")/.."
set -a; . deploy/.env; set +a
REPO="$(pwd)"

BASE="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT}"
TESTDB="blockchain_platform_migtest"
PASS=0; FAIL=0
check() {
  if [ "$3" = "$4" ]; then printf '  PASS  %-52s %s\n' "$1" "$2"; PASS=$((PASS+1));
  else printf '  FAIL  %-52s got=%s want=%s\n' "$1" "$2" "$3" "$4"; FAIL=$((FAIL+1)); fi
}

echo "== 准备一次性数据库 $TESTDB =="
psql "$BASE/postgres" -q -c "DROP DATABASE IF EXISTS $TESTDB" -c "CREATE DATABASE $TESTDB"
export DATABASE_URL="$BASE/$TESTDB?sslmode=disable"
export PATH=/usr/local/go/bin:$PATH

echo "== 1. 首次全量迁移 =="
cd backend
OUT=$(go run ./cmd/migrate 2>&1)
cd ..
check "DATA-010" "首次迁移成功" "$(echo "$OUT" | grep -c 'migrate done')" "1"
FIRST_COUNT=$(psql "$BASE/$TESTDB" -tAc "select count(*) from schema_migrations")

echo "== 2. 重复执行应幂等 =="
cd backend
OUT2=$(go run ./cmd/migrate 2>&1)
cd ..
check "DATA-010" "重复执行成功" "$(echo "$OUT2" | grep -c 'migrate done')" "1"
SECOND_COUNT=$(psql "$BASE/$TESTDB" -tAc "select count(*) from schema_migrations")
check "DATA-010" "已应用迁移数不变（无重复记录）" "$SECOND_COUNT" "$FIRST_COUNT"

echo "== 3. 注入真实失败：验证单文件事务回滚 =="
# 关键：注入物必须真的让迁移报错。第一次尝试用"同名表"制造冲突是无效的
# —— PostgreSQL 里约束与表不在同一命名空间，迁移照样成功，测出来的是假阴性。
# 这里插入一个临时迁移文件，其最后一条语句必然抛错，从而验证：
#   1. 该文件整体回滚（前面的 DDL 不残留）；
#   2. schema_migrations 不写入半成品记录；
#   3. 旧表仍可查询。
INJ="$REPO/backend/migrations/postgres/9999_injected_failure.sql"
cat > "$INJ" <<'SQL'
CREATE TABLE IF NOT EXISTS injected_probe (id int);
SELECT 1 / 0;
SQL
cd backend
go run ./cmd/migrate >/dev/null 2>&1
INJ_EXIT=$?
cd ..
if [ "$INJ_EXIT" -ne 0 ]; then
  printf '  PASS  %-52s %s
' "DATA-010" "注入的迁移确实失败了（exit=$INJ_EXIT）"; PASS=$((PASS+1))
else
  printf '  FAIL  %-52s got=成功 want=失败
' "DATA-010" "注入的迁移应当失败"; FAIL=$((FAIL+1))
fi
HALF="$(psql "$BASE/$TESTDB" -tAc "select count(*) from schema_migrations where name='postgres/9999_injected_failure.sql'" | tr -d '[:space:]')"
if [ "$HALF" = "0" ]; then
  printf '  PASS  %-52s %s
' "DATA-010" "中断的迁移未写入 schema_migrations"; PASS=$((PASS+1))
else
  printf '  FAIL  %-52s got=%s want=0
' "DATA-010" "中断的迁移留下了半成品记录"; FAIL=$((FAIL+1))
fi
RESIDUE="$(psql "$BASE/$TESTDB" -tAc "select count(*) from information_schema.tables where table_name='injected_probe'" | tr -d '[:space:]')"
if [ "$RESIDUE" = "0" ]; then
  printf '  PASS  %-52s %s
' "DATA-010" "失败迁移内的 DDL 已整体回滚（无残留表）"; PASS=$((PASS+1))
else
  printf '  FAIL  %-52s got=%s want=0
' "DATA-010" "失败迁移残留了对象（injected_probe）"; FAIL=$((FAIL+1))
fi
LEGACY_READ="$(psql "$BASE/$TESTDB" -tAc "select count(*) >= 0 from users" 2>&1 | tr -d '[:space:]')"
check "DATA-010" "迁移失败后旧表仍可读（users）" "$LEGACY_READ" "t"
rm -f "$INJ"

echo "== 4. 清理注入物后应可重入 =="
psql "$BASE/$TESTDB" -q -c "DROP TABLE IF EXISTS sessions_device_id_fkey"
cd backend
RECOVER=$(go run ./cmd/migrate 2>&1)
cd ..
check "DATA-010" "清理后可重新迁移成功" "$(echo "$RECOVER" | grep -c 'migrate done')" "1"
APPLIED=$(psql "$BASE/$TESTDB" -tAc "select count(*) from schema_migrations where name='postgres/0046_session_device_fk.sql'")
check "DATA-010" "该迁移最终被正确记录" "$APPLIED" "1"
FK=$(psql "$BASE/$TESTDB" -tAc "select count(*) from pg_constraint where conname='sessions_device_id_fkey' and confdeltype='n'")
check "DATA-010" "外键重建为 ON DELETE SET NULL" "$FK" "1"

echo "== 收尾：删除一次性数据库 =="
psql "$BASE/postgres" -q -c "DROP DATABASE IF EXISTS $TESTDB"
echo
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
