cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
PGB="docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB"
APPPW=$(python3 -c "import secrets;print(secrets.token_urlsafe(32))")
$PGB -v ON_ERROR_STOP=1 -c "ALTER ROLE blockchain_api PASSWORD '$APPPW'" -c "GRANT blockchain_app TO blockchain_api"
echo "grant rc=$?"
$PGB -c "select rolname, rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolinherit, rolcanlogin from pg_roles where rolname in ('blockchain','blockchain_app','blockchain_api')"
echo "=== 新账号直连 + SET ROLE ==="
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -Atc "select current_user||' / '||session_user;" 2>&1 | head -3
echo "-- SET ROLE 后 --"
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -v ON_ERROR_STOP=0 -c "SET ROLE blockchain_app" -c "select current_user" -c "select has_table_privilege('audit_events_v2','UPDATE') as can_update, has_table_privilege('audit_events_v2','SELECT') as can_select" 2>&1 | head -12
echo "-- RESET ROLE 之后（期望 rolsuper=f 且无审计表读权限） --"
PGPASSWORD=$APPPW psql -h 127.0.0.1 -p $POSTGRES_PORT -U blockchain_api -d $POSTGRES_DB -c "select rolsuper as is_super from pg_roles where rolname=current_user" -c "select has_table_privilege('audit_events_v2','SELECT') as can_select" 2>&1 | head -12
umask 077; printf '%s' "$APPPW" > /tmp/s7-app-role.pw; chmod 600 /tmp/s7-app-role.pw
echo "saved"
echo SCRIPT_END
