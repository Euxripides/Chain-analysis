set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
SCRATCH=blockchain_platform_s7_migtest
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d postgres -q -c "DROP DATABASE IF EXISTS $SCRATCH" -c "CREATE DATABASE $SCRATCH OWNER $POSTGRES_USER"
export PATH=/usr/local/go/bin:$PATH
go build -o backend/bin/migrate ./backend/cmd/migrate 2>&1 | head -5 || (cd backend && go build -o bin/migrate ./cmd/migrate)
export DATABASE_URL="postgres://$POSTGRES_USER:$POSTGRES_PASSWORD@127.0.0.1:$POSTGRES_PORT/$SCRATCH?sslmode=disable"
echo "=== migrate ==="
./backend/bin/migrate 2>&1 | tail -6
echo "=== count ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $SCRATCH -Atc "select count(*) from schema_migrations"
echo "=== 0057 objects ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $SCRATCH -Atc "select relname, relkind from pg_class where relname like 'user_activity%' order by relname"
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $SCRATCH -Atc "select * from s7_partition_status()"
echo "=== rerun idempotency ==="
./backend/bin/migrate 2>&1 | tail -3
echo RERUN_DONE
