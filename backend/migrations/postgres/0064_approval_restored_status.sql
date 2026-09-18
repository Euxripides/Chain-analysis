-- 0064_approval_restored_status.sql
-- Kill Switch 恢复流程写 approval_requests.status='restored'，但 0051 建立的
-- approval_requests_status_check 只允许 pending/approved/rejected/expired/executed/failed/cancelled。
--
-- 真实后果（2026-09-16 实测）：POST /api/admin/v1/control/kill-switch/requests/{id}/restore
-- 在 s6FinishApproval 处违反 CHECK -> 语句失败 -> 请求事务 aborted -> 提交门把响应改写成
-- 409 AUTHZ_WORKSPACE_SCOPE_STALE。Kill Switch 因此**无法恢复**，平台被永久留在停摆状态
-- （同一轮里它又导致下载任务停在 QUEUED、导出全部 EXPORT_FAILED）。
--
-- 处理：把 'restored' 纳入允许集合。代码侧 s6FinishApproval 的 CASE 已经显式区分
-- 'restored'（executed_at = now()），kill_switch_engagements.status 也早已允许 'restored'，
-- 缺的只是这张表的约束同步。

ALTER TABLE approval_requests DROP CONSTRAINT IF EXISTS approval_requests_status_check;
ALTER TABLE approval_requests ADD CONSTRAINT approval_requests_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executed', 'failed', 'cancelled', 'restored'));

-- 自检：约束必须存在且包含 restored，否则迁移失败（不允许静默降级）。
DO $$
DECLARE
    def text;
BEGIN
    SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint
     WHERE conname = 'approval_requests_status_check' AND conrelid = 'approval_requests'::regclass;
    IF def IS NULL THEN
        RAISE EXCEPTION 'approval_requests_status_check missing after 0064';
    END IF;
    IF position('restored' in def) = 0 THEN
        RAISE EXCEPTION 'approval_requests_status_check does not allow restored: %', def;
    END IF;
END
$$;
