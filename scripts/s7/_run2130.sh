cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
PG="docker exec -i -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB"
echo "=== 1) 创建角色 ==="
cat scripts/s7/_role.sql | $PG -v ON_ERROR_STOP=1
echo "rc=$?"
APPPW=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
$PG -v ON_ERROR_STOP=1 -c "ALTER ROLE blockchain_api PASSWORD '$APPPW'" -c "GRANT blockchain_app TO blockchain_api"
echo "grant rc=$?"
$PG -c "select rolname, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolinherit, rolcanlogin from pg_roles where rolname in ('blockchain','blockchain_app','blockchain_api')"
echo "=== 2) 验证新账号可连接并 SET ROLE ==="
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "select current_user, session_user" 2>&1 | head -3
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "SET ROLE blockchain_app; select current_user; select has_table_privilege('audit_events_v2','UPDATE')" 2>&1 | head -5
echo "=== 3) RESET ROLE 后是否还有权限（关键：不能回到超级用户） ==="
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "select rolsuper from pg_roles where rolname=current_user; select has_table_privilege('audit_events_v2','SELECT')" 2>&1 | head -5
echo "APPPW_SAVED_TO=/tmp/s7-app-role.pw"
umask 077; printf '%s' "$APPPW" > /tmp/s7-app-role.pw
echo SCRIPT_END
