set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
echo "=== apply migrations to production ==="
./backend/bin/migrate 2>&1 | tail -4
echo "=== verify ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from schema_migrations"
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select name from schema_migrations order by name desc limit 2"
echo "=== scope audit after ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select table_name, rows, org_null, ws_null, ws_without_org from s7_scope_audit() where org_null>0 or ws_without_org>0 order by table_name"
echo "=== partitions ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select * from s7_partition_status()"
echo "=== new indexes present ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from pg_indexes where schemaname='public'"
echo "=== feature_flags scope_user_id ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select column_name from information_schema.columns where table_name='feature_flags' and column_name like 'scope%'"