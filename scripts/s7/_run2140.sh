cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
PG="docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB"
echo "=== 1) 创建角色（不带 -i，避免抢占脚本 stdin） ==="
$PG -v ON_ERROR_STOP=1 -f /dev/stdin < scripts/s7/_role.sql
echo "create rc=$?"
APPPW=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
$PG -v ON_ERROR_STOP=1 -c "ALTER ROLE blockchain_api PASSWORD '$APPPW'" -c "GRANT blockchain_app TO blockchain_api"
echo "grant rc=$?"
$PG -c "select rolname, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolinherit, rolcanlogin from pg_roles where rolname in ('blockchain','blockchain_app','blockchain_api')"
echo "=== 2) 新账号连接 + SET ROLE ==="
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "select current_user, session_user" 2>&1 | head -3
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "SET ROLE blockchain_app; select current_user; select has_table_privilege('audit_events_v2','UPDATE');" 2>&1 | head -5
echo "=== 3) RESET ROLE 后是否仍能读审计表（期望 f，即降级为无权限角色） ==="
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "select rolsuper from pg_roles where rolname=current_user; select has_table_privilege('audit_events_v2','SELECT');" 2>&1 | head -5
umask 077; printf '%s' "$APPPW" > /tmp/s7-app-role.pw
echo "saved=/tmp/s7-app-role.pw"
echo SCRIPT_END
