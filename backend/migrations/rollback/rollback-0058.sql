-- rollback-0058：仅回滚账本表本身（不涉及任何对象存储动作）
-- 用法：psql -f rollback-0058.sql
-- 注意：删除账本会丢失"哪些对象已复制/已验证"的记录，重建后只能重新全量核对；
--       本回滚**不会**、也**不允许**删除任何 MinIO 对象，RAW 侧对象始终保留。
DROP TABLE IF EXISTS public.export_object_migrations;
