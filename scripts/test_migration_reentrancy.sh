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
# 2026-09-16 真实事故（必须写下来）：本脚本此前只导出 DATABASE_URL，而 cmd/migrate 读的是
# **MIGRATION_DATABASE_URL**（deploy/.env）。于是：① 一次性库从未被迁移，全部断言都打在
# 不存在的库上（psql 报 relation "schema_migrations" does not exist，两个 check 因为"空 == 空"
# 假 PASS）；② 注入的失败迁移 9999_injected_failure.sql 实际是拿**生产库**跑的。
# 生产库事后核对无损（head 仍为 0065、无 9999 记录、无 injected_probe 残留、服务健康），
# 但"绝不触碰生产库"这句注释当时是假的。现在：两个 DSN 都指向一次性库，并且加一道硬断言
# ——运行前后生产库的迁移头与行数必须完全不变，否则脚本以非 0 退出。
export DATABASE_URL="$BASE/$TESTDB?sslmode=disable"
export MIGRATION_DATABASE_URL="$BASE/$TESTDB?sslmode=disable"
export PATH=/usr/local/go/bin:$PATH

# 生产库基线：脚本自己证明"没有碰生产"。
PROD_HEAD_BEFORE=$(psql "$BASE/blockchain_platform" -tAc "select coalesce(max(name),'') from schema_migrations" 2>/dev/null | tr -d '[:space:]')
PROD_ROWS_BEFORE=$(psql "$BASE/blockchain_platform" -tAc "select count(*) from schema_migrations" 2>/dev/null | tr -d '[:space:]')
echo "  生产库基线：head=$PROD_HEAD_BEFORE rows=$PROD_ROWS_BEFORE"

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
# 注意：sessions_device_id_fkey 是**约束**不是表，原来这一句从来就是 no-op（注释想"制造需要重跑的
# 状态"）。留着只会误导读者，删掉；第 4 步的 FK 断言核对的是 0046 迁移本身的产物。
cd backend
RECOVER=$(go run ./cmd/migrate 2>&1)
cd ..
check "DATA-010" "清理后可重新迁移成功" "$(echo "$RECOVER" | grep -c 'migrate done')" "1"
APPLIED=$(psql "$BASE/$TESTDB" -tAc "select count(*) from schema_migrations where name='postgres/0046_session_device_fk.sql'")
check "DATA-010" "该迁移最终被正确记录" "$APPLIED" "1"
FK=$(psql "$BASE/$TESTDB" -tAc "select count(*) from pg_constraint where conname='sessions_device_id_fkey' and confdeltype='n'")
check "DATA-010" "外键重建为 ON DELETE SET NULL" "$FK" "1"

echo "== 5. 生产库未被触碰（本脚本的自我约束） =="
PROD_HEAD_AFTER=$(psql "$BASE/blockchain_platform" -tAc "select coalesce(max(name),'') from schema_migrations" 2>/dev/null | tr -d '[:space:]')
PROD_ROWS_AFTER=$(psql "$BASE/blockchain_platform" -tAc "select count(*) from schema_migrations" 2>/dev/null | tr -d '[:space:]')
INJ_IN_PROD=$(psql "$BASE/blockchain_platform" -tAc "select count(*) from schema_migrations where name like '%9999_injected%'" 2>/dev/null | tr -d '[:space:]')
check "DATA-010" "生产库迁移头未被改变" "$PROD_HEAD_AFTER" "$PROD_HEAD_BEFORE"
check "DATA-010" "生产库迁移行数未被改变" "$PROD_ROWS_AFTER" "$PROD_ROWS_BEFORE"
check "DATA-010" "生产库没有注入迁移的记录" "$INJ_IN_PROD" "0"

echo "== 收尾：删除一次性数据库 =="
psql "$BASE/postgres" -q -c "DROP DATABASE IF EXISTS $TESTDB"
echo
printf 'PASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
