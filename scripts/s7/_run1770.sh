cd /home/euripides/projects/blockchain-platform
set -a; . deploy/.env; set +a
P() { docker exec -e PGPASSWORD=$POSTGRES_PASSWORD deploy-postgres-1 psql -U $POSTGRES_USER -d $POSTGRES_DB "$@"; }
{
echo "## A. 回填可推导性核对（现在仍可推导的行数，用于证明历史 NULL 不可推导）"
P -c "select count(*) as derivable_org from feature_flag_evaluations ffe join organization_memberships m on m.identity_id::text = ffe.subject_id where ffe.organization_id is null and ffe.subject_type='user' and m.status='active'"
P -c "select count(*) as derivable_ws from feature_flag_evaluations ffe join workspace_memberships w on w.identity_id::text = ffe.subject_id where ffe.workspace_id is null and ffe.subject_type='user' and w.status='active'"
P -c "select count(*) as ticket_backfill_candidates from export_tickets et join exports e on e.id=et.export_id where et.organization_id is null and e.organization_id is not null"
echo "## B. 迁移前空值基线（来自 pre-migration 快照）"
echo "audit_events_v2 org_null=51 ws_null=57 (of 1046); login_events org_null=966 ws_null=1058 (of 2241); feature_flag_evaluations org_null=33 ws_null=33 (of 33)"
echo "## C. 哈希链载荷包含 organization_id 的代码证据"
grep -n "organization_id" backend/internal/audit/verify.go | head -4
grep -n "organization_id" backend/internal/audit/writer.go | head -3
echo "## D. 实际回填结果（迁移为幂等，重放后影响行数）"
P -c "select 'feature_flag_evaluations org' as what, count(*) from feature_flag_evaluations where organization_id is null"
echo "## E. 全库 tenant 表 scope 现状"
P -c "select table_name, rows, org_null, ws_null, ws_without_org from s7_scope_audit() where rows > 0 order by table_name"
} > acceptance-evidence/dual-system-v1/sprint7/db/scope-derivation-checks.txt 2>&1
cat acceptance-evidence/dual-system-v1/sprint7/db/scope-derivation-checks.txt
echo SCRIPT_END
