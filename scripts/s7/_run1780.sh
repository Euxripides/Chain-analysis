echo BEGIN
cd /home/euripides/projects/blockchain-platform || exit 9
set -a; . deploy/.env; set +a
EV=acceptance-evidence/dual-system-v1/sprint7/db
mkdir -p "$EV"
PG="docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB"
echo "--- A derivable ---"
$PG -Atc "select count(*) from feature_flag_evaluations ffe join organization_memberships m on m.identity_id::text = ffe.subject_id where ffe.organization_id is null and ffe.subject_type='user' and m.status='active'"
$PG -Atc "select count(*) from export_tickets et join exports e on e.id=et.export_id where et.organization_id is null and e.organization_id is not null"
echo "--- B hash payload evidence ---"
grep -n "organization_id" backend/internal/audit/verify.go | head -3
grep -n "organization_id" backend/internal/audit/writer.go | head -3
echo "--- C tenant tables ---"
$PG -c "select table_name, rows, org_null, ws_null, ws_without_org from s7_scope_audit() where rows > 0 order by table_name" | head -30
echo END
echo SCRIPT_END
