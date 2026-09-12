// A-KILL-01 Kill Switch（路由 /control/kill-switch · 权限 admin:killswitch:read）。
//
// 四步流程：① 影响预览 → ② 提交请求（原因 + step-up）→ ③ 双人审批 → ④ 执行 / 恢复（step-up + 健康验证）。
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：正文只放业务数据、字段标签与操作控件，
// 说明 / 警告 / 成功 / 错误 / 风险 / 确认一律通过 useDialog() 弹窗。

import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { JsonBlock, KeyValueList, SelectInput, Timeline, type TimelineEntry } from "../components/s6";
import {
  fmtDateTime,
  fmtNumber,
  useAdminCommand,
  useAdminList,
  useAdminResource,
} from "../lib/adminApi";

const PERM_READ = "admin:killswitch:read";
const PERM_ENGAGE = "admin:killswitch:engage";
const PERM_RELEASE = "admin:killswitch:release";
const PERM_APPROVE = "admin:approval:decide";

type DialogApi = ReturnType<typeof useDialog>;
type ApiClientLike = ReturnType<typeof useAuth>["client"];

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

/** 错误码 → 弹窗：双人审批与前置审批必须显式说明规则。 */
function notifyCommandError(dialog: DialogApi, error: unknown, fallbackTitle: string): void {
  const details = apiErrorDetails(error);
  const code = error instanceof ApiError ? error.code : "";
  if (code === "TWO_PERSON_REQUIRED") {
    dialog.notify("warning", "双人审批规则", "请求人与审批人不能是同一人，请由另一位管理员审批该请求。", details);
    return;
  }
  if (code === "APPROVAL_REQUIRED") {
    dialog.notify("warning", "需要先完成审批", "该命令需要先由第二位管理员审批通过后才能执行。", details);
    return;
  }
  if (code === "CONFLICT_STATE") {
    dialog.notify("warning", "模式与命令不匹配", "当前请求的模式与要执行的命令不一致，请核对后重试。", details);
    return;
  }
  dialog.notify("error", fallbackTitle, error instanceof ApiError ? error.userMessage : "请稍后重试", details);
}

async function postCommand(
  client: ApiClientLike,
  dialog: DialogApi,
  path: string,
  body: Record<string, unknown>,
  options: {
    successTitle?: string;
    successDetails?: Array<{ label: string; value: ReactNode }>;
    failTitle?: string;
    onDone?: (data: Record<string, unknown>) => void;
  } = {},
): Promise<Record<string, unknown> | null> {
  try {
    const response = await client.post<Record<string, unknown>>(path, body, { idempotencyKey: newIdempotencyKey() });
    const data = (response.data ?? {}) as Record<string, unknown>;
    dialog.notify("success", options.successTitle ?? "操作已完成", null, options.successDetails);
    options.onDone?.(data);
    return data;
  } catch (error) {
    notifyCommandError(dialog, error, options.failTitle ?? "操作失败");
    return null;
  }
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

async function askNote(dialog: DialogApi, action: "approve" | "reject"): Promise<string | null> {
  const raw = await dialog.prompt({
    kind: "info",
    title: action === "approve" ? "填写审批意见" : "填写驳回原因",
    body: "意见会记录在审批单上，可留空。",
    input: {
      label: "审批意见",
      placeholder: action === "approve" ? "例如：已核对上游故障，同意启用" : "例如：影响面评估不足，暂不启用",
      pattern: /^.{0,500}$/,
      patternHint: "最多 500 个字符",
    },
    submitLabel: action === "approve" ? "通过审批" : "驳回请求",
  });
  if (!raw) return null;
  return raw.startsWith("input:") ? raw.slice("input:".length).trim() : raw;
}

function num(value: unknown): number {
  const parsed = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(parsed) ? parsed : 0;
}

function shortId(value: string | undefined): string {
  if (!value) return "—";
  return value.length > 18 ? value.slice(0, 10) + "…" + value.slice(-6) : value;
}

function yesNo(value: unknown): string {
  return value === true ? "是" : "否";
}

function text(value: unknown): string {
  return typeof value === "string" && value !== "" ? value : "—";
}

function asStringList(value: unknown): string {
  if (!Array.isArray(value) || value.length === 0) return "—";
  return value.map((item) => String(item)).join(" / ");
}

interface Engagement {
  id: string;
  mode: string;
  status: string;
  reason: string;
  scope?: Record<string, unknown>;
  impact_preview?: Record<string, unknown>;
  approval_id?: string;
  approval_status?: string;
  requested_by: string;
  requested_by_email: string;
  approved_by?: string;
  approved_by_email?: string;
  step_up_at?: string | null;
  step_up_method?: string;
  engaged_at?: string | null;
  restored_at?: string | null;
  health_check?: Record<string, unknown>;
  break_glass?: boolean;
  created_at: string;
  can_approve?: boolean;
}

interface ApprovalRow {
  id: string;
  operation?: string;
  target_type?: string;
  target_id?: string;
  status?: string;
  payload?: Record<string, unknown>;
  requested_by?: string;
  requested_by_email?: string;
  requested_at?: string;
  decided_by?: string;
  decided_by_email?: string;
  decided_at?: string;
  decision_note?: string;
  executed_at?: string;
  expires_at?: string;
  result?: unknown;
  requester_can_approve?: boolean;
}

interface KillSwitchPreview {
  mode?: string;
  scope?: Record<string, unknown>;
  impact?: Record<string, unknown>;
  requires?: string[];
  step_up_method?: string;
  approval_ttl_seconds?: number;
  data_source?: string;
}

interface ProbeHealth {
  checked_at?: string;
  checks?: Record<string, string>;
  healthy?: boolean;
  open_alerts?: number;
}

interface KillSwitchHealthResponse {
  health?: ProbeHealth;
  active_engagement?: Engagement | null;
}

const PROBE_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  clickhouse: "ClickHouse",
  nats: "NATS",
  minio: "MinIO",
};

const MODE_LABELS: Record<string, string> = { engage: "启用 Kill Switch", restore: "恢复（解除 Kill Switch）" };

function modeLabel(mode: string | undefined): string {
  return MODE_LABELS[mode ?? ""] ?? (mode || "—");
}

function statusTone(status: string): "ok" | "warn" | "danger" | "info" | "neutral" {
  switch (status) {
    case "active":
      return "danger";
    case "pending_approval":
      return "warn";
    case "approved":
      return "info";
    case "restored":
      return "ok";
    case "rejected":
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

function approvalTone(status: string | undefined): "ok" | "warn" | "danger" | "info" | "neutral" {
  switch (status) {
    case "pending":
      return "warn";
    case "approved":
      return "info";
    case "executed":
      return "ok";
    case "rejected":
    case "failed":
      return "danger";
    default:
      return "neutral";
  }
}

function checkTone(value: string | undefined): "ok" | "warn" | "danger" | "neutral" {
  if (value === "ok") return "ok";
  if (value === "degraded") return "warn";
  if (!value) return "neutral";
  return "danger";
}

function scopeItems(scope: Record<string, unknown> | undefined): Array<{ label: string; value: ReactNode }> {
  const data = scope ?? {};
  return [
    { label: "停止 API 接入", value: yesNo(data.apis) },
    { label: "停止任务派发", value: yesNo(data.jobs) },
    { label: "暂停 Worker 轮询", value: yesNo(data.worker_poll) },
    { label: "阻断 Actions / MCP", value: yesNo(data.actions_mcp) },
    { label: "删除任务", value: yesNo(data.delete_jobs) },
    { label: "清空队列", value: yesNo(data.purge_queue) },
    { label: "丢弃 Checkpoint", value: yesNo(data.drop_checkpoints) },
  ];
}

function impactItems(impact: Record<string, unknown> | undefined): Array<{ label: string; value: ReactNode }> {
  const data = impact ?? {};
  return [
    { label: "运行中任务", value: fmtNumber(num(data.running_jobs)) },
    { label: "等待中任务", value: fmtNumber(num(data.waiting_jobs)) },
    { label: "已暂停任务", value: fmtNumber(num(data.paused_jobs)) },
    { label: "运行中数据集任务", value: fmtNumber(num(data.running_dataset_jobs)) },
    { label: "运行中分片", value: fmtNumber(num(data.running_shards)) },
    { label: "将停止的 API", value: asStringList(data.stopped_apis) },
    { label: "保持可用的 API", value: asStringList(data.kept_apis) },
    { label: "暂停 Worker 轮询", value: yesNo(data.paused_workers) },
    { label: "阻断 Actions / MCP", value: yesNo(data.actions_mcp_blocked) },
    { label: "删除任务", value: yesNo(data.deletes_jobs) },
    { label: "清空队列", value: yesNo(data.purges_queue) },
    { label: "丢弃 Checkpoint", value: yesNo(data.drops_checkpoints) },
    { label: "保留 Checkpoint", value: yesNo(data.keeps_checkpoints) },
  ];
}

export default function KillSwitchPage() {
  const { client, can, session } = useAuth();
  const dialog = useDialog();
  const command = useAdminCommand();
  const health = useAdminResource<KillSwitchHealthResponse>("/api/admin/v1/control/kill-switch/health");
  const requests = useAdminList<Engagement>("/api/admin/v1/control/kill-switch/requests", {}, 20);
  const approvals = useAdminList<ApprovalRow>("/api/admin/v1/approvals", {}, 20);

  const [mode, setMode] = useState("engage");
  const [preview, setPreview] = useState<KillSwitchPreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [lastHealth, setLastHealth] = useState<ProbeHealth | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [approvalOperation, setApprovalOperation] = useState("");

  const items = requests.items;
  useEffect(() => {
    if (items.length === 0) {
      setSelectedId(null);
      return;
    }
    setSelectedId((current) => (current && items.some((row) => row.id === current) ? current : items[0].id));
  }, [items]);

  const selected = useMemo(
    () => items.find((row) => row.id === selectedId) ?? items[0] ?? null,
    [items, selectedId],
  );

  const pendingRows = items.filter((row) => row.status === "pending_approval");
  const actionableRows = items.filter((row) => row.status === "approved" || row.status === "active");
  const activeEngagement = health.data?.active_engagement ?? null;
  const checks = health.data?.health?.checks ?? {};
  const selfId = session?.user?.id ?? "";

  const reloadAll = useCallback(() => {
    health.reload();
    requests.reload();
    approvals.reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [health.reload, requests.reload, approvals.reload]);

  const showCaliber = () => {
    dialog.notify("info", "Kill Switch 口径", null, [
      { label: "影响预览来源", value: preview?.data_source ?? "postgres:download_jobs/dataset_jobs/shards" },
      { label: "必填字段", value: (preview?.requires ?? ["step_up_password", "second_approver"]).join(" / ") },
      { label: "二次确认方式", value: preview?.step_up_method ?? "password" },
      { label: "审批有效期（秒）", value: fmtNumber(preview?.approval_ttl_seconds ?? 1800) },
      { label: "审批规则", value: "请求人与审批人不能是同一人（TWO_PERSON_REQUIRED）" },
      { label: "执行前置条件", value: "审批通过后才能执行，否则返回 412 APPROVAL_REQUIRED" },
      { label: "健康探针", value: "postgres / clickhouse / nats / minio" },
      { label: "任务与队列", value: "只停止新的拉取与派发，不删除任务、不清空队列、不丢弃 Checkpoint" },
      { label: "预览中的 note 字段", value: text(preview?.impact?.note) },
      { label: "探针时间", value: fmtDateTime(health.data?.health?.checked_at) },
    ]);
  };

  const runPreview = async (nextMode: string) => {
    if (!can(PERM_ENGAGE)) {
      warnNoPermission(dialog, PERM_ENGAGE);
      return;
    }
    setPreviewLoading(true);
    try {
      const response = await client.post<KillSwitchPreview>("/api/admin/v1/control/kill-switch/preview", {
        mode: nextMode,
      });
      const data = response.data ?? null;
      setPreview(data);
      setMode(nextMode);
      dialog.notify(
        data?.mode === "restore" ? "info" : "warning",
        "影响预览（未执行任何变更）",
        data?.mode === "restore" ? "恢复后按原游标继续派发。" : "启用后新的拉取与派发会停止。",
        [
          { label: "模式", value: modeLabel(data?.mode) },
          ...impactItems(data?.impact),
          { label: "接口说明", value: text(data?.impact?.note) },
          { label: "数据来源", value: text(data?.data_source) },
        ],
      );
    } catch (error) {
      notifyCommandError(dialog, error, "影响预览失败");
    } finally {
      setPreviewLoading(false);
    }
  };

  const submitRequest = async () => {
    if (!can(PERM_ENGAGE)) {
      warnNoPermission(dialog, PERM_ENGAGE);
      return;
    }
    await command.run({
      path: "/api/admin/v1/control/kill-switch/requests",
      body: { mode },
      requireStepUp: true,
      stepUpLabel: "当前账号密码",
      variant: mode === "engage" ? "danger" : "primary",
      confirmTitle: mode === "engage" ? "提交 Kill Switch 启用请求？" : "提交 Kill Switch 恢复请求？",
      confirmBody: "提交后需要第二位管理员审批，审批通过后才能执行；本步骤不会立即改变系统状态。",
      confirmLabel: "提交请求",
      details: [
        { label: "模式", value: modeLabel(mode) },
        ...impactItems(preview?.impact),
      ],
      successTitle: "请求已提交，等待第二位管理员审批",
      successDetails: (data) => {
        const engagement = (data.engagement ?? {}) as Engagement;
        return [
          { label: "请求 ID", value: shortId(String(engagement.id ?? "—")) },
          { label: "状态", value: String(engagement.status ?? "—") },
          { label: "审批单 ID", value: shortId(String(engagement.approval_id ?? "—")) },
          { label: "审批规则", value: "请求人与审批人不能是同一人" },
        ];
      },
      onDone: () => reloadAll(),
    });
  };

  const decide = async (row: Engagement, action: "approve" | "reject") => {
    if (!can(PERM_APPROVE)) {
      warnNoPermission(dialog, PERM_APPROVE);
      return;
    }
    if (row.requested_by === selfId) {
      showTwoPersonRule(row);
      return;
    }
    const note = await askNote(dialog, action);
    if (note === null) return;
    await postCommand(client, dialog, "/api/admin/v1/control/kill-switch/requests/" + row.id + "/" + action, { note }, {
      successTitle: action === "approve" ? "审批已通过" : "请求已驳回",
      successDetails: [
        { label: "请求 ID", value: shortId(row.id) },
        { label: "模式", value: modeLabel(row.mode) },
      ],
      failTitle: "审批失败",
      onDone: () => reloadAll(),
    });
  };

  const showTwoPersonRule = (row: Engagement) => {
    dialog.notify(
      "warning",
      "双人审批规则",
      "请求人与审批人不能是同一人。该请求由当前账号提交，必须由另一位持有 admin:approval:decide 的管理员审批。",
      [
        { label: "请求 ID", value: shortId(row.id) },
        { label: "请求人", value: row.requested_by_email || "—" },
        { label: "当前账号", value: session?.email ?? "—" },
        { label: "错误码", value: "TWO_PERSON_REQUIRED" },
      ],
    );
  };

  const runCommand = async (row: Engagement, action: "execute" | "restore") => {
    const permission = action === "execute" ? PERM_ENGAGE : PERM_RELEASE;
    if (!can(permission)) {
      warnNoPermission(dialog, permission);
      return;
    }
    const password = await askStepUpPassword(dialog);
    if (!password) return;
    const data = await postCommand(
      client,
      dialog,
      "/api/admin/v1/control/kill-switch/requests/" + row.id + "/" + action,
      { step_up_password: password },
      {
        successTitle: action === "execute" ? "Kill Switch 已启用" : "系统已恢复",
        successDetails: [
          { label: "请求 ID", value: shortId(row.id) },
          { label: "模式", value: modeLabel(row.mode) },
        ],
        failTitle: action === "execute" ? "执行失败" : "恢复失败",
        onDone: () => reloadAll(),
      },
    );
    if (!data) return;
    const healthResult = (data.health ?? null) as ProbeHealth | null;
    setLastHealth(healthResult);
    dialog.notify(
      healthResult?.healthy === false ? "warning" : "success",
      "健康检查结果",
      null,
      healthResult ? healthDetails(healthResult) : [{ label: "结果", value: "未返回健康检查" }],
    );
  };

  const showDetail = async (row: Engagement) => {
    try {
      const response = await client.get<Engagement>("/api/admin/v1/control/kill-switch/requests/" + row.id);
      const detail = response.data ?? row;
      await dialog.open({
        kind: "info",
        title: "Kill Switch 请求详情",
        details: [
          { label: "请求 ID", value: <span className="mono">{detail.id}</span> },
          { label: "模式", value: modeLabel(detail.mode) },
          { label: "状态", value: <StatusBadge tone={statusTone(detail.status)}>{detail.status}</StatusBadge> },
          { label: "原因", value: detail.reason || "—" },
          { label: "请求人", value: detail.requested_by_email || "—" },
          { label: "审批人", value: detail.approved_by_email || "—" },
          { label: "审批单", value: <span className="mono">{detail.approval_id || "—"}</span> },
          { label: "审批状态", value: detail.approval_status || "—" },
          { label: "二次确认时间", value: fmtDateTime(detail.step_up_at) },
          { label: "二次确认方式", value: detail.step_up_method || "—" },
          { label: "执行时间", value: fmtDateTime(detail.engaged_at) },
          { label: "恢复时间", value: fmtDateTime(detail.restored_at) },
          { label: "紧急通道", value: detail.break_glass ? "是" : "否" },
          { label: "提交时间", value: fmtDateTime(detail.created_at) },
        ],
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
    } catch (error) {
      notifyCommandError(dialog, error, "读取请求详情失败");
    }
  };

  const showApprovalDetail = (row: ApprovalRow) => {
    void dialog.open({
      kind: "info",
      title: "审批单详情",
      details: [
        { label: "审批单 ID", value: <span className="mono">{row.id}</span> },
        { label: "操作", value: row.operation ?? "—" },
        { label: "对象类型", value: row.target_type ?? "—" },
        { label: "对象 ID", value: <span className="mono">{row.target_id || "—"}</span> },
        { label: "状态", value: <StatusBadge tone={approvalTone(row.status)}>{row.status ?? "—"}</StatusBadge> },
        { label: "请求人", value: row.requested_by_email || "—" },
        { label: "提交时间", value: fmtDateTime(row.requested_at) },
        { label: "审批人", value: row.decided_by_email || "—" },
        { label: "审批时间", value: fmtDateTime(row.decided_at) },
        { label: "审批意见", value: row.decision_note || "—" },
        { label: "执行时间", value: fmtDateTime(row.executed_at) },
        { label: "有效期至", value: fmtDateTime(row.expires_at) },
        { label: "本人可审批", value: row.requester_can_approve ? "否（请求人与审批人不能是同一人）" : "—" },
        { label: "payload", value: <JsonBlock value={row.payload} label="payload" /> },
        { label: "执行结果", value: <JsonBlock value={row.result} label="result" /> },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  const timelineFor = (row: Engagement): TimelineEntry[] => {
    const entries: TimelineEntry[] = [
      {
        id: row.id + "-created",
        title: "① 提交请求 · " + modeLabel(row.mode),
        meta: fmtDateTime(row.created_at),
        tone: "info",
        body: "请求人 " + (row.requested_by_email || "—") + " · 原因：" + (row.reason || "—"),
      },
    ];
    if (row.step_up_at) {
      entries.push({
        id: row.id + "-stepup",
        title: "② 二次身份确认",
        meta: fmtDateTime(row.step_up_at),
        tone: "ok",
        body: "方式 " + (row.step_up_method || "password"),
      });
    }
    if (row.approval_id) {
      entries.push({
        id: row.id + "-approval",
        title: "③ 审批 · " + (row.approval_status || row.status),
        meta: row.approved_by_email || "等待第二位管理员",
        tone: row.approval_status === "approved" ? "ok" : row.approval_status === "rejected" ? "danger" : "warn",
        body: "审批单 " + shortId(row.approval_id),
      });
    }
    if (row.engaged_at) {
      entries.push({
        id: row.id + "-engaged",
        title: "④ 已执行",
        meta: fmtDateTime(row.engaged_at),
        tone: "danger",
      });
    }
    if (row.restored_at) {
      entries.push({
        id: row.id + "-restored",
        title: "④ 已恢复",
        meta: fmtDateTime(row.restored_at),
        tone: "ok",
      });
    }
    if (row.status === "rejected" || row.status === "failed" || row.status === "cancelled") {
      entries.push({
        id: row.id + "-terminal",
        title: "流程终止 · " + row.status,
        meta: fmtDateTime(row.created_at),
        tone: row.status === "failed" ? "danger" : "neutral",
      });
    }
    return entries;
  };

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="Kill Switch"
          subtitle={
            <>
              读取 {PERM_READ} · 当前状态 <span className="mono">{activeEngagement ? "active" : "未启用"}</span>
            </>
          }
          actions={
            <>
              <button type="button" className="btn btn--sm" onClick={showCaliber}>
                口径
              </button>
              <button type="button" className="btn btn--sm" onClick={reloadAll}>
                刷新
              </button>
            </>
          }
        />

        {health.loading && !health.data ? (
          <SkeletonRows rows={3} columns={4} />
        ) : health.error && !health.data ? (
          <Card>
            <ErrorState title="健康检查加载失败" message={health.error} onRetry={health.reload} />
          </Card>
        ) : (
          <>
            <Card title="系统健康轨道">
              <div className="toolbar">
                {Object.keys(PROBE_LABELS).map((key) => (
                  <span className="toolbar" key={key} aria-label={PROBE_LABELS[key] + " 探针状态"}>
                    <StatusBadge tone={checkTone(checks[key])}>
                      {PROBE_LABELS[key] + " " + (checks[key] ?? "未探测")}
                    </StatusBadge>
                  </span>
                ))}
                <span className="shell__spacer" />
                <StatusBadge tone={health.data?.health?.healthy ? "ok" : "warn"}>
                  {health.data?.health?.healthy ? "全部探针正常" : "存在异常探针"}
                </StatusBadge>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"探针时间 " + fmtDateTime(health.data?.health?.checked_at)}
                </span>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"未处理告警 " + fmtNumber(health.data?.health?.open_alerts ?? 0)}
                </span>
              </div>
              <div className="metric-grid" style={{ marginTop: "var(--sp-4)" }}>
                <Metric
                  label="Kill Switch 状态"
                  value={activeEngagement ? "已启用" : "未启用"}
                  tone={activeEngagement ? "danger" : "ok"}
                  hint={activeEngagement ? modeLabel(activeEngagement.mode) : "无 active engagement"}
                />
                <Metric label="待审批请求" value={fmtNumber(pendingRows.length)} tone={pendingRows.length > 0 ? "warn" : "ok"} hint="status = pending_approval" />
                <Metric label="本页请求数" value={fmtNumber(items.length)} tone="neutral" hint="service cursor 分页" />
                <Metric
                  label="最近一次健康检查"
                  value={lastHealth?.healthy === false ? "异常" : lastHealth ? "正常" : "—"}
                  tone={lastHealth?.healthy === false ? "danger" : lastHealth ? "ok" : "neutral"}
                  hint={lastHealth ? "来自 execute / restore 响应" : "执行或恢复后显示"}
                />
              </div>
            </Card>

            <Card title="① 影响预览">
              <div className="toolbar">
                <button
                  type="button"
                  className="btn btn--sm"
                  disabled={!can(PERM_ENGAGE) || previewLoading}
                  onClick={() => void runPreview("engage")}
                >
                  预览启用影响
                </button>
                <button
                  type="button"
                  className="btn btn--sm"
                  disabled={!can(PERM_ENGAGE) || previewLoading}
                  onClick={() => void runPreview("restore")}
                >
                  预览恢复影响
                </button>
                <span className="shell__spacer" />
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {preview ? "预览模式 " + modeLabel(preview.mode) + " · 数据来源 " + (preview.data_source ?? "—") : "尚未预览"}
                </span>
              </div>
              <div style={{ marginTop: "var(--sp-4)" }}>
                <KeyValueList
                  items={preview ? [...scopeItems(preview.scope), ...impactItems(preview.impact)] : impactItems(undefined).map((item) => ({ ...item, value: "—" }))}
                />
              </div>
            </Card>

            <Card title="② 提交请求">
              <div className="toolbar" style={{ alignItems: "flex-end" }}>
                <SelectInput
                  id="kill-switch-mode"
                  label="模式"
                  value={mode}
                  onChange={setMode}
                  options={[
                    { value: "engage", label: "启用 Kill Switch" },
                    { value: "restore", label: "恢复（解除 Kill Switch）" },
                  ]}
                />
                <button
                  type="button"
                  className="btn btn--primary btn--sm"
                  style={{ alignSelf: "flex-end" }}
                  disabled={!can(PERM_ENGAGE) || command.busy}
                  onClick={() => void submitRequest()}
                >
                  {command.busy ? "提交中…" : "提交请求"}
                </button>
              </div>
              <div style={{ marginTop: "var(--sp-4)" }}>
                <KeyValueList
                  items={[
                    { label: "拟提交模式", value: modeLabel(mode) },
                    { label: "状态影响范围 · 停止 API", value: asStringList(preview?.impact?.stopped_apis) },
                    { label: "状态影响范围 · 保留 API", value: asStringList(preview?.impact?.kept_apis) },
                    { label: "运行中任务", value: fmtNumber(num(preview?.impact?.running_jobs)) },
                    { label: "运行中数据集任务", value: fmtNumber(num(preview?.impact?.running_dataset_jobs)) },
                    { label: "运行中分片", value: fmtNumber(num(preview?.impact?.running_shards)) },
                    { label: "删除任务", value: yesNo(preview?.impact?.deletes_jobs) },
                    { label: "清空队列", value: yesNo(preview?.impact?.purges_queue) },
                    { label: "丢弃 Checkpoint", value: yesNo(preview?.impact?.drops_checkpoints) },
                    { label: "保留 Checkpoint", value: yesNo(preview?.impact?.keeps_checkpoints) },
                    { label: "需要", value: (preview?.requires ?? ["step_up_password", "second_approver"]).join(" / ") },
                    { label: "审批有效期（秒）", value: fmtNumber(preview?.approval_ttl_seconds ?? 1800) },
                  ]}
                />
              </div>
            </Card>

            <Card title={"③ 审批（待审批 " + pendingRows.length + "）"} flush>
              <DataTable<Engagement>
                loading={requests.loading}
                error={requests.error}
                onRetry={requests.reload}
                rows={pendingRows}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有待审批的 Kill Switch 请求" />}
                columns={[
                  {
                    key: "id",
                    header: "请求",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "mode", header: "模式", width: "150px", render: (row) => modeLabel(row.mode) },
                  { key: "reason", header: "原因", render: (row) => row.reason || "—" },
                  { key: "requester", header: "请求人", width: "200px", render: (row) => row.requested_by_email || "—" },
                  { key: "created", header: "提交时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  {
                    key: "self",
                    header: "双人规则",
                    width: "200px",
                    render: (row) =>
                      row.requested_by === selfId ? (
                        <button type="button" className="btn btn--sm" onClick={() => showTwoPersonRule(row)}>
                          本人提交 · 查看规则
                        </button>
                      ) : (
                        <StatusBadge tone="ok">可由当前账号审批</StatusBadge>
                      ),
                  },
                  {
                    key: "actions",
                    header: "操作",
                    width: "190px",
                    align: "right",
                    render: (row) => (
                      <>
                        <button
                          type="button"
                          className="btn btn--sm"
                          disabled={row.requested_by === selfId || !can(PERM_APPROVE)}
                          onClick={() => void decide(row, "approve")}
                          style={{ marginRight: "var(--sp-2)" }}
                        >
                          通过
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          disabled={row.requested_by === selfId || !can(PERM_APPROVE)}
                          onClick={() => void decide(row, "reject")}
                        >
                          驳回
                        </button>
                      </>
                    ),
                  },
                ]}
              />
            </Card>

            <Card title="④ 执行与恢复" flush>
              <DataTable<Engagement>
                loading={requests.loading}
                error={requests.error}
                onRetry={requests.reload}
                rows={actionableRows}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有已审批或执行中的请求" />}
                columns={[
                  {
                    key: "id",
                    header: "请求",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "mode", header: "模式", width: "150px", render: (row) => modeLabel(row.mode) },
                  {
                    key: "status",
                    header: "状态",
                    width: "140px",
                    render: (row) => <StatusBadge tone={statusTone(row.status)}>{row.status}</StatusBadge>,
                  },
                  {
                    key: "approval",
                    header: "审批",
                    width: "170px",
                    render: (row) => (row.approved_by_email || row.approval_status || "—"),
                  },
                  { key: "engaged", header: "执行时间", width: "170px", render: (row) => fmtDateTime(row.engaged_at) },
                  { key: "restored", header: "恢复时间", width: "170px", render: (row) => fmtDateTime(row.restored_at) },
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
                          disabled={row.status !== "approved" || !can(PERM_ENGAGE)}
                          onClick={() => void runCommand(row, "execute")}
                        >
                          执行
                        </button>
                        <button
                          type="button"
                          className="btn btn--sm"
                          disabled={row.status !== "active" || !can(PERM_RELEASE)}
                          onClick={() => void runCommand(row, "restore")}
                        >
                          恢复
                        </button>
                      </>
                    ),
                  },
                ]}
              />
            </Card>

            <Card title={"审批中心（本页 " + approvals.items.length + "）"} flush>
              <div className="toolbar" style={{ padding: "var(--sp-4) var(--sp-4) 0", alignItems: "flex-end" }}>
                <SelectInput
                  id="approval-operation"
                  label="操作类型"
                  value={approvalOperation}
                  onChange={(value) => {
                    setApprovalOperation(value);
                    approvals.setFilter({ operation: value === "" ? undefined : value });
                  }}
                  options={[
                    { value: "", label: "全部" },
                    { value: "system.kill_switch", label: "system.kill_switch" },
                    { value: "system.restore", label: "system.restore" },
                    { value: "data.deletion", label: "data.deletion" },
                  ]}
                />
              </div>
              <DataTable<ApprovalRow>
                loading={approvals.loading}
                error={approvals.error}
                onRetry={approvals.reload}
                rows={approvals.items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有审批单" />}
                columns={[
                  {
                    key: "id",
                    header: "审批单",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "operation", header: "操作", width: "190px", render: (row) => <span className="mono">{row.operation ?? "—"}</span> },
                  {
                    key: "target",
                    header: "对象",
                    width: "190px",
                    render: (row) => (
                      <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                        {(row.target_type ?? "—") + " " + shortId(row.target_id)}
                      </span>
                    ),
                  },
                  {
                    key: "status",
                    header: "状态",
                    width: "130px",
                    render: (row) => <StatusBadge tone={approvalTone(row.status)}>{row.status ?? "—"}</StatusBadge>,
                  },
                  { key: "requester", header: "请求人", width: "190px", render: (row) => row.requested_by_email || "—" },
                  { key: "requested", header: "提交时间", width: "170px", render: (row) => fmtDateTime(row.requested_at) },
                  { key: "decider", header: "审批人", width: "190px", render: (row) => row.decided_by_email || "—" },
                  { key: "decided", header: "审批时间", width: "170px", render: (row) => fmtDateTime(row.decided_at) },
                  {
                    key: "self",
                    header: "本人可审批",
                    width: "130px",
                    render: (row) => <StatusBadge tone={row.requester_can_approve ? "warn" : "neutral"}>{row.requester_can_approve ? "否（双人规则）" : "—"}</StatusBadge>,
                  },
                  { key: "expires", header: "有效期至", width: "170px", render: (row) => fmtDateTime(row.expires_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "90px",
                    align: "right",
                    render: (row) => (
                      <button type="button" className="btn btn--sm" onClick={() => showApprovalDetail(row)}>
                        详情
                      </button>
                    ),
                  },
                ]}
              />
            </Card>

            <Card title={"全流程状态时间线 · " + (selected ? shortId(selected.id) : "—")} actions={
              <select
                className="select"
                aria-label="选择请求"
                value={selectedId ?? ""}
                onChange={(event) => setSelectedId(event.target.value)}
              >
                {items.map((row) => (
                  <option key={row.id} value={row.id}>
                    {shortId(row.id) + " · " + modeLabel(row.mode) + " · " + row.status}
                  </option>
                ))}
              </select>
            }>
              <Timeline
                entries={selected ? timelineFor(selected) : []}
                emptyLabel="没有可展示的请求流程"
              />
            </Card>

            <Card title={"全部请求（本页 " + items.length + "）"} flush>
              <DataTable<Engagement>
                loading={requests.loading}
                error={requests.error}
                onRetry={requests.reload}
                rows={items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有 Kill Switch 请求" />}
                columns={[
                  {
                    key: "id",
                    header: "请求",
                    width: "170px",
                    render: (row) => <span className="mono" title={row.id}>{shortId(row.id)}</span>,
                  },
                  { key: "mode", header: "模式", width: "150px", render: (row) => modeLabel(row.mode) },
                  {
                    key: "status",
                    header: "状态",
                    width: "150px",
                    render: (row) => <StatusBadge tone={statusTone(row.status)}>{row.status}</StatusBadge>,
                  },
                  { key: "requester", header: "请求人", width: "190px", render: (row) => row.requested_by_email || "—" },
                  { key: "approver", header: "审批人", width: "190px", render: (row) => row.approved_by_email || "—" },
                  { key: "created", header: "提交时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  {
                    key: "actions",
                    header: "操作",
                    width: "170px",
                    align: "right",
                    render: (row) => (
                      <>
                        <button type="button" className="btn btn--sm" onClick={() => void showDetail(row)} style={{ marginRight: "var(--sp-2)" }}>
                          详情
                        </button>
                        <button type="button" className="btn btn--sm" onClick={() => setSelectedId(row.id)}>
                          时间线
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
                  <button type="button" className="btn btn--sm" disabled={!requests.hasPrevious || requests.loading} onClick={requests.goPrevious}>
                    上一页
                  </button>
                  <button type="button" className="btn btn--sm" disabled={!requests.nextCursor || requests.loading} onClick={requests.goNext}>
                    下一页
                  </button>
                </span>
              </div>
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="流程与证据"
        items={[
          { label: "当前会话", value: session?.email ?? "—" },
          { label: "审批权限", value: can(PERM_APPROVE) ? "已授予" : "未授予" },
          { label: "启用权限", value: can(PERM_ENGAGE) ? "已授予" : "未授予" },
          { label: "恢复权限", value: can(PERM_RELEASE) ? "已授予" : "未授予" },
          { label: "双人规则", value: "请求人 ≠ 审批人" },
          { label: "审批有效期", value: fmtNumber(preview?.approval_ttl_seconds ?? 1800) + " 秒" },
          { label: "二次确认方式", value: preview?.step_up_method ?? "password" },
          { label: "活动请求", value: activeEngagement ? shortId(activeEngagement.id) : "无" },
          { label: "最后一次健康检查", value: fmtDateTime(lastHealth?.checked_at) },
        ]}
      />
    </div>
  );
}

function healthDetails(health: ProbeHealth): Array<{ label: string; value: ReactNode }> {
  const checks = health.checks ?? {};
  const details: Array<{ label: string; value: ReactNode }> = [
    { label: "检查时间", value: fmtDateTime(health.checked_at) },
    { label: "整体结论", value: health.healthy ? "正常" : "存在异常" },
  ];
  Object.entries(checks).forEach(([key, value]) => {
    details.push({ label: PROBE_LABELS[key] ?? key, value });
  });
  details.push({ label: "未处理告警", value: fmtNumber(health.open_alerts ?? 0) });
  return details;
}
