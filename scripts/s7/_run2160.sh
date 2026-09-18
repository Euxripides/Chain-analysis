cd /home/euripides/projects/blockchain-platform
echo "=== file content ==="; cat -A scripts/s7/_role.sql | head -12
set -a; . deploy/.env; set +a
echo "=== direct psql with error output ==="
docker exec -i -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -v ON_ERROR_STOP=1 -f - < scripts/s7/_role.sql
echo "rc=$?"
echo "=== alternative: -c with single DO ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -v ON_ERROR_STOP=1 -c "CREATE ROLE blockchain_api LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS NOREPLICATION" 2>&1 | head -3
echo "rc2=$?"
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select rolname from pg_roles where rolname='blockchain_api'"
echo SCRIPT_END
