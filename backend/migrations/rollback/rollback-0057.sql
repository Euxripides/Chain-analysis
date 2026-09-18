-- rollback-0057：S7-DB-01 索引/分区回滚（§22.1：可删除新索引，不回退数据）
-- 用法：psql -f rollback-0057.sql
DROP INDEX IF EXISTS public.idx_download_pages_job_status;
DROP INDEX IF EXISTS public.idx_checkpoints_job;
DROP INDEX IF EXISTS public.idx_exports_job_org_created;
DROP INDEX IF EXISTS public.idx_audit_v2_seq_desc;
DROP INDEX IF EXISTS public.idx_audit_v2_result_seq;
DROP INDEX IF EXISTS public.idx_login_events_seq_desc;
DROP INDEX IF EXISTS public.idx_login_events_org_seq;
DROP INDEX IF EXISTS public.idx_login_events_type_seq;
DROP INDEX IF EXISTS public.idx_investigations_case_status;
DROP INDEX IF EXISTS public.idx_sessions_org_last_seen;
-- 注意：实际签名为 (int, date)；只写 (int) 的 DROP 是无效语句（S7-DOC-01 复核时发现并修正）。
DROP FUNCTION IF EXISTS public.s7_ensure_user_activity_partitions(int, date);
DROP FUNCTION IF EXISTS public.s7_ensure_user_activity_partitions(int);
DROP FUNCTION IF EXISTS public.s7_partition_archive_candidates(int);
DROP FUNCTION IF EXISTS public.s7_partition_status();
DROP FUNCTION IF EXISTS public.s7_partition_readiness();
-- 分区结构**不自动回滚**：DETACH + RENAME 会把历史数据留在 user_activity_events_hist，
-- 需要人工确认数据归属后再执行。完整步骤见 docs/manuals/rollback.md「分区结构回滚」一节：
--   1) ALTER TABLE public.user_activity_events DETACH PARTITION public.user_activity_events_hist;
--   2) ALTER TABLE public.user_activity_events_hist RENAME TO user_activity_events;
--   3) 重建 PK(id) 与被删除的二级索引；
--   4) 确认行数与 RENAME 前一致后再删除空的父表与分区。
-- 1)–4) 属数据搬迁动作，必须在维护窗口内由两人复核执行，禁止脚本化自动运行。
