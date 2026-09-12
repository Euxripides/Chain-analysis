// A-CONTROL-01 实时控制面（路由 /control · 权限 admin:killswitch:read）。
//
// 约束（AGENTS.md / docs/code-standards.md / AD-009）：
//   页面正文只承载业务数据、字段标签、导航与操作控件；
//   任何说明 / 警告 / 成功 / 错误 / 风险 / 确认提示一律通过 useDialog() 弹窗呈现。

import { useCallback, useEffect, useState, type ReactNode } from "react";
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
import { JsonBlock, KeyValueList, NumberInput } from "../components/s6";
import {
  fmtDateTime,
  fmtMillis,
  fmtNumber,
  fmtPercent,
  useAdminList,
  useAdminPatch,
  useAdminResource,
} from "../lib/adminApi";

const PERM_READ = "admin:killswitch:read";
const PERM_ENGAGE = "admin:killswitch:engage";

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

function extractInput(raw: string | null): string | null {
  if (!raw) return null;
  if (!raw.startsWith("input:")) return raw;
  const value = raw.slice("input:".length).trim();
  return value === "" ? null : value;
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
  return extractInput(raw);
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

function bool(value: unknown): boolean {
  return value === true || value === "true";
}

function shortId(value: string): string {
  if (!value) return "—";
  return value.length > 18 ? value.slice(0, 10) + "…" + value.slice(-6) : value;
}

async function postCommand(
  client: ApiClientLike,
  dialog: DialogApi,
  path: string,
  body: Record<string, unknown>,
  options: {
    method?: "post" | "patch";
    ifMatch?: string;
    successTitle?: string;
    successBody?: ReactNode;
    successDetails?: Array<{ label: string; value: ReactNode }>;
    failTitle?: string;
    onDone?: (data: Record<string, unknown>) => void;
  } = {},
): Promise<Record<string, unknown> | null> {
  try {
    const requestOptions = { idempotencyKey: newIdempotencyKey(), ifMatch: options.ifMatch };
    const response =
      options.method === "patch"
        ? await client.patch<Record<string, unknown>>(path, body, requestOptions)
        : await client.post<Record<string, unknown>>(path, body, requestOptions);
    const data = (response.data ?? {}) as Record<string, unknown>;
    dialog.notify("success", options.successTitle ?? "操作已完成", options.successBody ?? null, options.successDetails);
    options.onDone?.(data);
    return data;
  } catch (error) {
    dialog.notify(
      "error",
      options.failTitle ?? "操作失败",
      error instanceof ApiError ? error.userMessage : "请稍后重试",
      apiErrorDetails(error),
    );
    return null;
  }
}

interface ControlQueue {
  running?: unknown;
  waiting?: unknown;
  failed?: unknown;
  paused?: unknown;
}

interface ControlState {
  as_of?: string;
  state?: string;
  state_reason?: string;
  state_since?: string | null;
  automatic_protection?: boolean;
  kill_switch?: boolean;
  background_paused?: boolean;
  manual_mode?: string;
  manual_reason?: string;
  manual_until?: string | null;
  queue?: ControlQueue;
  circuits?: Record<string, { status?: string; capability_drift?: boolean }>;
  pending_approvals?: number;
  data_source?: Record<string, string>;
}

interface ProbeHealth {
  checked_at?: string;
  checks?: Record<string, string>;
  healthy?: boolean;
  open_alerts?: number;
}

interface KillSwitchHealthResponse {
  health?: ProbeHealth;
}

interface ControlPolicy {
  id?: string;
  automatic_protection?: boolean;
  policy_enabled?: boolean;
  warning_error_rate?: number;
  critical_error_rate?: number;
  warning_p95_ms?: number;
  critical_p95_ms?: number;
  cooldown_seconds?: number;
  updated_at?: string;
  data_source?: string;
}

interface ControlActionRow {
  id: string;
  action?: string;
  source?: string;
  from_state?: string;
  to_state?: string;
  reason?: string;
  detail?: unknown;
  actor?: string;
  created_at?: string;
}

interface PolicyForm {
  warning_error_rate: number;
  critical_error_rate: number;
  warning_p95_ms: number;
  critical_p95_ms: number;
  cooldown_seconds: number;
}

const PROBE_LABELS: Record<string, string> = {
  postgres: "PostgreSQL",
  clickhouse: "ClickHouse",
  nats: "NATS",
  minio: "MinIO",
};

function checkTone(value: string | undefined): "ok" | "warn" | "danger" | "neutral" {
  if (value === "ok") return "ok";
  if (value === "degraded") return "warn";
  if (!value) return "neutral";
  return "danger";
}

function stateTone(state: string | undefined): "ok" | "warn" | "danger" | "neutral" {
  const value = (state ?? "").toUpperCase();
  if (value === "NORMAL" || value === "OK" || value === "HEALTHY") return "ok";
  if (value === "DEGRADED" || value === "WARNING" || value === "PROTECTED") return "warn";
  if (value === "KILL_SWITCH" || value === "HALTED" || value === "CRITICAL") return "danger";
  return "neutral";
}

export default function ControlPage() {
  const { can } = useAuth();
  const dialog = useDialog();
  const state = useAdminResource<ControlState>("/api/admin/v1/control/state");
  const health = useAdminResource<KillSwitchHealthResponse>("/api/admin/v1/control/kill-switch/health");
  const policies = useAdminResource<ControlPolicy>("/api/admin/v1/control/policies");
  const actions = useAdminList<ControlActionRow>("/api/admin/v1/control/actions", {}, 50);
  const patch = useAdminPatch();

  const [form, setForm] = useState<PolicyForm>({
    warning_error_rate: 0,
    critical_error_rate: 0,
    warning_p95_ms: 0,
    critical_p95_ms: 0,
    cooldown_seconds: 0,
  });

  const loadedPolicy = policies.data;
  useEffect(() => {
    if (!loadedPolicy) return;
    setForm({
      warning_error_rate: num(loadedPolicy.warning_error_rate),
      critical_error_rate: num(loadedPolicy.critical_error_rate),
      warning_p95_ms: num(loadedPolicy.warning_p95_ms),
      critical_p95_ms: num(loadedPolicy.critical_p95_ms),
      cooldown_seconds: num(loadedPolicy.cooldown_seconds),
    });
  }, [loadedPolicy]);

  const reloadAll = useCallback(() => {
    state.reload();
    health.reload();
    policies.reload();
    actions.reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.reload, health.reload, policies.reload, actions.reload]);

  const showCaliber = () => {
    const sources = state.data?.data_source ?? {};
    dialog.notify("info", "控制面口径", null, [
      { label: "控制状态来源", value: sources.state ?? "postgres:api_control_settings" },
      { label: "队列来源", value: sources.queue ?? "postgres:download_jobs" },
      { label: "熔断来源", value: sources.circuits ?? "postgres:provider_descriptors" },
      { label: "审批来源", value: sources.approvals ?? "postgres:approval_requests" },
      { label: "策略来源", value: policies.data?.data_source ?? "postgres:api_control_policies" },
      { label: "动作历史来源", value: "postgres:api_control_actions（按 created_at 倒序，最多 200 条）" },
      { label: "状态轨探针", value: "GET /control/kill-switch/health → postgres / clickhouse / nats / minio" },
      { label: "控制面 API 节点", value: "本次 /control/* 读取成功即视为可达；不代表上游 Provider 可用" },
      { label: "状态取样时间", value: fmtDateTime(state.data?.as_of) },
      { label: "探针时间", value: fmtDateTime(health.data?.health?.checked_at) },
    ]);
  };

  const toggleAutomaticProtection = async () => {
    if (!can(PERM_ENGAGE)) {
      warnNoPermission(dialog, PERM_ENGAGE);
      return;
    }
    const current = state.data?.automatic_protection === true;
    const next = !current;
    const password = await askStepUpPassword(dialog);
    if (!password) return;
    await patch.run({
      path: "/api/admin/v1/control/policies",
      body: { automatic_protection: next, step_up_password: password },
      confirmTitle: next ? "启用自动保护？" : "停用自动保护？",
      confirmBody: next
        ? "启用后，错误率或延迟越过阈值时系统会按策略自动进入保护状态。"
        : "停用后，越过阈值不会再自动进入保护状态，只能由人工触发 Kill Switch。",
      confirmLabel: next ? "启用" : "停用",
      variant: next ? "primary" : "danger",
      details: [
        { label: "当前值", value: current ? "已启用" : "已停用" },
        { label: "目标值", value: next ? "已启用" : "已停用" },
        { label: "策略 ID", value: policies.data?.id ?? "—" },
      ],
      successTitle: "自动保护已更新",
      onDone: () => {
        state.reload();
        policies.reload();
      },
    });
  };

  const savePolicyThresholds = async () => {
    if (!can(PERM_ENGAGE)) {
      warnNoPermission(dialog, PERM_ENGAGE);
      return;
    }
    const password = await askStepUpPassword(dialog);
    if (!password) return;
    await patch.run({
      path: "/api/admin/v1/control/policies",
      body: {
        warning_error_rate: form.warning_error_rate,
        critical_error_rate: form.critical_error_rate,
        warning_p95_ms: form.warning_p95_ms,
        critical_p95_ms: form.critical_p95_ms,
        cooldown_seconds: form.cooldown_seconds,
        step_up_password: password,
      },
      confirmTitle: "保存控制策略阈值？",
      confirmBody: "阈值会立即影响自动保护的触发判定，变更原因写入审计日志。",
      details: [
        { label: "警告错误率", value: fmtPercent(form.warning_error_rate) },
        { label: "严重错误率", value: fmtPercent(form.critical_error_rate) },
        { label: "警告 P95", value: fmtMillis(form.warning_p95_ms) },
        { label: "严重 P95", value: fmtMillis(form.critical_p95_ms) },
        { label: "冷却时间", value: fmtNumber(form.cooldown_seconds) + " s" },
      ],
      successTitle: "控制策略已保存",
      onDone: () => {
        policies.reload();
        state.reload();
      },
    });
  };

  const showActionDetail = (row: ControlActionRow) => {
    void dialog.open({
      kind: "info",
      title: row.action || "控制动作",
      details: [
        { label: "动作 ID", value: <span className="mono">{row.id}</span> },
        { label: "来源", value: row.source || "—" },
        { label: "状态变化", value: (row.from_state || "—") + " → " + (row.to_state || "—") },
        { label: "原因", value: row.reason || "—" },
        { label: "操作者", value: row.actor || "—" },
        { label: "时间", value: fmtDateTime(row.created_at) },
        { label: "明细", value: <JsonBlock value={row.detail} label="原始 detail" /> },
      ],
      actions: [{ label: "关闭", variant: "primary", result: "ok" }],
    });
  };

  const queue = state.data?.queue ?? {};
  const checks = health.data?.health?.checks ?? {};
  const apiReachable = Boolean(health.data) && !health.error;
  const circuitRows = Object.entries(state.data?.circuits ?? {}).map(([provider, value]) => ({
    provider,
    status: (value?.status ?? "").toString() || "unknown",
    drift: Boolean(value?.capability_drift),
  }));

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader
          title="实时控制面"
          subtitle={
            <>
              读取 {PERM_READ} · 数据截至 <span className="mono">{fmtDateTime(state.data?.as_of)}</span>
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

        {state.loading && !state.data ? (
          <SkeletonRows rows={4} columns={4} />
        ) : state.error && !state.data ? (
          <Card>
            <ErrorState title="控制状态加载失败" message={state.error} onRetry={state.reload} />
          </Card>
        ) : (
          <>
            <Card title="状态轨">
              <div className="toolbar">
                <span className="toolbar" aria-label="控制面 API 状态">
                  <StatusBadge tone={apiReachable ? "ok" : "danger"}>
                    {"API " + (apiReachable ? "可达" : "不可达")}
                  </StatusBadge>
                </span>
                {Object.keys(PROBE_LABELS).map((key) => (
                  <span className="toolbar" key={key} aria-label={PROBE_LABELS[key] + " 探针状态"}>
                    <StatusBadge tone={checkTone(checks[key])}>
                      {PROBE_LABELS[key] + " " + (checks[key] ?? "未探测")}
                    </StatusBadge>
                  </span>
                ))}
                <span className="shell__spacer" />
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"探针时间 " + fmtDateTime(health.data?.health?.checked_at)}
                </span>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"未处理告警 " + fmtNumber(health.data?.health?.open_alerts ?? 0)}
                </span>
              </div>
            </Card>

            <Card
              title="控制状态"
              actions={
                <>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!can(PERM_ENGAGE) || patch.busy}
                    onClick={() => void toggleAutomaticProtection()}
                  >
                    {state.data?.automatic_protection ? "停用自动保护" : "启用自动保护"}
                  </button>
                  <StatusBadge tone={stateTone(state.data?.state)}>{state.data?.state ?? "未知"}</StatusBadge>
                </>
              }
            >
              <KeyValueList
                items={[
                  { label: "state", value: state.data?.state ?? "—", mono: true },
                  { label: "state_reason", value: state.data?.state_reason ?? "—" },
                  { label: "state_since", value: fmtDateTime(state.data?.state_since) },
                  { label: "automatic_protection", value: state.data?.automatic_protection ? "已启用" : "已停用" },
                  { label: "kill_switch", value: state.data?.kill_switch ? "已启用" : "未启用" },
                  { label: "background_paused", value: state.data?.background_paused ? "已暂停" : "未暂停" },
                  { label: "manual_mode", value: state.data?.manual_mode || "—", mono: true },
                  { label: "manual_reason", value: state.data?.manual_reason ?? "—" },
                  { label: "manual_until", value: fmtDateTime(state.data?.manual_until) },
                  { label: "策略 ID", value: policies.data?.id ?? "—", mono: true },
                  { label: "策略启用", value: policies.data?.policy_enabled ? "已启用" : "已停用" },
                  { label: "策略更新时间", value: fmtDateTime(policies.data?.updated_at) },
                ]}
              />
            </Card>

            <Card title="队列与审批">
              <div className="metric-grid">
                <Metric label="运行中任务" value={fmtNumber(num(queue.running))} tone={num(queue.running) > 0 ? "warn" : "neutral"} hint="RUNNING / RESUMING" />
                <Metric label="等待中任务" value={fmtNumber(num(queue.waiting))} tone={num(queue.waiting) > 0 ? "warn" : "neutral"} hint="CREATED / PLANNING / QUEUED / PAUSE_REQUESTED" />
                <Metric label="失败任务" value={fmtNumber(num(queue.failed))} tone={num(queue.failed) > 0 ? "danger" : "ok"} hint="FAILED" />
                <Metric label="暂停任务" value={fmtNumber(num(queue.paused))} tone={num(queue.paused) > 0 ? "warn" : "neutral"} hint="PAUSED" />
                <Metric
                  label="待审批"
                  value={fmtNumber(num(state.data?.pending_approvals))}
                  tone={num(state.data?.pending_approvals) > 0 ? "warn" : "ok"}
                  hint="approval_requests.status = pending"
                />
              </div>
            </Card>

            <Card title="控制策略阈值">
              <div className="toolbar" style={{ alignItems: "flex-end" }}>
                <NumberInput
                  id="control-warning-rate"
                  label="警告错误率（0-1）"
                  value={form.warning_error_rate}
                  min={0}
                  max={1}
                  step={0.001}
                  onChange={(value) => setForm((current) => ({ ...current, warning_error_rate: value }))}
                />
                <NumberInput
                  id="control-critical-rate"
                  label="严重错误率（0-1）"
                  value={form.critical_error_rate}
                  min={0}
                  max={1}
                  step={0.001}
                  onChange={(value) => setForm((current) => ({ ...current, critical_error_rate: value }))}
                />
                <NumberInput
                  id="control-warning-p95"
                  label="警告 P95（ms）"
                  value={form.warning_p95_ms}
                  min={0}
                  step={50}
                  onChange={(value) => setForm((current) => ({ ...current, warning_p95_ms: value }))}
                />
                <NumberInput
                  id="control-critical-p95"
                  label="严重 P95（ms）"
                  value={form.critical_p95_ms}
                  min={0}
                  step={50}
                  onChange={(value) => setForm((current) => ({ ...current, critical_p95_ms: value }))}
                />
                <NumberInput
                  id="control-cooldown"
                  label="冷却（秒）"
                  value={form.cooldown_seconds}
                  min={0}
                  step={30}
                  onChange={(value) => setForm((current) => ({ ...current, cooldown_seconds: value }))}
                />
                <button
                  type="button"
                  className="btn btn--primary btn--sm"
                  disabled={!can(PERM_ENGAGE) || patch.busy}
                  onClick={() => void savePolicyThresholds()}
                  style={{ alignSelf: "flex-end" }}
                >
                  {patch.busy ? "提交中…" : "保存阈值"}
                </button>
              </div>
            </Card>

            <Card title="Provider 熔断">
              <DataTable<{ provider: string; status: string; drift: boolean }>
                rows={circuitRows}
                rowKey={(row) => row.provider}
                empty={<EmptyState title="没有 Provider 熔断记录" />}
                columns={[
                  { key: "provider", header: "Provider", render: (row) => <span className="mono">{row.provider}</span> },
                  {
                    key: "status",
                    header: "熔断状态",
                    width: "180px",
                    render: (row) => <StatusBadge tone={statusBadgeTone(row.status)}>{row.status}</StatusBadge>,
                  },
                  {
                    key: "drift",
                    header: "能力漂移",
                    width: "140px",
                    render: (row) => (
                      <StatusBadge tone={row.drift ? "warn" : "ok"}>{row.drift ? "有" : "无"}</StatusBadge>
                    ),
                  },
                ]}
              />
            </Card>

            <Card title={"控制动作历史（本页 " + actions.items.length + "）"} flush>
              <DataTable<ControlActionRow>
                loading={actions.loading}
                error={actions.error}
                onRetry={actions.reload}
                rows={actions.items}
                rowKey={(row) => row.id}
                empty={<EmptyState title="没有控制动作记录" />}
                columns={[
                  { key: "created", header: "时间", width: "170px", render: (row) => fmtDateTime(row.created_at) },
                  { key: "action", header: "动作", render: (row) => <span className="mono">{row.action ?? "—"}</span> },
                  { key: "source", header: "来源", width: "120px", render: (row) => row.source ?? "—" },
                  {
                    key: "transition",
                    header: "状态变化",
                    width: "200px",
                    render: (row) => (
                      <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
                        {(row.from_state || "—") + " → " + (row.to_state || "—")}
                      </span>
                    ),
                  },
                  { key: "reason", header: "原因", width: "240px", render: (row) => row.reason || "—" },
                  { key: "actor", header: "操作者", width: "180px", render: (row) => row.actor || "—" },
                  {
                    key: "detail",
                    header: "明细",
                    width: "90px",
                    align: "right",
                    render: (row) => (
                      <button type="button" className="btn btn--sm" onClick={() => showActionDetail(row)}>
                        详情
                      </button>
                    ),
                  },
                ]}
              />
              <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", justifyContent: "space-between" }}>
                <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
                  {"服务端 limit 分页 · 本页 " + actions.items.length + " 条"}
                </span>
                <span className="toolbar">
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!actions.hasPrevious || actions.loading}
                    onClick={actions.goPrevious}
                  >
                    上一页
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!actions.nextCursor || actions.loading}
                    onClick={actions.goNext}
                  >
                    下一页
                  </button>
                </span>
              </div>
            </Card>
          </>
        )}
      </div>

      <EvidenceRail
        title="控制来源"
        items={[
          { label: "状态来源", value: state.data?.data_source?.state ?? "—" },
          { label: "队列来源", value: state.data?.data_source?.queue ?? "—" },
          { label: "熔断来源", value: state.data?.data_source?.circuits ?? "—" },
          { label: "审批来源", value: state.data?.data_source?.approvals ?? "—" },
          { label: "策略来源", value: policies.data?.data_source ?? "—" },
          { label: "动作历史", value: "postgres:api_control_actions" },
          { label: "策略 ID", value: policies.data?.id ?? "—" },
          { label: "控制面 API", value: apiReachable ? "本次读取成功" : "读取失败" },
        ]}
      />
    </div>
  );
}

function statusBadgeTone(value: string): "ok" | "warn" | "danger" | "neutral" {
  const upper = (value || "").toUpperCase();
  if (["OK", "HEALTHY", "CLOSED", "ACTIVE", "AVAILABLE"].includes(upper)) return "ok";
  if (["DEGRADED", "HALF_OPEN", "WARN", "WARNING"].includes(upper)) return "warn";
  if (["OPEN", "DOWN", "ERROR", "FAILED", "UNAVAILABLE"].includes(upper)) return "danger";
  return "neutral";
}
