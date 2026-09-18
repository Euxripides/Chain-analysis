cd /home/euripides/projects/blockchain-platform
echo "=== frozen build ==="
sha256sum backend/bin/api backend/bin/worker backend/bin/migrate 2>/dev/null
echo "=== services ==="
export XDG_RUNTIME_DIR=/run/user/$(id -u)
systemctl --user is-active blockchain-api blockchain-worker blockchain-containers-watchdog.timer blockchain-user-console blockchain-admin-console
curl -s http://127.0.0.1:8080/health/ready; echo
echo "=== migrations ==="
set -a; . deploy/.env; set +a
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from schema_migrations"
echo "=== security invariants ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select 'runtime_role_update='||has_table_privilege('blockchain_app','audit_events_v2','UPDATE')::text"
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select 'app_account_super='||rolsuper::text from pg_roles where rolname='blockchain_api'"
./backend/bin/auditverify 2>/dev/null | python3 -c "import sys,json;d=json.load(sys.stdin);print('audit_chain_ok=%s'%d['ok']);[print('   ',r['chain'],r['integrity_status'],'valid=%s'%r['valid']) for r in d['results']]"
echo SCRIPT_END