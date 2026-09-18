set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
TS=$(date -u +%Y%m%dT%H%M%SZ)
EV=acceptance-evidence/dual-system-v1/sprint7/dr
mkdir -p $EV
DUMP=$(ls -t $EV/../backups/pre-s7-pg-*.dump | head -1)
echo "dump=$DUMP"
echo "=== restore target ==="
TARGET=blockchain_platform_dr_restore
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d postgres -q -c "DROP DATABASE IF EXISTS $TARGET" -c "CREATE DATABASE $TARGET OWNER $POSTGRES_USER"
docker cp "$DUMP" deploy-postgres-1:/tmp/dr.dump
echo "=== restore start $(date -u +%H:%M:%S.%N) ==="
START=$(date +%s.%N)
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 pg_restore -U $POSTGRES_USER -d $TARGET -j 4 --no-owner --no-privileges /tmp/dr.dump 2>&1 | tail -5
END=$(date +%s.%N)
echo "=== restore end $(date -u +%H:%M:%S.%N) ==="
python3 -c "print('RTO_pg_restore_seconds=%.2f' % ($END-$START))" | tee $EV/pg-restore-$TS.rto.txt
echo "=== verify row counts restored vs live ==="
for t in audit_events_v2 login_events sessions users cases investigations download_jobs exports provider_usage download_pages checkpoints; do
  live=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from $t")
  rest=$(docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $TARGET -Atc "select count(*) from $t")
  printf "%-24s live=%-10s restored=%-10s delta=%s\n" "$t" "$live" "$rest" "$((live-rest))"
done | tee $EV/pg-restore-$TS.counts.txt
echo "=== schema_migrations in restore ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $TARGET -Atc "select count(*) from schema_migrations"
echo TS=$TS