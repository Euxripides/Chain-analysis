set -a; . /home/euripides/projects/blockchain-platform/deploy/.env; set +a
PG="docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB"
echo "=== job states ==="
$PG -c "select status, count(*) from download_jobs group by 1 order by 2 desc"
echo "=== recent active jobs ==="
$PG -c "select id::text, status, updated_at from download_jobs where status in ('RUNNING','PENDING','QUEUED','PLANNING','PAUSED') order by updated_at desc limit 5"
echo "=== worker/api pids ==="
pgrep -af 'bin/worker|bin/api'
echo "=== outbox ==="
$PG -c "select status, count(*) from outbox_events group by 1"
echo "=== nats container ==="
sudo -n docker ps --format '{{.Names}} {{.Status}}' | grep nats
echo SCRIPT_END
