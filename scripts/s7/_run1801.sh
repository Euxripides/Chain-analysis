cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
EV=acceptance-evidence/dual-system-v1/sprint7/db
mkdir -p $EV
P() { docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB "$@"; }
{
echo "# S7-DB-01 verification raw evidence ($(date -u +%Y-%m-%dT%H:%M:%SZ))"
echo; echo "## 1. schema_migrations"
P -c "select name, applied_at from schema_migrations where name like '%0056%' or name like '%0057%' order by name"
echo; echo "## 2. scope audit (after 0056)"
P -c "select * from s7_scope_audit() where org_null>0 or ws_null>0 or ws_without_org>0 order by table_name"
echo; echo "## 3. new constraints"
P -c "select conrelid::regclass as tbl, conname, convalidated from pg_constraint where conname like '%s7_scope%' or conname like 'feature_flags_scope%' or conname like 'feature_flags_ws%' order by 1,2"
echo; echo "## 4. NOT NULL enforcement"
P -c "select table_name, column_name, is_nullable from information_schema.columns where table_name in ('export_tickets','report_download_tickets') and column_name='organization_id'"
echo; echo "## 5. partition inventory"
P -c "select * from s7_partition_status()"
echo; echo "## 6. partition readiness gate"
P -c "select table_name, ready, array_to_string(blockers, ' | ') as blockers from s7_partition_readiness()"
echo; echo "## 7. index inventory (S7 indexes)"
P -c "select indexname, indexdef from pg_indexes where schemaname='public' and indexname in ('idx_download_pages_job_status','idx_checkpoints_job','idx_exports_job_org_created','idx_audit_v2_seq_desc','idx_audit_v2_result_seq','idx_login_events_seq_desc','idx_login_events_org_seq','idx_login_events_type_seq','idx_investigations_case_status','idx_sessions_org_last_seen','user_activity_identity_idx','user_activity_time_idx','user_activity_org_time_idx') order by indexname"
echo; echo "## 8. index counts"
P -Atc "select count(*) from pg_indexes where schemaname='public'"
echo; echo "## 9. partition maintenance log"
P -c "select table_name, action, partition_name, detail from partition_maintenance_log order by created_at"
echo; echo "## 10. feature_flags scope columns"
P -c "select key, scope_type, scope_org_id, scope_workspace_id, scope_user_id from feature_flags order by key"
} > $EV/verification-raw.txt 2>&1
tail -60 $EV/verification-raw.txt
echo "=== files in db evidence ==="; ls -la $EV/