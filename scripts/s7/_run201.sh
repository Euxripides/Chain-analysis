set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
EV=acceptance-evidence/dual-system-v1/sprint7
mkdir -p $EV/db $EV/backups
TS=$(date -u +%Y%m%dT%H%M%SZ)
echo "=== pre-migration snapshot -> $EV/db/pre-migration-$TS.txt ==="
{
echo "# S7-DB-01 迁移前基线  $TS"
echo "## bin sha"; sha256sum backend/bin/api backend/bin/worker backend/bin/migrate 2>/dev/null
echo "## schema_migrations count"; docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from schema_migrations"
echo "## scope audit"; docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select * from (select 1) x" >/dev/null 2>&1 || true
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -c "select relname, seq_scan, idx_scan, n_live_tup from pg_stat_user_tables where relname in ('download_pages','checkpoints','audit_events_v2','login_events','exports','investigations','sessions','user_activity_events') order by relname"
echo "## index counts"; docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from pg_indexes where schemaname='public'"
echo "## partition state"; docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB -Atc "select count(*) from pg_inherits"
} | tee $EV/db/pre-migration-$TS.txt | tail -25
echo "=== pg_dump backup ==="
docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 pg_dump -U $POSTGRES_USER -d $POSTGRES_DB -Fc -f /tmp/pre_s7.dump
docker cp deploy-postgres-1:/tmp/pre_s7.dump $EV/backups/pre-s7-pg-$TS.dump
ls -la $EV/backups/
sha256sum $EV/backups/pre-s7-pg-$TS.dump | tee $EV/backups/pre-s7-pg-$TS.dump.sha256
echo "BACKUP_OK TS=$TS"