# ACME 测试夹具清理记录（2026-09-18）

> 执行人：执行 Agent（依据 `docs/multiuser-login-and-isolation-plan.md` §8 D2 与附录 A.6）
> 目标：清空 ACME 测试夹具组织（slug=`acme`）两个工作区下的全部业务对象，为"公网真实用户 + 用户级数据隔离"让出干净起点。
> 结论：**已清理并复核为 0；备份与可复盘证据齐全；未触碰审计链、身份、角色与成员关系。**

## 1. 范围

| 项 | 值 |
|---|---|
| 组织 | `627c131a-3842-4e19-bade-d20ffb8aea8f`（slug=`acme`，名称「ACME 调查组（测试夹具）」） |
| 工作区 | `14dfbf1b-b3d7-4326-8395-b1f7fa9f8872`（acme-lab）、`231875b4-2687-4535-aba6-cbee410c5020`（acme-lab-other） |
| 数据库 | `blockchain_platform` @ 127.0.0.1:5433（容器 `deploy-postgres-1`，PostgreSQL 18.6） |
| 脚本 | `scripts/ops/cleanup-acme-fixtures-20260918.sql`（单事务，`ON_ERROR_STOP=1`） |

**保留未删**：`identities`（fixture-* 六个夹具账号）、`credentials`、`roles`/`role_permissions`、`organization_memberships`、`workspace_memberships`、`member_role_bindings`、`audit_events_v2`、`login_events`（append-only，且是验收证据）、`sessions`。

## 2. 备份（清理前）

| 项 | 值 |
|---|---|
| 文件 | `/home/euripides/backups/blockchain_platform-pre-acme-cleanup-20260918.dump` |
| 格式 / 大小 | `pg_dump -Fc`（容器内 18.6 版本导出，避免 16.x 客户端版本不匹配）/ 143 MB |
| SHA-256 | 见同目录 `.sha256` |
| 恢复方式 | `docker exec -i deploy-postgres-1 pg_restore -U blockchain -d <目标库> < dump` |

## 3. 删除明细（每张表均为脚本返回的真实删除行数）

| 表 | 删除行数 | 表 | 删除行数 |
|---|---|---|---|
| cases | 150 | investigation_reports | 107 |
| investigations | 176 | report_approvals | 44 |
| investigation_findings | 535 | report_download_tickets | 26 |
| investigation_evidence | 10263 | review_requests | 44 |
| finding_evidence_links | 10237 | notifications | 116 |
| graph_patches | 99 | notification_delivery_attempts | 116 |
| investigation_runs | 102 | export_tickets | 224 |
| investigation_canvases | 74 | export_object_migrations | 114 |
| case_activity | 968 | exports | 171 |
| case_comments | 28 | download_jobs | 108 |
| case_members | 176 | download_datasets | 108 |
| case_subjects | 65 | download_subjects | 108 |
| job_event_log | 583 | entity_relationships / investigation_subjects / canvas_snapshots / download_shards / download_volume_guards / address_dataset_sync_state / address_case_links / address_tag_links / tags / workspace_addresses / projects / update_previews / user_activity_events | 0（本就没有数据） |

## 4. 复核（清理后）

`SELECT count(*) ... WHERE workspace_id IN (acme 两个工作区)` 全部为 **0**：cases、investigations、investigation_canvases、investigation_reports、investigation_findings、investigation_evidence、download_jobs、exports、export_tickets、notifications、review_requests、report_download_tickets、tags、workspace_addresses、projects、update_previews、user_activity_events（17 张表）。

## 5. 对象存储清理（MinIO）

- 桶 `blockchain-exports` 下 `exports/` 前缀原有 **377** 个对象；其中 **171** 个属于本次清理的导出（key 从备份 dump 中按 `workspace_id` 反解，与桶内清单逐一比对命中 171/171）。
- 已删除 171 个对象（合计约 5.05 MiB，其中 5 个为 0 字节）；删除后 `exports/` 剩余 **206** 个对象（属于其他工作区，未触碰）。

## 6. 过程记录（含一次失败与回滚）

1. **v1 脚本失败并整体回滚**：v1 漏写 `DELETE FROM investigations`，在 `DELETE FROM cases` 时触发 `investigations_case_id_fkey` 外键错误；由于脚本在单事务内且 `ON_ERROR_STOP=1`，**未产生任何变更**（复核 cases 仍为 150）。
2. **v2 修正并成功**：补上 `investigations` 的删除（置于其全部子表之后、`cases` 之前），重新执行，`COMMIT` 后复核全 0。
3. 教训：批量清理脚本必须"先建 id 快照、再按叶子→根顺序删除"，并且必须在事务内以 `ON_ERROR_STOP` 运行——本次正是这条纪律把一次外键失败变成了零影响。

## 7. 后续（不在本次范围）

- `default` 工作区仍有 26 个案件（3 个真人案件 + 23 个脚本产物）与 2363 条下载任务，按 §8 D2 的待确认口径处理。
- ACME 组织/工作区外壳与夹具身份保留；若确定不再需要验收环境，可另行删除（需先确认没有测试脚本依赖）。
