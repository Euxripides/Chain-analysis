set -uo pipefail
cd /home/euripides/projects/blockchain-platform
PGP=$(python3 -c "
import re,pathlib
t=pathlib.Path('deploy/.env').read_text()
m=re.search(r'^POSTGRES_PASSWORD=(.*)',t,re.M); print(m.group(1).strip())")
q(){ printf "%s\n" "$1" | PGPASSWORD="$PGP" psql -h 127.0.0.1 -p 5433 -U blockchain -d blockchain_platform -v ON_ERROR_STOP=1 -At -F'|'; }
echo "=== DB_RUNTIME_ROLE present? ==="
grep -c '^DB_RUNTIME_ROLE=' deploy/.env; grep '^DB_RUNTIME_ROLE=' deploy/.env
echo "=== columns: download_jobs ==="
q "SELECT column_name||':'||data_type FROM information_schema.columns WHERE table_name='download_jobs' ORDER BY ordinal_position;" | tr '\n' ' '; echo
echo "=== columns: download_shards ==="
q "SELECT column_name||':'||data_type FROM information_schema.columns WHERE table_name='download_shards' ORDER BY ordinal_position;" | tr '\n' ' '; echo
echo "=== columns: checkpoints ==="
q "SELECT column_name||':'||data_type FROM information_schema.columns WHERE table_name='checkpoints' ORDER BY ordinal_position;" | tr '\n' ' '; echo
echo "=== columns: outbox_events ==="
q "SELECT column_name||':'||data_type FROM information_schema.columns WHERE table_name='outbox_events' ORDER BY ordinal_position;" | tr '\n' ' '; echo
echo "=== primary keys ==="
q "SELECT c.relname||' -> '||string_agg(a.attname,',' ORDER BY array_position(i.indkey,a.attnum)) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum=ANY(i.indkey) WHERE i.indisprimary AND c.relname IN ('download_jobs','download_shards','checkpoints','outbox_events','platform_jobs','backup_jobs','job_event_log') GROUP BY c.relname;"
echo "=== row counts ==="
q "SELECT 'download_jobs',count(*)::text FROM download_jobs UNION ALL SELECT 'download_shards',count(*)::text FROM download_shards UNION ALL SELECT 'checkpoints',count(*)::text FROM checkpoints UNION ALL SELECT 'outbox_events',count(*)::text FROM outbox_events UNION ALL SELECT 'platform_jobs',count(*)::text FROM platform_jobs UNION ALL SELECT 'backup_jobs',count(*)::text FROM backup_jobs UNION ALL SELECT 'job_event_log',count(*)::text FROM job_event_log ORDER BY 1;"
