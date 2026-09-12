// A-BACKUP-01 备份与恢复（路由 /system/backups · 权限 admin:backup:read）。
//
// 备份列表 / 创建备份 / 校验 checksum / 恢复演练（preview → 第二位管理员审批 + 维护窗口 → 执行）。
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：正文只放业务数据、字段标签与操作控件，
// 说明 / 警告 / 成功 / 错误 / 风险 / 确认一律通过 useDialog() 弹窗。

import { useMemo, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EntityLink,
  EvidenceRail,
  Metric,
  PageHeader,
  StatusBadge,
  SkeletonRows,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { JsonBlock, KeyValueList, SelectInput } from "../components/s6";
import {
  fmtBytes,
  fmtDateTime,
  fmtNumber,
  useAdminCommand,
  useAdminList,
} from "../lib/adminApi";

const PERM_READ = "admin:backup:read";
const PERM_CREATE = "admin:backup:create";
const PERM_RESTORE = "admin:backup:restore";

type DialogApi = ReturnType<typeof useDialog>;

function apiErrorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  if (error.code) details.push({ label: "错误码", value: error.code });
  Object.entries(error.details ?? {}).forEach(([key, value]) => {
    if (value === null || value === undefined) return;
    details.push({ label: key, value: typeof value === "object" ? JSON.stringify(value) : String(value) });
  });
  return details;
}

function notifyCommandError(dialog: DialogApi, error: unknown, fallbackTitle: string): void {
  const details = apiErrorDetails(error);
  const code = error instanceof ApiError ? error.code : "";
  if (code === "TWO_PERSON_REQUIRED") {
    dialog.notify("warning", "双人审批规则", "请求人与审批人不能是同一人，请由另一位管理员审批。", details);
    return;
  }
  if (code === "APPROVAL_REQUIRED") {
    dialog.notify("warning", "需要先完成审批", "恢复必须先由第二位管理员审批通过，且处于维护窗口内。", details);
    return;
  }
  if (code === "MAINTENANCE_WINDOW") {
    dialog.notify("warning", "不在维护窗口内", "恢复只能在维护窗口内提交或执行。", details);
    return;
  }
  dialog.notify("error", fallbackTitle, error instanceof ApiError ? error.userMessage : "请稍后重试", details);
}

function warnNoPermission(dialog: DialogApi, permission: string): void {
  dialog.notify("warning", "缺少控制权限", "当前账号未被授予该操作所需的权限，请求未发送。", [
    { label: "所需权限", value: permission },
  ]);
}

function num(value: unknown): number {
  const parsed = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function text(value: unknown): string {
  return typeof value === "string" && value !== "" ? value : "—";
}

function yesNo(value: unknown): string {
  return value === true ? "是" : "否";
}

function asStringList(value: unknown): string {
  if (!Array.isArray(value) || value.length === 0) return "—";
  return value.map((item) => String(item)).join(" / ");
}

function shortId(value: string | undefined): string {
  if (!value) return "—";
  return value.length > 18 ? value.slice(0, 10) + "…" + value.slice(-6) : value;
}

interface BackupJob {
  id: string;
  kind?: string;
  target?: string;
  status?: string;
  drill?: boolean;
  requested_by_email?: string;
  approval_id?: string;
  backup_id?: string;
  started_at?: string;
  finished_at?: string;
  size_bytes?: number;
  object_ref?: string;
  checksum?: string;
  schema_version?: string;
  build_sha?: string;
  verify_status?: string;
  verify_detail?: Record<string, unknown>;
  rpo_seconds?: number;
  rto_seconds?: number;
  maintenance_window?: string;
  notes?: string;
  result?: unknown;
  created_at?: string;
}

interface RestoreImpact {
  target?: string;
  overwrites?: unknown;
  merges?: unknown;
  never_touched?: unknown;
  requires_window?: boolean;
  maintenance_window?: string;
  approval_required?: boolean;
  second_approver?: boolean;
  conflict_count?: number;
  settings_in_snapshot?: unknown;
}

interface RestorePreview {
  preview_id?: string;
  backup_id?: string;
  impact?: RestoreImpact;
  verify_status?: string;
  dry_run?: boolean;
}

interface RestoreRow {
  id: string;
  backup_id?: string;
  target?: string;
  conflict_count?: number;
  verified?: boolean;
  verify_detail?: Record<string, unknown>;
  maintenance_window?: string;
  approval_id?: string;
  created_at?: string;
}

const TARGET_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  clickhouse: "ClickHouse",
  minio: "MinIO",
  config: "配置",
  all: "全部",
};

function targetLabel(value: string | undefined): string {
  return TARGET_LABELS[value ?? ""] ?? (value || "—");
}

function backupTone(value: string | undefined): "ok" | "warn" | "danger" | "info" | "neutral" {
  const upper = (value ?? "").toUpperCase();
  if (["SUCCESS", "SUCCEEDED", "DONE", "COMPLETED", "VERIFIED", "OK"].includes(upper)) return "ok";
  if (["RUNNING", "QUEUED", "PENDING", "IN_PROGRESS", "VERIFYING"].includes(upper)) return "warn";
  if (["FAILED", "ERROR", "MISMATCH", "CORRUPTED"].includes(upper)) return "danger";
  return "neutral";
}

function impactItems(impact: RestoreImpact | undefined): Array<{ label: string; value: ReactNode }> {
  const data = impact ?? {};
  return [
    { label: "恢复目标", value: targetLabel(data.target) },
    { label: "覆盖对象", value: asStringList(data.overwrites) },
    { label: "合并对象", value: asStringList(data.merges) },
    { label: "不会触碰", value: asStringList(data.never_touched) },
    { label: "冲突数量", value: fmtNumber(num(data.conflict_count)) },
    { label: "需要维护窗口", value: yesNo(data.requires_window) },
    { label: "维护窗口", value: text(data.maintenance_window) },
    { label: "需要审批", value: yesNo(data.approval_required) },
    { label: "需要第二位管理员", value: yesNo(data.second_approver) },
    { label: "快照内设置", value: data.settings_in_snapshot === undefined ? "—" : <JsonBlock value={data.settings_in_snapshot} label="settings_in_snapshot" /> },
  ];
}

export default function BackupsPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const command = useAdminCommand();
  const backups = useAdminList<BackupJob>("/api/admin/v1/backups", {}, 20);
  const restores = useAdminList<RestoreRow>("/api/admin/v1/restores", {}, 20);

  const [targetBackupId, setTargetBackupId] = useState("");
  const [preview, setPreview] = useState<RestorePreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);

  const items = backups.items;
  const backupOptions = useMemo(
    () =>
      items
        .filter((row) => (row.kind ?? "backup") === "backup")
        .map((row) => ({
          value: row.id,
          label: shortId(row.id) + " · " + targetLabel(row.target) + " · " + (row.status ?? "—") + " · " + fmtDateTime(row.created_at),
        })),
    [items],
  );
  const selectedBackup = items.find((row) => row.id === targetBackupId) ?? null;
  const lastSuccessful = text(backups.extra.last_successful_backup_at);
  const latestBackup = items[0] ?? null;

  const showCaliber = () => {
    dialog.notify("info", "备份与恢复口径", null, [
      { label: "备份来源", value: "GET /api/admin/v1/backups" },
      { label: "恢复来源", value: "GET /api/admin/v1/restores" },
      { label: "校验方式", value: "对象级 checksum 重算并与备份记录比对" },
      { label: "恢复前置", value: "preview → 第二位管理员审批 → 维护窗口内执行" },
      { label: "RPO / RTO", value: "来自备份记录字段，非推算值" },
      { label: "数据来源", value: text(backups.extra.data_source) },
      { label: "最近成功备份", value: lastSuccessful },
    ]);
  };

  const createBackup = async () => {
    if (!can(PERM_CREATE)) {
      warnNoPermission(dialog, PERM_CREATE);
      return;
    }
    const choice = await dialog.open({
      kind: "confirm",
      title: "选择备份目标",
      body: "备份在后台异步执行，完成后可在列表中校验 checksum。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "PostgreSQL", result: "postgres", variant: "primary" },
        { label: "ClickHouse", result: "clickhouse" },
        { label: "MinIO", result: "minio" },
        { label: "配置", result: "config" },
        { label: "全部", result: "all" },
      ],
    });
    if (!choice) return;
    await command.run({
      path: "/api/admin/v1/backups",
      body: { target: choice },
      confirmTitle: "提交备份任务？",
      confirmBody: "备份会读取数据库与对象存储，期间可能产生额外 IO 负载。",
      confirmLabel: "创建备份",
      details: [{ label: "备份目标", value: targetLabel(choice) }],
      successTitle: "备份任务已提交",
      successDetails: (data) => [
        { label: "任务 ID", value: shortId(text(data.id)) },
        { label: "状态", value: text(data.status) },
        { label: "对象引用", value: text(data.object_ref) },
        { label: "校验状态", value: text(data.verify_status) },
      ],
      onDone: () => backups.reload(),
    });
  };

  const verifyBackup = async (row: BackupJob) => {
    if (!can(PERM_READ)) {
      warnNoPermission(dialog, PERM_READ);
      return;
    }
    await command.run({
      path: "/api/admin/v1/backups/" + row.id + "/commands/verify",
      body: {},
      requireReason: false,
      confirmTitle: "校验该备份的 checksum？",
      confirmBody: "系统会重新计算对象实际 checksum，并与备份记录比对。",
      confirmLabel: "开始校验",
      details: [
        { label: "任务 ID", value: shortId(row.id) },
        { label: "目标", value: targetLabel(row.target) },
        { label: "记录 checksum", value: text(row.checksum) },
      ],
      successTitle: "校验完成",
      successDetails: (data) => {
        const detail = (data.detail ?? {}) as Record<string, unknown>;
        return [
          { label: "校验状态", value: text(data.verify_status) },
          { label: "期望 checksum", value: text(detail.expected_checksum) },
          { label: "实际 checksum", value: text(detail.actual_checksum) },
          { label: "比对结果", value: detail.checksum_matches === true ? "一致" : detail.checksum_matches === false ? "不一致" : "—" },
          { label: "schema 最高迁移", value: text(detail.schema_migrations_max) },
        ];
      },
      onDone: () => backups.reload(),
    });
  };

  const showBackupDetail = (row: BackupJob) => {
    void dialog.open({
      kind: "info",
      title: "备份任务详情",
      details: [
        { label: "任务 ID", value: <span className="mono">{row.id}</span> },
        { label: "类型", value: row.kind ?? "backup" },
        { label: "目标", value: targetLabel(row.target) },
        { label: "状态", value: <StatusBadge tone={backupTone(row.status)}>{row.status ?? "—"}</StatusBadge> },
        { label: "演练", value: row.drill ? "是" : "否" },
        { label: "创建人", value: row.requested_by_email || "—" },
        { label: "审批单", value: row.approval_id || "—" },
        { label: "备份对象", value: row.backup_id || "—" },
        { label: "开始时间", value: fmtDateTime(row.started_at) },
        { label: "完成时间", value: fmtDateTime(row.finished_at) },
        { label: "大小", value: fmtBytes(row.size_bytes) },
        { label: "对象引用", value: row.object_ref || "—" },
        { label: "checksum", value: row.checksum || "—" },
        { label: "schema 版本", value: row.schema_version || "—" },
        { label: "构建 SHA", value: row.build_sha || "未注入" },
        { label: "校验状态", value: row.verify_status || "—" },
        { label: "RPO（秒）", value: fmtNumber(row.rpo_seconds) },
        { label: "RTO（秒）", value: fmtNumber(row.rto_seconds) },
        { label: "维护窗口", value: row.maintenance_window || "—" },
        { label: "备注", value: row.notes || "—" },
        { label: "校验明细", value: <JsonBlock value={row.verify_detail} label="verify_detail" /> },
        { label: "结果", value: <JsonBlock value={row.result} label="result" /> },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  const runPreview = async () => {
    if (!can(PERM_RESTORE)) {
      warnNoPermission(dialog, PERM_RESTORE);
      return;
    }
    if (!targetBackupId) {
      dialog.notify("warning", "尚未选择备份", "请先在恢复演练区选择要恢复的备份任务。");
      return;
    }
    setPreviewLoading(true);
    try {
      const response = await client.post<RestorePreview>("/api/admin/v1/restores/preview", {
        backup_id: targetBackupId,
      });
      const data = response.data ?? null;
      setPreview(data);
      dialog.notify(
        "warning",
        "恢复影响预览（dry-run，未修改任何数据）",
        "恢复需要第二位管理员审批，并且只能在维护窗口内执行。",
        [
          { label: "预览 ID", value: text(data?.preview_id) },
          { label: "备份任务", value: shortId(text(data?.backup_id)) },
          ...impactItems(data?.impact),
          { label: "校验状态", value: text(data?.verify_status) },
        ],
      );
    } catch (error) {
      notifyCommandError(dialog, error, "恢复影响预览失败");
    } finally {
      setPreviewLoading(false);
    }
  };

  const submitRestore = async () => {
    if (!can(PERM_RESTORE)) {
      warnNoPermission(dialog, PERM_RESTORE);
      return;
    }
    if (!targetBackupId) {
      dialog.notify("warning", "尚未选择备份", "请先在恢复演练区选择要恢复的备份任务。");
      return;
    }
    await command.run({
      path: "/api/admin/v1/restores",
      body: { backup_id: targetBackupId, preview_id: preview?.preview_id },
      requireStepUp: true,
      variant: "danger",
      confirmTitle: "提交恢复请求？",
      confirmBody: "恢复会覆盖目标对象的当前内容，需要第二位管理员审批，并且只能在维护窗口内执行。",
      confirmLabel: "提交恢复请求",
      details: [
        { label: "备份任务", value: shortId(targetBackupId) },
        { label: "预览 ID", value: text(preview?.preview_id) },
        ...impactItems(preview?.impact),
      ],
      successTitle: "恢复请求已提交",
      successDetails: (data) => [
        { label: "恢复 ID", value: text(data.id) },
        { label: "状态", value: text(data.status) },
        { label: "审批单", value: text((data.approval as Record<string, unknown> | undefined)?.id) },
        { label: "备注", value: text(data.note) },
        { label: "审批规则", value: "需要第二位管理员审批（second_approver）" },
      ],
      onDone: () => {
        restores.reload();
        backups.reload();
      },
    });
  };

  const showRestoreDetail = async (row: RestoreRow) => {
    void dialog.open({
      kind: "info",
      title: "恢复请求详情",
      details: [
        { label: "恢复请求 ID", value: <span className="mono">{row.id}</span> },
        { label: "来源备份", value: <span className="mono">{row.backup_id || "—"}</span> },
        { label: "目标", value: targetLabel(row.target) },
        { label: "冲突数量", value: fmtNumber(row.conflict_count) },
        { label: "校验", value: row.verified ? "已校验" : "未校验" },
        { label: "维护窗口", value: row.maintenance_window || "—" },
        { label: "审批单", value: row.approval_id || "—" },
        { label: "提交时间", value: fmtDateTime(row.created_at) },
        { label: "校验明细", value: <JsonBlock value={row.verify_detail} label="verify_detail" /> },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="备份与恢复"
          subtitle={
            <>
              读取 {PERM_READ} · 最近成功备份 <span className="mono">{lastSuccessful}</span>
            </>
          }
          actions={
            <>
              <button type="button" className="btn btn--sm" onClick={showCaliber}>
                口径
              </button>
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => {
                  backups.reload();
                  restores.reload();
                }}
              >
                刷新
              </button>
            </>
          }
        />

        {backups.loading && items.length === 0 ? (
          <SkeletonRows rows={4} columns={4} />
        ) : backups.error && items.length === 0 ? (
          <Card>
            <ErrorState title="备份列表加载失败" message={backups.error} onRetry={backups.reload} />
          </Card>
        ) : (
          <>
            <Card title="备份概览">
              <div className="metric-grid">
                <Metric label="最近成功备份" value={<span style={{ fontSize: "var(--fs-16)" }}>{lastSuccessful}</span>} tone={lastSuccessful === "—" ? "warn" : "ok"} hint="last_successful_backup_at" />
                <Metric label="本页备份任务" value={fmtNumber(items.length)} tone="neutral" hint="GET /api/admin/v1/backups" />
                <Metric label="RPO（秒）" value={fmtNumber(latestBackup?.rpo_seconds)} tone="neutral" hint={latestBackup ? shortId(latestBackup.id) : "—"} />
                <Metric label="RTO（秒）" value={fmtNumber(latestBackup?.rto_seconds)} tone="neutral" hint={latestBackup ? shortId(latestBackup.id) : "—"} />
                <Metric label="本页恢复请求" value={fmtNumber(restores.items.length)} tone="neutral" hint="GET /api/admin/v1/restores" />
              </div>
            </Card>

            <Card
              title={"备份任务（本页 " + items.length + "）"}
              actions={
                <button type="button" className="btn btn--primary btn--sm" disabled={!can(PERM_CREATE) || command.busy} onClick={() => void createBackup()}>
                  创建备份
                </button>
              }
              flush
            >
              <DataTable<BackupJob>
                loading={backups.loading}
                error={backups.error}
                onRetry={backups.reload}
                rows={items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有备份任务" />}
                columns={[
                  {
                    key: "id",
                    header: "任务",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "kind", header: "类型", width: "100px", render: (row) => row.kind ?? "backup" },
                  { key: "target", header: "目标", width: "120px", render: (row) => targetLabel(row.target) },
                  {
                    key: "status",
                    header: "状态",
                    width: "140px",
                    render: (row) => (
                      <span>
                        <StatusBadge tone={backupTone(row.status)}>{row.status ?? "—"}</StatusBadge>
                        {row.drill ? <span className="badge badge--info" style={{ marginLeft: 4 }}>演练</span> : null}
                      </span>
                    ),
                  },
                  { key: "size", header: "大小", width: "110px", align: "right", render: (row) => <span className="num">{fmtBytes(row.size_bytes)}</span> },
                  {
                    key: "checksum",
                    header: "checksum",
                    width: "190px",
                    render: (row) => (row.checksum ? <EntityLink value={row.checksum} /> : "—"),
                  },
                  {
                    key: "object_ref",
                    header: "对象引用",
                    width: "220px",
                    render: (row) => (row.object_ref ? <EntityLink value={row.object_ref} label={row.object_ref.length > 24 ? row.object_ref.slice(0, 24) + "…" : row.object_ref} /> : "—"),
                  },
                  {
                    key: "verify",
                    header: "校验",
                    width: "130px",
                    render: (row) => <StatusBadge tone={backupTone(row.verify_status)}>{row.verify_status || "未校验"}</StatusBadge>,
                  },
                  { key: "requester", header: "创建人", width: "190px", render: (row) => row.requested_by_email || "—" },
                  { key: "created", header: "创建时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "180px",
                    align: "right",
                    render: (row) => (
                      <>
                        <button type="button" className="btn btn--sm" style={{ marginRight: "var(--sp-2)" }} disabled={!can(PERM_READ) || command.busy} onClick={() => void verifyBackup(row)}>
                          校验
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          onClick={() => {
                            showBackupDetail(row);
                          }}
                        >
                          详情
                        </button>
                      </>
                    ),
                  },
                ]}
              />
              <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"服务端 cursor 分页 · 本页 " + items.length + " 条"}
                </span>
                <span className="toolbar">
                  <button type="button" className="btn btn--sm" disabled={!backups.hasPrevious || backups.loading} onClick={backups.goPrevious}>
                    上一页
                  </button>
                  <button type="button" className="btn btn--sm" disabled={!backups.nextCursor || backups.loading} onClick={backups.goNext}>
                    下一页
                  </button>
                </span>
              </div>
            </Card>

            <Card title="恢复演练">
              <div className="toolbar" style={{ alignItems: "flex-end" }}>
                <SelectInput
                  id="restore-backup"
                  label="选择备份任务"
                  value={targetBackupId}
                  onChange={(value) => {
                    setTargetBackupId(value);
                    setPreview(null);
                  }}
                  options={[{ value: "", label: "未选择" }].concat(backupOptions)}
                />
                <button
                  type="button"
                  className="btn btn--sm"
                  style={{ alignSelf: "flex-end" }}
                  disabled={!can(PERM_RESTORE) || previewLoading}
                  onClick={() => void runPreview()}
                >
                  {previewLoading ? "预览中…" : "预览影响"}
                </button>
                <button
                  type="button"
                  className="btn btn--danger btn--sm"
                  style={{ alignSelf: "flex-end" }}
                  disabled={!can(PERM_RESTORE) || command.busy}
                  onClick={() => void submitRestore()}
                >
                  {command.busy ? "提交中…" : "提交恢复请求"}
                </button>
                <span className="shell__spacer" />
                <StatusBadge tone={preview ? "warn" : "neutral"}>
                  {preview ? "已生成影响预览" : "未预览"}
                </StatusBadge>
              </div>
              <div style={{ marginTop: "var(--sp-4)" }}>
                <KeyValueList
                  items={[
                    { label: "已选备份", value: shortId(targetBackupId) },
                    { label: "备份目标", value: targetLabel(selectedBackup?.target) },
                    { label: "备份状态", value: selectedBackup?.status ?? "—" },
                    { label: "预览 ID", value: text(preview?.preview_id) },
                    { label: "dry_run", value: preview ? yesNo(preview.dry_run) : "—" },
                    ...impactItems(preview?.impact),
                  ]}
                />
              </div>
            </Card>

            <Card title={"恢复请求（本页 " + restores.items.length + "）"} flush>
              <DataTable<RestoreRow>
                loading={restores.loading}
                error={restores.error}
                onRetry={restores.reload}
                rows={restores.items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有恢复请求" />}
                columns={[
                  {
                    key: "id",
                    header: "恢复请求",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  {
                    key: "backup",
                    header: "来源备份",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.backup_id ?? ""}>{shortId(row.backup_id)}</span>,
                  },
                  { key: "target", header: "目标", width: "120px", render: (row) => targetLabel(row.target) },
                  { key: "conflicts", header: "冲突数量", width: "110px", align: "right", render: (row) => <span className="num">{fmtNumber(row.conflict_count)}</span> },
                  {
                    key: "verified",
                    header: "校验",
                    width: "110px",
                    render: (row) => <StatusBadge tone={row.verified ? "ok" : "warn"}>{row.verified ? "已校验" : "未校验"}</StatusBadge>,
                  },
                  { key: "window", header: "维护窗口", width: "190px", render: (row) => row.maintenance_window || "—" },
                  {
                    key: "approval",
                    header: "审批单",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.approval_id ?? ""}>{shortId(row.approval_id)}</span>,
                  },
                  { key: "created", header: "提交时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "90px",
                    align: "right",
                    render: (row) => (
                      <button type="button" className="btn btn--sm" onClick={() => void showRestoreDetail(row)}>
                        详情
                      </button>
                    ),
                  },
                ]}
              />
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="备份证据链"
        items={[
          { label: "最近成功备份", value: lastSuccessful },
          { label: "最近备份状态", value: latestBackup?.status ?? "—" },
          { label: "最近备份 checksum", value: latestBackup?.checksum ?? "—" },
          { label: "最近备份对象", value: latestBackup?.object_ref ?? "—" },
          { label: "schema 版本", value: latestBackup?.schema_version ?? "—" },
          { label: "构建 SHA", value: latestBackup?.build_sha || "未注入" },
          { label: "维护窗口", value: text(preview?.impact?.maintenance_window) },
          { label: "需要第二位管理员", value: yesNo(preview?.impact?.second_approver) },
          { label: "数据来源", value: text(backups.extra.data_source) },
        ]}
      />
    </div>
  );

}
