-- rollback-0056：S7-DB-01 作用域回填/约束回滚（§22.1：只回退约束，不回退数据）
-- 用法：psql -f rollback-0056.sql（仅在前滚失败或需要回退 S7-DB-01 时执行）
ALTER TABLE public.export_tickets          ALTER COLUMN organization_id DROP NOT NULL;
ALTER TABLE public.report_download_tickets ALTER COLUMN organization_id DROP NOT NULL;
ALTER TABLE public.audit_events_v2          DROP CONSTRAINT IF EXISTS audit_events_v2_s7_scope_coherence_check;
ALTER TABLE public.login_events             DROP CONSTRAINT IF EXISTS login_events_s7_scope_coherence_check;
ALTER TABLE public.user_activity_events     DROP CONSTRAINT IF EXISTS user_activity_events_s7_scope_coherence_check;
ALTER TABLE public.notifications            DROP CONSTRAINT IF EXISTS notifications_s7_scope_coherence_check;
ALTER TABLE public.export_tickets           DROP CONSTRAINT IF EXISTS export_tickets_s7_scope_coherence_check;
ALTER TABLE public.report_download_tickets  DROP CONSTRAINT IF EXISTS report_download_tickets_s7_scope_coherence_check;
ALTER TABLE public.feature_flag_evaluations DROP CONSTRAINT IF EXISTS feature_flag_evaluations_s7_scope_coherence_check;
ALTER TABLE public.feature_flags            DROP CONSTRAINT IF EXISTS feature_flags_scope_target_check;
ALTER TABLE public.feature_flags            DROP CONSTRAINT IF EXISTS feature_flags_ws_implies_org_check;
ALTER TABLE public.feature_flags            DROP COLUMN IF EXISTS scope_user_id;
DROP FUNCTION IF EXISTS public.s7_scope_audit();
-- 注意：feature_flag_evaluations 的回填行**不**回退（属于真实作用域修正，回退会造成新的不一致）。
