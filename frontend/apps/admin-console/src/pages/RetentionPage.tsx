// A-DATA-04 保留与删除（路由 /data/retention · 权限 admin:retention:read）。
//
// 保留策略表 / 影响预览（dry-run）/ 版本化编辑（If-Match）/ 删除请求（双人审批 + 实际删除开关）。
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：正文只放业务数据、字段标签与操作控件，
// 说明 / 警告 / 成功 / 错误 / 风险 / 确认一律通过 useDialog() 弹窗。

import { useMemo, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Metric,
  PageHeader,
  StatusBadge,
  SkeletonRows,
  useDialog,
} from "@chain/ui";
import { CheckboxRow, JsonBlock, KeyValueList, NumberInput, SelectInput } from "../components/s6";
import {
  fmtBytes,
  fmtDateTime,
  fmtNumber,
  useAdminCommand,
  useAdminList,
  useAdminPatch,
} from "../lib/adminApi";

const PERM_READ = "admin:retention:read";
const PERM_MANAGE = "admin:retention:manage";
const PERM_DELETION_READ = "admin:deletion:read";
const PERM_DELETION_REQUEST = "admin:deletion:request";
const PERM_DELETION_APPROVE = "admin:deletion:approve";

type DialogApi = ReturnType<typeof useDialog>;

const RESOURCE_TYPES = [
  { value: "raw_object", label: "raw_object（原始对象）" },
  { value: "export_object", label: "export_object（导出对象）" },
  { value: "user_activity", label: "user_activity（用户行为）" },
  { value: "audit_archive", label: "audit_archive（审计归档）" },
  { value: "clickhouse_partition", label: "clickhouse_partition（ClickHouse 分区）" },
];

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
    dialog.notify("warning", "双人审批规则", "请求人与审批人不能是同一人，请由另一位管理员审批该删除请求。", details);
    return;
  }
  if (code === "DELETION_DISABLED") {
    dialog.notify(
      "warning",
      "实际删除默认关闭",
      "governance.deletion.execution_enabled=false 时不会执行任何删除；审批通过只授权流程，不产生实际数据删除。",
      details,
    );
    return;
  }
  if (code === "NOT_IMPLEMENTED") {
    dialog.notify("warning", "该能力尚未开放", "实际删除执行需专项验收后开放，当前接口不执行删除。", details);
    return;
  }
  if (code === "CONFLICT_STATE") {
    dialog.notify("warning", "当前状态不允许该操作", null, details);
    return;
  }
  dialog.notify("error", fallbackTitle, error instanceof ApiError ? error.userMessage : "请稍后重试", details);
}

function warnNoPermission(dialog: DialogApi, permission: string): void {
  dialog.notify("warning", "缺少控制权限", "当前账号未被授予该操作所需的权限，请求未发送。", [
    { label: "所需权限", value: permission },
  ]);
}

async function askStepUpPassword(dialog: DialogApi): Promise<string | null> {
  const raw = await dialog.prompt({
    kind: "warning",
    title: "二次身份确认",
    body: "该操作需要在当前会话重新确认身份。密码只用于本次校验，不写入日志。",
    input: {
      label: "当前账号密码",
      placeholder: "请输入当前账号密码",
      pattern: /^.{1,200}$/,
      patternHint: "请输入当前账号密码",
    },
    submitLabel: "确认身份",
  });
  if (!raw || !raw.startsWith("input:")) return null;
  const value = raw.slice("input:".length).trim();
  return value === "" ? null : value;
}

async function askRequiredNote(dialog: DialogApi, title: string, label: string): Promise<string | null> {
  const raw = await dialog.prompt({
    kind: "warning",
    title,
    body: "处置说明会写入审计日志，不能为空。",
    input: { label, placeholder: "例如：已核对审批单与影响预览", pattern: /^.{1,500}$/, patternHint: "1-500 个字符" },
    submitLabel: "提交",
  });
  if (!raw || !raw.startsWith("input:")) return null;
  const value = raw.slice("input:".length).trim();
  return value === "" ? null : value;
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

function shortId(value: string | undefined): string {
  if (!value) return "—";
  return value.length > 18 ? value.slice(0, 10) + "…" + value.slice(-6) : value;
}

interface RetentionPolicy {
  id: string;
  policy_key?: string;
  description?: string;
  resource_type?: string;
  scope_type?: string;
  retain_days?: number;
  archive_after_days?: number;
  action?: string;
  grace_days?: number;
  legal_hold?: boolean;
  enabled?: boolean;
  last_run_at?: string;
  next_run_at?: string;
  revision?: number;
  updated_at?: string;
}

interface RetentionPreview {
  policy_key?: string;
  resource_type?: string;
  action?: string;
  policy_enabled?: boolean;
  legal_hold?: boolean;
  retain_days?: number;
  scanned_objects?: number;
  matched_objects?: number;
  matched_bytes?: number;
  matched_rows?: number;
  block_reason?: string;
  cutoff?: string;
  deletion_execution_enabled?: boolean;
  source?: string;
}

interface PreviewResponse {
  preview?: RetentionPreview;
  run_id?: string;
  dry_run?: boolean;
}

interface DeletionRequestRow {
  id: string;
  resource_type?: string;
  policy_id?: string;
  reason?: string;
  status?: string;
  dry_run?: boolean;
  execution_enabled?: boolean;
  matched_objects?: number;
  matched_bytes?: number;
  approval_id?: string;
  requested_by_email?: string;
  restore_window_until?: string;
  executed_at?: string;
  result?: unknown;
  created_at?: string;
  updated_at?: string;
}

interface PolicyForm {
  retain_days: number;
  action: string;
  grace_days: number;
  enabled: boolean;
  legal_hold: boolean;
}

const ACTION_LABELS: Record<string, string> = { archive: "归档", delete: "删除", none: "不处理" };

function actionLabel(value: string | undefined): string {
  return ACTION_LABELS[value ?? ""] ?? (value || "—");
}

function deletionTone(status: string | undefined): "ok" | "warn" | "danger" | "info" | "neutral" {
  switch (status) {
    case "pending_approval":
      return "warn";
    case "approved":
      return "info";
    case "executed":
      return "ok";
    case "blocked_disabled":
      return "neutral";
    case "rejected":
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

export default function RetentionPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const command = useAdminCommand();
  const patch = useAdminPatch();
  const policies = useAdminList<RetentionPolicy>("/api/admin/v1/retention-policies", {}, 20);
  const deletions = useAdminList<DeletionRequestRow>("/api/admin/v1/deletion-requests", {}, 20);

  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [form, setForm] = useState<PolicyForm>({
    retain_days: 0,
    action: "none",
    grace_days: 0,
    enabled: false,
    legal_hold: false,
  });
  const [lastPreview, setLastPreview] = useState<PreviewResponse | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [deletionResourceType, setDeletionResourceType] = useState("raw_object");
  const [deletionPolicyKey, setDeletionPolicyKey] = useState("");

  const items = policies.items;
  const selected = useMemo(() => items.find((row) => row.id === selectedId) ?? null, [items, selectedId]);
  const executionEnabled = policies.extra.deletion_execution_enabled === true;
  const deletionRows = deletions.items;

  const pickPolicy = (row: RetentionPolicy) => {
    setSelectedId(row.id);
    setForm({
      retain_days: num(row.retain_days),
      action: row.action ?? "none",
      grace_days: num(row.grace_days),
      enabled: row.enabled === true,
      legal_hold: row.legal_hold === true,
    });
  };

  const showCaliber = () => {
    dialog.notify("info", "保留与删除口径", null, [
      { label: "策略来源", value: "GET /api/admin/v1/retention-policies" },
      { label: "删除请求来源", value: "GET /api/admin/v1/deletion-requests" },
      { label: "实际删除开关", value: executionEnabled ? "已启用（governance.deletion.execution_enabled=true）" : "默认关闭（governance.deletion.execution_enabled=false）" },
      { label: "预览语义", value: "dry_run=true：只做只读统计，不修改任何数据" },
      { label: "编辑并发控制", value: "PATCH 需要 If-Match: revision（版本不匹配返回 412）" },
      { label: "法务保留", value: "legal_hold=true 的策略，任何删除都会被拒绝" },
      { label: "审批规则", value: "删除请求需要第二位管理员审批（TWO_PERSON_REQUIRED）" },
      { label: "可恢复窗口", value: "请求创建后 7 天（restore_window_until）" },
      { label: "接口 note", value: text(policies.extra.note) },
    ]);
  };

  const runPreview = async (row: RetentionPolicy) => {
    if (!can(PERM_READ)) {
      warnNoPermission(dialog, PERM_READ);
      return;
    }
    setPreviewLoading(true);
    try {
      const response = await client.post<PreviewResponse>("/api/admin/v1/retention-policies/preview", {
        policy_key: row.policy_key,
        resource_type: row.resource_type,
      });
      const data = response.data ?? null;
      setLastPreview(data);
      const preview = data?.preview;
      dialog.notify(
        "warning",
        "保留影响预览（dry_run，不修改任何数据）",
        "预览只做只读统计；实际删除受 governance.deletion.execution_enabled 控制，默认关闭。",
        [
          { label: "策略", value: text(preview?.policy_key) },
          { label: "资源类型", value: text(preview?.resource_type) },
          { label: "动作", value: actionLabel(preview?.action) },
          { label: "保留天数", value: fmtNumber(preview?.retain_days) },
          { label: "命中对象", value: fmtNumber(preview?.matched_objects) },
          { label: "命中容量", value: fmtBytes(preview?.matched_bytes) },
          { label: "命中行数", value: fmtNumber(preview?.matched_rows) },
          { label: "截止时间", value: fmtDateTime(preview?.cutoff) },
          { label: "阻断原因", value: text(preview?.block_reason) },
          { label: "实际删除开关", value: preview?.deletion_execution_enabled ? "已启用" : "默认关闭" },
          { label: "运行 ID", value: text(data?.run_id) },
          { label: "数据来源", value: text(preview?.source) },
        ],
      );
    } catch (error) {
      notifyCommandError(dialog, error, "保留影响预览失败");
    } finally {
      setPreviewLoading(false);
    }
  };

  const savePolicy = async () => {
    if (!can(PERM_MANAGE)) {
      warnNoPermission(dialog, PERM_MANAGE);
      return;
    }
    if (!selected) {
      dialog.notify("warning", "尚未选择策略", "请先在策略表中选择要编辑的保留策略。");
      return;
    }
    await patch.run({
      path: "/api/admin/v1/retention-policies/" + selected.id,
      ifMatch: selected.revision,
      body: {
        retain_days: form.retain_days,
        action: form.action,
        grace_days: form.grace_days,
        enabled: form.enabled,
        legal_hold: form.legal_hold,
      },
      confirmTitle: "保存保留策略？",
      confirmBody: "策略变更会影响后续的归档与删除判定，变更原因会写入审计日志。",
      details: [
        { label: "策略", value: text(selected.policy_key) },
        { label: "If-Match revision", value: fmtNumber(selected.revision) },
        { label: "保留天数", value: fmtNumber(selected.retain_days) + " → " + fmtNumber(form.retain_days) },
        { label: "动作", value: actionLabel(selected.action) + " → " + actionLabel(form.action) },
        { label: "宽限天数", value: fmtNumber(selected.grace_days) + " → " + fmtNumber(form.grace_days) },
        { label: "启用", value: yesNo(selected.enabled) + " → " + yesNo(form.enabled) },
        { label: "法务保留", value: yesNo(selected.legal_hold) + " → " + yesNo(form.legal_hold) },
      ],
      successTitle: "保留策略已保存",
      onDone: () => policies.reload(),
    });
  };

  const createDeletionRequest = async () => {
    if (!can(PERM_DELETION_REQUEST)) {
      warnNoPermission(dialog, PERM_DELETION_REQUEST);
      return;
    }
    await command.run({
      path: "/api/admin/v1/deletion-requests",
      body: { resource_type: deletionResourceType, policy_key: deletionPolicyKey },
      requireStepUp: true,
      variant: "danger",
      confirmTitle: "提交删除请求？",
      confirmBody: "删除请求只会登记影响预览与审批链；实际删除受治理开关控制，默认关闭。",
      confirmLabel: "提交删除请求",
      details: [
        { label: "资源类型", value: deletionResourceType },
        { label: "保留策略", value: deletionPolicyKey || "未指定" },
        { label: "实际删除开关", value: executionEnabled ? "已启用" : "默认关闭" },
      ],
      successTitle: "删除请求已提交",
      successDetails: (data) => {
        const preview = (data.preview ?? {}) as RetentionPreview;
        return [
          { label: "请求 ID", value: text(data.id) },
          { label: "状态", value: text(data.status) },
          { label: "命中对象", value: fmtNumber(preview.matched_objects) },
          { label: "命中容量", value: fmtBytes(preview.matched_bytes) },
          { label: "实际删除开关", value: data.execution_enabled === true ? "已启用" : "默认关闭" },
          { label: "接口 note", value: text(data.note) },
        ];
      },
      onDone: () => {
        deletions.reload();
        policies.reload();
      },
    });
  };

  const deletionCommand = async (row: DeletionRequestRow, action: "approve" | "reject" | "cancel" | "execute") => {
    const permission = action === "approve" || action === "reject" ? PERM_DELETION_APPROVE : PERM_DELETION_REQUEST;
    if (!can(permission)) {
      warnNoPermission(dialog, permission);
      return;
    }
    const titles: Record<string, string> = {
      approve: "填写审批通过说明",
      reject: "填写驳回说明",
      cancel: "填写取消说明",
      execute: "填写执行说明",
    };
    const note = await askRequiredNote(dialog, titles[action], "处置说明");
    if (!note) return;
    await command.run({
      path: "/api/admin/v1/deletion-requests/" + row.id + "/commands/" + action,
      body: { note },
      requireReason: false,
      variant: action === "approve" ? "primary" : "danger",
      confirmTitle: "确认执行「" + action + "」命令？",
      confirmBody:
        action === "execute"
          ? "实际删除受 governance.deletion.execution_enabled 控制；关闭时接口会拒绝执行。"
          : "该命令会写入审计日志与审批链。",
      confirmLabel: "提交命令",
      details: [
        { label: "请求 ID", value: shortId(row.id) },
        { label: "资源类型", value: text(row.resource_type) },
        { label: "当前状态", value: text(row.status) },
        { label: "实际删除开关", value: row.execution_enabled === true ? "已启用" : "默认关闭" },
        { label: "处置说明", value: note },
      ],
      successTitle: "命令已提交",
      onDone: () => {
        deletions.reload();
        policies.reload();
      },
    });
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="保留与删除"
          subtitle={
            <>
              读取 {PERM_READ} · 实际删除执行开关{" "}
              <span className="mono">{executionEnabled ? "enabled" : "disabled"}</span>
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
                  policies.reload();
                  deletions.reload();
                }}
              >
                刷新
              </button>
            </>
          }
        />

        <Card title="删除执行开关">
          <div className="metric-grid">
            <Metric
              label="deletion_execution_enabled"
              value={executionEnabled ? "已启用" : "默认关闭"}
              tone={executionEnabled ? "danger" : "ok"}
              hint="governance.deletion.execution_enabled"
            />
            <Metric label="保留策略数（本页）" value={fmtNumber(items.length)} tone="neutral" hint="GET /api/admin/v1/retention-policies" />
            <Metric label="删除请求数（本页）" value={fmtNumber(deletionRows.length)} tone="neutral" hint="GET /api/admin/v1/deletion-requests" />
            <Metric
              label="待审批删除请求"
              value={fmtNumber(deletionRows.filter((row) => row.status === "pending_approval").length)}
              tone={deletionRows.some((row) => row.status === "pending_approval") ? "warn" : "ok"}
              hint="status = pending_approval"
            />
          </div>
        </Card>

        {policies.loading && items.length === 0 ? (
          <SkeletonRows rows={4} columns={4} />
        ) : policies.error && items.length === 0 ? (
          <Card>
            <ErrorState title="保留策略加载失败" message={policies.error} onRetry={policies.reload} />
          </Card>
        ) : (
          <>
            <Card title={"保留策略（本页 " + items.length + "）"} flush>
              <DataTable<RetentionPolicy>
                loading={policies.loading}
                error={policies.error}
                onRetry={policies.reload}
                rows={items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有保留策略" />}
                columns={[
                  {
                    key: "policy",
                    header: "策略",
                    width: "220px",
                    render: (row) => (
                      <span>
                        <div className="mono">{row.policy_key ?? "—"}</div>
                        <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.description || row.scope_type || "—"}</div>
                      </span>
                    ),
                  },
                  { key: "resource", header: "资源类型", width: "170px", render: (row) => <span className="mono">{row.resource_type ?? "—"}</span> },
                  { key: "retain", header: "保留天数", width: "100px", align: "right", render: (row) => <span className="num">{fmtNumber(row.retain_days)}</span> },
                  { key: "archive", header: "归档天数", width: "100px", align: "right", render: (row) => <span className="num">{fmtNumber(row.archive_after_days)}</span> },
                  {
                    key: "action",
                    header: "动作",
                    width: "110px",
                    render: (row) => (
                      <StatusBadge tone={row.action === "delete" ? "danger" : row.action === "archive" ? "info" : "neutral"}>
                        {actionLabel(row.action)}
                      </StatusBadge>
                    ),
                  },
                  { key: "grace", header: "宽限天数", width: "100px", align: "right", render: (row) => <span className="num">{fmtNumber(row.grace_days)}</span> },
                  {
                    key: "legal",
                    header: "法务保留",
                    width: "110px",
                    render: (row) => <StatusBadge tone={row.legal_hold ? "warn" : "neutral"}>{row.legal_hold ? "已保留" : "未保留"}</StatusBadge>,
                  },
                  {
                    key: "enabled",
                    header: "启用",
                    width: "100px",
                    render: (row) => <StatusBadge tone={row.enabled ? "ok" : "neutral"}>{row.enabled ? "已启用" : "已停用"}</StatusBadge>,
                  },
                  { key: "revision", header: "版本", width: "80px", align: "right", render: (row) => <span className="num">{fmtNumber(row.revision)}</span> },
                  { key: "updated", header: "更新时间", width: "170px", render: (row) => fmtDateTime(row.updated_at) },
                  { key: "next", header: "下次运行", width: "170px", render: (row) => fmtDateTime(row.next_run_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "200px",
                    align: "right",
                    render: (row) => (
                      <>
                        <button
                          type="button"
                          className="btn btn--sm"
                          style={{ marginRight: "var(--sp-2)" }}
                          disabled={!can(PERM_READ) || previewLoading}
                          onClick={() => void runPreview(row)}
                        >
                          影响预览
                        </button>
                        <button type="button" className="btn btn--sm" disabled={!can(PERM_MANAGE)} onClick={() => pickPolicy(row)}>
                          编辑
                        </button>
                      </>
                    ),
                  },
                ]}
              />
            </Card>

            <Card title="策略编辑">
              <div className="toolbar" style={{ alignItems: "flex-end" }}>
                <SelectInput
                  id="retention-policy"
                  label="选择策略"
                  value={selectedId ?? ""}
                  onChange={(value) => {
                    const next = items.find((row) => row.id === value);
                    if (next) pickPolicy(next);
                  }}
                  options={[{ value: "", label: "未选择" }].concat(
                    items.map((row) => ({ value: row.id, label: (row.policy_key ?? shortId(row.id)) + " · v" + fmtNumber(row.revision) })),
                  )}
                />
                <NumberInput id="retention-days" label="保留天数" value={form.retain_days} min={1} onChange={(value) => setForm((current) => ({ ...current, retain_days: value }))} />
                <SelectInput
                  id="retention-action"
                  label="到期动作"
                  value={form.action}
                  onChange={(value) => setForm((current) => ({ ...current, action: value }))}
                  options={[
                    { value: "archive", label: "archive（归档）" },
                    { value: "delete", label: "delete（删除）" },
                    { value: "none", label: "none（不处理）" },
                  ]}
                />
                <NumberInput id="retention-grace" label="宽限天数" value={form.grace_days} min={0} onChange={(value) => setForm((current) => ({ ...current, grace_days: value }))} />
                <div style={{ flex: "0 1 auto" }}>
                  <CheckboxRow id="retention-enabled" label="启用策略" checked={form.enabled} onChange={(checked) => setForm((current) => ({ ...current, enabled: checked }))} />
                </div>
                <div style={{ flex: "0 1 auto" }}>
                  <CheckboxRow id="retention-legal" label="法务保留" checked={form.legal_hold} onChange={(checked) => setForm((current) => ({ ...current, legal_hold: checked }))} />
                </div>
                <button
                  type="button"
                  className="btn btn--primary btn--sm"
                  style={{ alignSelf: "flex-end" }}
                  disabled={!can(PERM_MANAGE) || patch.busy}
                  onClick={() => void savePolicy()}
                >
                  {patch.busy ? "保存中…" : "保存策略"}
                </button>
              </div>
              <div style={{ marginTop: "var(--sp-4)" }}>
                <KeyValueList
                  items={[
                    { label: "当前策略", value: selected?.policy_key ?? "—", mono: true },
                    { label: "当前 revision", value: fmtNumber(selected?.revision) },
                    { label: "当前资源类型", value: selected?.resource_type ?? "—" },
                    { label: "当前动作", value: actionLabel(selected?.action) },
                    { label: "当前保留天数", value: fmtNumber(selected?.retain_days) },
                    { label: "当前宽限天数", value: fmtNumber(selected?.grace_days) },
                    { label: "当前启用", value: yesNo(selected?.enabled) },
                    { label: "当前法务保留", value: yesNo(selected?.legal_hold) },
                    { label: "最近运行", value: fmtDateTime(selected?.last_run_at) },
                  ]}
                />
              </div>
            </Card>

            <Card title="最近一次影响预览">
              <KeyValueList
                items={[
                  { label: "dry_run", value: lastPreview ? yesNo(lastPreview.dry_run) : "—" },
                  { label: "run_id", value: text(lastPreview?.run_id), mono: true },
                  { label: "策略", value: text(lastPreview?.preview?.policy_key) },
                  { label: "资源类型", value: text(lastPreview?.preview?.resource_type) },
                  { label: "动作", value: actionLabel(lastPreview?.preview?.action) },
                  { label: "策略启用", value: yesNo(lastPreview?.preview?.policy_enabled) },
                  { label: "法务保留", value: yesNo(lastPreview?.preview?.legal_hold) },
                  { label: "保留天数", value: fmtNumber(lastPreview?.preview?.retain_days) },
                  { label: "扫描对象", value: fmtNumber(lastPreview?.preview?.scanned_objects) },
                  { label: "命中对象", value: fmtNumber(lastPreview?.preview?.matched_objects) },
                  { label: "命中容量", value: fmtBytes(lastPreview?.preview?.matched_bytes) },
                  { label: "命中行数", value: fmtNumber(lastPreview?.preview?.matched_rows) },
                  { label: "截止时间", value: fmtDateTime(lastPreview?.preview?.cutoff) },
                  { label: "阻断原因", value: text(lastPreview?.preview?.block_reason) },
                  { label: "实际删除开关", value: yesNo(lastPreview?.preview?.deletion_execution_enabled) },
                  { label: "数据来源", value: text(lastPreview?.preview?.source) },
                ]}
              />
            </Card>

            <Card title={"删除请求（本页 " + deletionRows.length + "）"} flush>
              <div className="toolbar" style={{ padding: "var(--sp-4) var(--sp-4) 0", alignItems: "flex-end" }}>
                <SelectInput
                  id="deletion-resource"
                  label="资源类型"
                  value={deletionResourceType}
                  onChange={setDeletionResourceType}
                  options={RESOURCE_TYPES}
                />
                <SelectInput
                  id="deletion-policy"
                  label="关联策略"
                  value={deletionPolicyKey}
                  onChange={setDeletionPolicyKey}
                  options={[{ value: "", label: "未指定" }].concat(
                    items
                      .filter((row) => row.policy_key)
                      .map((row) => ({ value: row.policy_key as string, label: row.policy_key as string })),
                  )}
                />
                <button
                  type="button"
                  className="btn btn--danger btn--sm"
                  style={{ alignSelf: "flex-end" }}
                  disabled={!can(PERM_DELETION_REQUEST) || command.busy}
                  onClick={() => void createDeletionRequest()}
                >
                  {command.busy ? "提交中…" : "发起删除请求"}
                </button>
              </div>
              <DataTable<DeletionRequestRow>
                loading={deletions.loading}
                error={deletions.error}
                onRetry={deletions.reload}
                rows={deletionRows}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有删除请求" />}
                columns={[
                  {
                    key: "id",
                    header: "请求",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "resource", header: "资源类型", width: "170px", render: (row) => <span className="mono">{row.resource_type ?? "—"}</span> },
                  {
                    key: "status",
                    header: "状态",
                    width: "160px",
                    render: (row) => <StatusBadge tone={deletionTone(row.status)}>{row.status ?? "—"}</StatusBadge>,
                  },
                  { key: "objects", header: "命中对象", width: "110px", align: "right", render: (row) => <span className="num">{fmtNumber(row.matched_objects)}</span> },
                  { key: "bytes", header: "命中容量", width: "110px", align: "right", render: (row) => <span className="num">{fmtBytes(row.matched_bytes)}</span> },
                  {
                    key: "flag",
                    header: "实际删除开关",
                    width: "140px",
                    render: (row) => <StatusBadge tone={row.execution_enabled ? "danger" : "ok"}>{row.execution_enabled ? "已启用" : "默认关闭"}</StatusBadge>,
                  },
                  { key: "requester", header: "请求人", width: "190px", render: (row) => row.requested_by_email || "—" },
                  { key: "window", header: "可恢复窗口至", width: "170px", render: (row) => fmtDateTime(row.restore_window_until) },
                  { key: "created", header: "创建时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "290px",
                    align: "right",
                    render: (row) => (
                      <>
                        <button
                          type="button"
                          className="btn btn--sm"
                          style={{ marginRight: "var(--sp-2)" }}
                          disabled={row.status !== "pending_approval" || !can(PERM_DELETION_APPROVE)}
                          onClick={() => void deletionCommand(row, "approve")}
                        >
                          通过
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          style={{ marginRight: "var(--sp-2)" }}
                          disabled={row.status !== "pending_approval" || !can(PERM_DELETION_APPROVE)}
                          onClick={() => void deletionCommand(row, "reject")}
                        >
                          驳回
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          style={{ marginRight: "var(--sp-2)" }}
                          disabled={row.status === "cancelled" || row.status === "executed" || !can(PERM_DELETION_REQUEST)}
                          onClick={() => void deletionCommand(row, "cancel")}
                        >
                          取消
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          disabled={
                            !can(PERM_DELETION_REQUEST) ||
                            row.status === "pending_approval" ||
                            row.status === "rejected" ||
                            row.status === "cancelled" ||
                            row.status === "executed"
                          }
                          onClick={() => void deletionCommand(row, "execute")}
                        >
                          执行
                        </button>
                      </>
                    ),
                  },
                ]}
              />
              <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"服务端 cursor 分页 · 本页 " + deletionRows.length + " 条"}
                </span>
                <span className="toolbar">
                  <button type="button" className="btn btn--sm" disabled={!deletions.hasPrevious || deletions.loading} onClick={deletions.goPrevious}>
                    上一页
                  </button>
                  <button type="button" className="btn btn--sm" disabled={!deletions.nextCursor || deletions.loading} onClick={deletions.goNext}>
                    下一页
                  </button>
                </span>
              </div>
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="治理证据链"
        items={[
          { label: "删除执行开关", value: executionEnabled ? "已启用" : "默认关闭" },
          { label: "策略来源", value: "postgres:retention_policies" },
          { label: "预览运行记录", value: "postgres:retention_runs（dry_run=true）" },
          { label: "删除请求来源", value: "postgres:deletion_requests" },
          { label: "审批规则", value: "第二位管理员（TWO_PERSON_REQUIRED）" },
          { label: "可恢复窗口", value: "创建后 7 天" },
          { label: "最近预览 run_id", value: text(lastPreview?.run_id) },
          { label: "最近预览截止", value: fmtDateTime(lastPreview?.preview?.cutoff) },
          { label: "最近预览阻断原因", value: text(lastPreview?.preview?.block_reason) },
        ]}
      />
    </div>
  );
}
