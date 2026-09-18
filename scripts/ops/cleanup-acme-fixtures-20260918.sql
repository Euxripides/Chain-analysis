-- cleanup-acme-fixtures-20260918.sql
-- 目的：清理 ACME 测试夹具组织下两个工作区的全部业务对象（案件/调查/画布/报告/Finding/Evidence/
--       下载任务/导出/通知/复核请求等）。identities / roles / memberships / 审计事件一律保留。
-- 范围：workspace_id IN (acme-lab, acme-lab-other)
-- 备份：/home/euripides/backups/blockchain_platform-pre-acme-cleanup-20260918.dump（附 .sha256）
-- 语义：单事务；任一步失败整体回滚；每条 DELETE 以 CTE 返回删除行数作为证据。
-- 修订：v2 修正 v1 漏删 investigations 导致 cases 外键失败（v1 已整体回滚，未产生任何变更）。
BEGIN;

CREATE TEMP TABLE acme_ws(id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO acme_ws VALUES ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872'), ('231875b4-2687-4535-aba6-cbee410c5020');

CREATE TEMP TABLE t_case   ON COMMIT DROP AS SELECT id FROM cases                  WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_inv    ON COMMIT DROP AS SELECT id FROM investigations         WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_canvas ON COMMIT DROP AS SELECT id FROM investigation_canvases WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_job    ON COMMIT DROP AS SELECT id FROM download_jobs          WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_export ON COMMIT DROP AS SELECT id FROM exports                WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_report ON COMMIT DROP AS SELECT id FROM investigation_reports  WHERE workspace_id IN (SELECT id FROM acme_ws);
CREATE TEMP TABLE t_find   ON COMMIT DROP AS SELECT id FROM investigation_findings WHERE investigation_id IN (SELECT id FROM t_inv);
CREATE TEMP TABLE t_evid   ON COMMIT DROP AS SELECT id FROM investigation_evidence WHERE investigation_id IN (SELECT id FROM t_inv);
CREATE TEMP TABLE t_notif  ON COMMIT DROP AS SELECT id FROM notifications          WHERE workspace_id IN (SELECT id FROM acme_ws);

-- 叶子 → 根（每条输出：表名|删除行数）
WITH d AS (UPDATE investigation_canvases SET parent_canvas_id = NULL WHERE id IN (SELECT id FROM t_canvas) RETURNING 1) SELECT 'investigation_canvases(parent->NULL)' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM finding_evidence_links WHERE finding_id IN (SELECT id FROM t_find) OR evidence_id IN (SELECT id FROM t_evid) RETURNING 1) SELECT 'finding_evidence_links' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_evidence WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'investigation_evidence' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_findings WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'investigation_findings' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM graph_patches WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'graph_patches' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM entity_relationships WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'entity_relationships' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_runs WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'investigation_runs' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_subjects WHERE investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'investigation_subjects' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM report_approvals WHERE report_id IN (SELECT id FROM t_report) RETURNING 1) SELECT 'report_approvals' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM report_download_tickets WHERE report_id IN (SELECT id FROM t_report) OR workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'report_download_tickets' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_reports WHERE id IN (SELECT id FROM t_report) RETURNING 1) SELECT 'investigation_reports' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM review_requests WHERE workspace_id IN (SELECT id FROM acme_ws) OR case_id IN (SELECT id FROM t_case) OR investigation_id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'review_requests' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM canvas_snapshots WHERE canvas_id IN (SELECT id FROM t_canvas) RETURNING 1) SELECT 'canvas_snapshots' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigation_canvases WHERE id IN (SELECT id FROM t_canvas) RETURNING 1) SELECT 'investigation_canvases' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM investigations WHERE id IN (SELECT id FROM t_inv) RETURNING 1) SELECT 'investigations' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM case_activity WHERE case_id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'case_activity' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM case_comments WHERE case_id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'case_comments' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM case_members WHERE case_id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'case_members' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM case_subjects WHERE case_id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'case_subjects' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM notification_delivery_attempts WHERE notification_id IN (SELECT id FROM t_notif) RETURNING 1) SELECT 'notification_delivery_attempts' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM notifications WHERE id IN (SELECT id FROM t_notif) RETURNING 1) SELECT 'notifications' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM export_tickets WHERE export_id IN (SELECT id FROM t_export) OR workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'export_tickets' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM export_object_migrations WHERE export_id IN (SELECT id FROM t_export) RETURNING 1) SELECT 'export_object_migrations' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM exports WHERE id IN (SELECT id FROM t_export) RETURNING 1) SELECT 'exports' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM download_datasets WHERE job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'download_datasets' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM download_shards WHERE job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'download_shards' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM download_subjects WHERE job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'download_subjects' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM download_volume_guards WHERE job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'download_volume_guards' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM job_event_log WHERE job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'job_event_log' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM address_dataset_sync_state WHERE source_job_id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'address_dataset_sync_state' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM download_jobs WHERE id IN (SELECT id FROM t_job) RETURNING 1) SELECT 'download_jobs' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM address_case_links WHERE workspace_id IN (SELECT id FROM acme_ws) OR case_id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'address_case_links' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM address_tag_links WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'address_tag_links' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM tags WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'tags' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM workspace_addresses WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'workspace_addresses' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM projects WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'projects' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM update_previews WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'update_previews' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM user_activity_events WHERE workspace_id IN (SELECT id FROM acme_ws) RETURNING 1) SELECT 'user_activity_events' AS tbl, count(*) AS deleted FROM d;
WITH d AS (DELETE FROM cases WHERE id IN (SELECT id FROM t_case) RETURNING 1) SELECT 'cases' AS tbl, count(*) AS deleted FROM d;

COMMIT;

-- 复核：以下所有计数必须为 0
SELECT 'cases' AS tbl, count(*) AS remaining FROM cases WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'investigations', count(*) FROM investigations WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'investigation_canvases', count(*) FROM investigation_canvases WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'investigation_reports', count(*) FROM investigation_reports WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'download_jobs', count(*) FROM download_jobs WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'exports', count(*) FROM exports WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'export_tickets', count(*) FROM export_tickets WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'notifications', count(*) FROM notifications WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'review_requests', count(*) FROM review_requests WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'report_download_tickets', count(*) FROM report_download_tickets WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'tags', count(*) FROM tags WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'workspace_addresses', count(*) FROM workspace_addresses WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'projects', count(*) FROM projects WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'update_previews', count(*) FROM update_previews WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'user_activity_events', count(*) FROM user_activity_events WHERE workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'investigation_findings', count(*) FROM investigation_findings f JOIN investigations i ON i.id=f.investigation_id WHERE i.workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
UNION ALL SELECT 'investigation_evidence', count(*) FROM investigation_evidence e JOIN investigations i ON i.id=e.investigation_id WHERE i.workspace_id IN ('14dfbf1b-b3d7-4326-8395-b1f7fa9f8872','231875b4-2687-4535-aba6-cbee410c5020')
ORDER BY 1;