import { type ReactNode } from "react";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  Card,
  ErrorState,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { KeyValueList } from "../components/s6";
import {
  fmtBytes,
  fmtDateTime,
  fmtNumber,
  useAdminCommand,
  useAdminResource,
} from "../lib/adminApi";

/**
 * A-PERF-01：运行时性能控制（路由 /runtime/performance，读取权限 admin:performance:read）。
 *
 * 真实接口：
 *   GET  /api/admin/v1/runtime/performance
 *   POST /api/admin/v1/runtime/performance/preview          （切换前预估影响）
 *   POST /api/admin/v1/runtime/performance/commands/apply   （提交模式，turbo 需 step-up）
 *   POST /api/admin/v1/runtime/performance/commands/rollback
 *
 * 页面同时展示 requested / effective / reason 三者：只看请求模式会掩盖运行时降级的事实。
 */

interface PerformanceState {
  requested_mode: string;
  effective_mode: string;
  configured_mode: string;
  reason: string;
  changed_at: string;
  target_concurrency: number;
  active_concurrency: number;
  hard_ceiling: number;
  reject_boundary: number;
  memory_budget_bytes: number;
  absolute_rss_limit_bytes: number;
  page_size_default: number;
  request_timeout_seconds: number;
  range_sharding_enabled: boolean;
  modes: Record<string, number>;
  data_source: Record<string, string>;
}

interface PreviewResult {
  requested_mode: string;
  current_effective_mode: string;
  current_target_concurrency: number;
  next_target_concurrency: number;
  hard_ceiling: number;
  reject_boundary: number;
  memory_budget_bytes: number;
  effective_reason_preview: string;
  active_jobs: number;
  highest_active_requested_mode: string;
  requires_approval: boolean;
  data_source: string;
}

const MODES = ["safe", "fast", "turbo"] as const;
type Mode = (typeof MODES)[number];

function isMode(value: unknown): value is Mode {
  return typeof value === "string" && (MODES as readonly string[]).includes(value);
}

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function boolText(value: unknown): string {
  if (value === true) return "是";
  if (value === false) return "否";
  return "—";
}

function apiErrorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.traceId) details.push({ label: "Trace", value: <span className="mono">{error.traceId}</span> });
  if (error.code) details.push({ label: "错误码", value: error.code });
  details.push({ label: "HTTP", value: String(error.status) });
  Object.entries(error.details ?? {}).forEach(([key, value]) => {
    if (value === null || value === undefined) return;
    details.push({ label: key, value: text(value) });
  });
  return details;
}

export default function PerformancePage() {
  const dialog = useDialog();
  const { client, can } = useAuth();
  const resource = useAdminResource<PerformanceState>("/api/admin/v1/runtime/performance");
  const command = useAdminCommand();
  const state = resource.data;

  const requirePermission = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", null, [{ label: "所需权限", value: permission }]);
    return false;
  };

  const showScope = () => {
    dialog.notify("info", "性能控制口径", null, [
      { label: "运行时状态", value: "postgres:ankr_performance_control（requested / effective / reason / changed_at）" },
      { label: "资源边界", value: "postgres:ankr_production_profiles（hard_ceiling / reject_boundary / memory_budget）" },
      { label: "requested_mode", value: "管理员最后一次请求的模式" },
      { label: "effective_mode", value: "运行时实际生效模式；存在更高模式的活动任务时会被抬高，并在 reason 中说明" },
      { label: "configured_mode", value: "部署配置声明的模式，作为回滚目标" },
      { label: "预估接口", value: "POST /runtime/performance/preview：给出目标并发、生效原因预估、活动任务与是否需要审批" },
      { label: "apply / rollback", value: "提交后写入运行时请求模式；turbo 需要二次身份确认（step-up 密码）" },
      { label: "读取权限", value: "admin:performance:read" },
      { label: "变更权限", value: "admin:performance:update" },
    ]);
  };

  const previewDetails = (preview: PreviewResult): Array<{ label: string; value: ReactNode }> => [
    { label: "目标模式", value: <StatusBadge tone="info">{preview.requested_mode}</StatusBadge> },
    { label: "当前生效模式", value: text(preview.current_effective_mode) },
    { label: "当前目标并发", value: fmtNumber(preview.current_target_concurrency) },
    { label: "预估目标并发", value: fmtNumber(preview.next_target_concurrency) },
    { label: "硬上限", value: fmtNumber(preview.hard_ceiling) },
    { label: "拒绝边界", value: fmtNumber(preview.reject_boundary) },
    { label: "内存预算", value: fmtBytes(preview.memory_budget_bytes) },
    { label: "生效原因预估", value: text(preview.effective_reason_preview) },
    { label: "运行中任务", value: fmtNumber(preview.active_jobs) },
    { label: "最高活动请求模式", value: text(preview.highest_active_requested_mode) },
    {
      label: "需要审批 / 二次确认",
      value: <StatusBadge tone={preview.requires_approval ? "warn" : "neutral"}>{boolText(preview.requires_approval)}</StatusBadge>,
    },
    { label: "预估数据来源", value: preview.data_source },
  ];

  const commandDetails = (data: Record<string, unknown>): Array<{ label: string; value: ReactNode }> => [
    { label: "命令", value: text(data.command) },
    { label: "请求模式", value: text(data.requested_mode) },
    { label: "生效模式", value: text(data.effective_mode) },
    { label: "生效原因", value: text(data.reason) },
    { label: "目标并发", value: text(data.target_concurrency) },
    { label: "当前并发", value: text(data.active_concurrency) },
    { label: "硬上限", value: text(data.hard_ceiling) },
    { label: "提交备注", value: text(data.requested_note) },
  ];

  const switchMode = async () => {
    if (!requirePermission("admin:performance:update")) return;
    const chosen = await dialog.open({
      kind: "confirm",
      title: "选择目标性能模式",
      body: "选择后会先调用预估接口，预估结果（含并发、生效原因、活动任务、是否需要审批）会在确认弹窗中展示，确认后才提交。",
      details: [
        { label: "SAFE 并发", value: fmtNumber(state?.modes?.safe ?? 0) },
        { label: "FAST 并发", value: fmtNumber(state?.modes?.fast ?? 0) },
        { label: "TURBO 并发", value: fmtNumber(state?.modes?.turbo ?? 0) },
        { label: "硬上限", value: fmtNumber(state?.hard_ceiling ?? 0) },
        { label: "当前请求模式", value: text(state?.requested_mode) },
        { label: "当前生效模式", value: text(state?.effective_mode) },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "SAFE", result: "safe" },
        { label: "FAST", result: "fast", variant: "primary" },
        { label: "TURBO（需二次确认）", result: "turbo", variant: "danger" },
      ],
    });
    if (!isMode(chosen)) return;

    let preview: PreviewResult;
    try {
      const response = await client.post<PreviewResult>(
        "/api/admin/v1/runtime/performance/preview",
        { mode: chosen },
        { idempotencyKey: newIdempotencyKey() },
      );
      preview = response.data;
    } catch (error) {
      dialog.notify(
        "error",
        "性能预估失败",
        error instanceof ApiError ? error.userMessage : "请稍后重试",
        apiErrorDetails(error),
      );
      return;
    }

    await command.run({
      path: "/api/admin/v1/runtime/performance/commands/apply",
      body: { mode: chosen },
      variant: chosen === "turbo" ? "danger" : "primary",
      requireStepUp: chosen === "turbo",
      confirmTitle: `确认切换到 ${chosen.toUpperCase()}`,
      confirmBody: "以上为预估结果；提交后立即写入运行时请求模式，运行中任务不中断，新任务按新并发执行。",
      confirmLabel: "提交切换",
      details: previewDetails(preview),
      successTitle: "性能模式已提交",
      successDetails: commandDetails,
      onDone: () => resource.reload(),
    });
  };

  /** 回滚：先按普通命令提交；服务端要求 step-up（当前请求模式为 turbo）时再升级提交。 */
  const rollback = async () => {
    if (!requirePermission("admin:performance:update")) return;
    const confirmed = await dialog.confirm({
      kind: "warning",
      title: "回滚性能模式",
      body: "回滚会把请求模式恢复为当前会话请求的模式；若当前为 turbo，服务端会要求二次身份确认。",
      details: [
        { label: "当前请求模式", value: text(state?.requested_mode) },
        { label: "当前生效模式", value: text(state?.effective_mode) },
        { label: "配置模式", value: text(state?.configured_mode) },
        { label: "生效原因", value: text(state?.reason) },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "回滚", result: "ok", variant: "danger" },
      ],
    });
    if (confirmed !== "ok") return;

    const raw = await dialog.prompt({
      kind: "warning",
      title: "填写回滚原因",
      body: "原因会写入审计日志，无法为空。",
      input: { label: "原因", placeholder: "例如：高峰结束，回到常规并发", pattern: /^.{1,500}$/, patternHint: "1-500 个字符" },
      submitLabel: "提交回滚",
    });
    if (raw === null) return;
    const reason = raw.startsWith("input:") ? raw.slice("input:".length).trim() : raw.trim();
    if (reason === "") return;

    const path = "/api/admin/v1/runtime/performance/commands/rollback";
    try {
      const response = await client.post<Record<string, unknown>>(path, { reason }, { idempotencyKey: newIdempotencyKey() });
      dialog.notify("success", "已回滚性能模式", null, commandDetails(response.data ?? {}));
      resource.reload();
    } catch (error) {
      if (error instanceof ApiError && error.code === "STEP_UP_REQUIRED") {
        await command.run({
          path,
          body: { reason },
          requireReason: false,
          requireStepUp: true,
          variant: "danger",
          confirmTitle: "二次身份确认后回滚",
          confirmBody: "当前请求模式为 turbo，回滚需要重新确认当前账号身份。",
          confirmLabel: "确认身份并回滚",
          successTitle: "已回滚性能模式",
          successDetails: commandDetails,
          onDone: () => resource.reload(),
        });
        return;
      }
      dialog.notify(
        "error",
        "回滚失败",
        error instanceof ApiError ? error.userMessage : "请稍后重试",
        apiErrorDetails(error),
      );
    }
  };

  if (resource.loading && !state) {
    return (
      <>
        <PageHeader title="性能控制" subtitle={<span className="mono">/api/admin/v1/runtime/performance</span>} />
        <Card title="正在加载">
          <SkeletonRows rows={6} columns={4} />
        </Card>
      </>
    );
  }

  if (!state) {
    return (
      <>
        <PageHeader title="性能控制" subtitle={<span className="mono">/api/admin/v1/runtime/performance</span>} />
        <Card>
          <ErrorState title="性能运行状态加载失败" message={resource.error ?? "控制面存储不可用"} onRetry={resource.reload} />
        </Card>
      </>
    );
  }

  const dataSource = Object.entries(state.data_source ?? {});

  return (
    <>
      <PageHeader
        title="性能控制"
        subtitle={
          <>
            数据源 <span className="mono">ankr_performance_control · ankr_production_profiles</span>
          </>
        }
        actions={
          <>
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!can("admin:performance:update") || command.busy}
              onClick={() => void switchMode()}
            >
              切换模式
            </button>
            <button
              type="button"
              className="btn btn--sm btn--danger"
              disabled={!can("admin:performance:update") || command.busy}
              onClick={() => void rollback()}
            >
              回滚
            </button>
            <button type="button" className="btn btn--sm" onClick={resource.reload}>
              刷新
            </button>
            <button type="button" className="btn btn--sm" onClick={showScope}>
              口径
            </button>
          </>
        }
      />

      <div className="metric-grid">
        <Metric label="requested" value={state.requested_mode} hint="requested_mode" />
        <Metric label="effective" value={state.effective_mode} hint="effective_mode" tone={state.effective_mode === state.requested_mode ? "ok" : "warn"} />
        <Metric
          label="reason"
          value={<span className="mono" style={{ fontSize: "var(--fs-14)" }}>{state.reason || "—"}</span>}
          hint="reason"
        />
        <Metric
          label="目标并发"
          value={fmtNumber(state.target_concurrency)}
          hint={`active ${fmtNumber(state.active_concurrency)}`}
        />
        <Metric label="硬上限" value={fmtNumber(state.hard_ceiling)} hint="hard_ceiling" />
        <Metric label="拒绝边界" value={fmtNumber(state.reject_boundary)} hint="reject_boundary" />
        <Metric label="内存预算" value={fmtBytes(state.memory_budget_bytes)} hint="memory_budget_bytes" />
      </div>

      <Card title="模式与并发">
        <KeyValueList
          columns={3}
          items={[
            { label: "requested_mode", value: <StatusBadge tone="info">{state.requested_mode}</StatusBadge> },
            {
              label: "effective_mode",
              value: <StatusBadge tone={state.effective_mode === state.requested_mode ? "ok" : "warn"}>{state.effective_mode}</StatusBadge>,
            },
            { label: "configured_mode", value: state.configured_mode || "—" },
            { label: "reason", value: state.reason || "—", mono: true },
            { label: "changed_at", value: fmtDateTime(state.changed_at) },
            { label: "active_concurrency", value: fmtNumber(state.active_concurrency) },
            { label: "SAFE 并发", value: fmtNumber(state.modes?.safe ?? 0) },
            { label: "FAST 并发", value: fmtNumber(state.modes?.fast ?? 0) },
            { label: "TURBO 并发", value: fmtNumber(state.modes?.turbo ?? 0) },
          ]}
        />
      </Card>

      <Card title="资源边界与背压">
        <KeyValueList
          columns={3}
          items={[
            { label: "hard_ceiling", value: fmtNumber(state.hard_ceiling) },
            { label: "reject_boundary", value: fmtNumber(state.reject_boundary) },
            { label: "memory_budget_bytes", value: `${fmtBytes(state.memory_budget_bytes)}（${fmtNumber(state.memory_budget_bytes)} B）` },
            { label: "absolute_rss_limit_bytes", value: `${fmtBytes(state.absolute_rss_limit_bytes)}（${fmtNumber(state.absolute_rss_limit_bytes)} B）` },
            { label: "page_size_default", value: fmtNumber(state.page_size_default) },
            { label: "request_timeout_seconds", value: `${fmtNumber(state.request_timeout_seconds)} 秒` },
            { label: "range_sharding_enabled", value: boolText(state.range_sharding_enabled) },
            { label: "target_concurrency", value: fmtNumber(state.target_concurrency) },
            { label: "active_concurrency", value: fmtNumber(state.active_concurrency) },
          ]}
        />
      </Card>

      <Card title="数据来源">
        <KeyValueList
          items={
            dataSource.length > 0
              ? dataSource.map(([key, value]) => ({ label: key, value, mono: true }))
              : [{ label: "data_source", value: "—" }]
          }
        />
      </Card>
    </>
  );
}
