set -e
cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
DB=blockchain_platform_dr_restore
Q() { docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $DB -Atc "$1"; }
JOB=$(Q "select job_id::text from download_pages where job_id is not null limit 1")
EXJOB=$(Q "select job_id::text from exports where job_id is not null limit 1")
EXORG=$(Q "select organization_id::text from exports where job_id is not null limit 1")
CASE=$(Q "select case_id::text from investigations where case_id is not null limit 1")
ORG=$(Q "select organization_id::text from sessions where organization_id is not null limit 1")
echo "ids: job=$JOB exjob=$EXJOB exorg=$EXORG case=$CASE org=$ORG"
mkdir -p /tmp/idxab
cat > /tmp/idxab/queries.txt <<EOF
Q1_download_pages_job_sum|SELECT COALESCE(SUM(rows_inserted),0) FROM download_pages WHERE job_id='$JOB' AND status IN ('INGESTED','VALIDATED')
Q2_checkpoints_job_max|SELECT max(last_success_at) FROM checkpoints WHERE job_id='$JOB'
Q3_exports_job_org|SELECT id,status FROM exports WHERE job_id='$EXJOB' AND organization_id='$EXORG' ORDER BY created_at DESC LIMIT 20
Q4_audit_seq_desc|SELECT id,seq FROM audit_events_v2 ORDER BY seq DESC LIMIT 50
Q5_audit_result_seq|SELECT id,seq FROM audit_events_v2 WHERE result='allow' ORDER BY seq DESC LIMIT 50
Q6_login_seq_desc|SELECT id,seq FROM login_events ORDER BY seq DESC LIMIT 50
Q7_login_org_seq|SELECT id,seq FROM login_events WHERE organization_id='$ORG' ORDER BY seq DESC LIMIT 50
Q8_login_type_seq|SELECT id,seq FROM login_events WHERE event_type='auth.login.failure' ORDER BY seq DESC LIMIT 50
Q9_investigations_case_status|SELECT count(*) FROM investigations WHERE case_id='$CASE' AND status NOT IN ('completed','cancelled','failed')
Q10_sessions_org|SELECT id,last_seen_at FROM sessions WHERE organization_id='$ORG' ORDER BY last_seen_at DESC LIMIT 50
EOF
run_all() {
  local tag="$1"
  : > /tmp/idxab/$tag.txt
  while IFS='|' read -r name q; do
    [ -z "$name" ] && continue
    echo "### $name" >> /tmp/idxab/$tag.txt
    echo "SQL: $q" >> /tmp/idxab/$tag.txt
    docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $DB -c "EXPLAIN (ANALYZE, BUFFERS, TIMING ON) $q" >> /tmp/idxab/$tag.txt 2>&1
    echo >> /tmp/idxab/$tag.txt
  done < /tmp/idxab/queries.txt
  grep -E 'Execution Time|###' /tmp/idxab/$tag.txt | paste - - 2>/dev/null | sed 's/### //' || true
}
echo "=== BEFORE (no S7 indexes) ==="
run_all before
echo "=== apply S7 index DDL ==="
time docker exec -i -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $DB <<'SQL'
CREATE INDEX IF NOT EXISTS idx_download_pages_job_status ON public.download_pages (job_id, status);
CREATE INDEX IF NOT EXISTS idx_checkpoints_job ON public.checkpoints (job_id);
CREATE INDEX IF NOT EXISTS idx_exports_job_org_created ON public.exports (job_id, organization_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_v2_seq_desc ON public.audit_events_v2 (seq DESC);
CREATE INDEX IF NOT EXISTS idx_audit_v2_result_seq ON public.audit_events_v2 (result, seq DESC);
CREATE INDEX IF NOT EXISTS idx_login_events_seq_desc ON public.login_events (seq DESC);
CREATE INDEX IF NOT EXISTS idx_login_events_org_seq ON public.login_events (organization_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_login_events_type_seq ON public.login_events (event_type, seq DESC);
CREATE INDEX IF NOT EXISTS idx_investigations_case_status ON public.investigations (case_id, status);
CREATE INDEX IF NOT EXISTS idx_sessions_org_last_seen ON public.sessions (organization_id, last_seen_at DESC);
ANALYZE;
SQL
echo "=== AFTER ==="
run_all after
echo "=== diff ==="
for n in Q1_download_pages_job_sum Q2_checkpoints_job_max Q3_exports_job_org Q4_audit_seq_desc Q5_audit_result_seq Q6_login_seq_desc Q7_login_org_seq Q8_login_type_seq Q9_investigations_case_status Q10_sessions_org; do
  b=$(awk -v n="$n" '$0=="### "n{f=1} f&&/Execution Time/{print $3; exit}' /tmp/idxab/before.txt)
  a=$(awk -v n="$n" '$0=="### "n{f=1} f&&/Execution Time/{print $3; exit}' /tmp/idxab/after.txt)
  printf "%-34s before=%-10s after=%-10s\n" "$n" "$b" "$a"
done | tee /tmp/idxab/diff.txt
cp /tmp/idxab/before.txt /tmp/idxab/after.txt /tmp/idxab/diff.txt acceptance-evidence/dual-system-v1/sprint7/db/ 2>/dev/null || true
echo DONE