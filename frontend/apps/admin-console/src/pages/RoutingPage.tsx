import { useMemo, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  Metric,
  PageHeader,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { DiffList, Timeline } from "../components/s6";
import {
  fmtDateTime,
  fmtMillis,
  fmtNumber,
  fmtPercent,
  useAdminList,
  useAdminPatch,
} from "../lib/adminApi";

/**
 * A-ROUTING-01：链 / Dataset 路由策略（路由 /providers/routing，读取权限 admin:routing:read）。
 *
 * 真实接口：
 *   GET   /api/admin/v1/routing-policies
 *   GET   /api/admin/v1/routing-policies/{id 或 policy_key}   → { policy, history }
 *   PATCH /api/admin/v1/routing-policies/{id}                （If-Match: revision）
 *
 * 所有编辑都在弹窗内完成：先选字段分组，再逐个字段录入，最后 useAdminPatch 提交。
 */

interface RoutingPolicy {
  id: string;
  policy_key: string;
  description: string;
  chain: string;
  dataset: string;
  enabled: boolean;
  primary_provider: string;
  fallback_providers: string[];
  max_concurrency: number;
  rate_limit_per_minute: number;
  circuit_error_rate: number;
  circuit_p95_ms: number;
  circuit_cooldown_seconds: number;
  rollout_percent: number;
  revision: number;
  /** 0075 起：api_channel / operation_class 显式保存在 policy config 里（只增不改）。 */
  config?: Record<string, unknown>;
  api_channel?: string;
  operation_class?: string;
  updated_at: string;
}

interface HistoryEntry {
  revision: number;
  before: Record<string, unknown> | null;
  after: Record<string, unknown> | null;
  change_note: string;
  actor_email: string;
  created_at: string;
}

interface PolicyDetail {
  policy: RoutingPolicy;
  history: HistoryEntry[];
}

const HISTORY_LABELS: Record<string, string> = {
  enabled: "启用",
  primary_provider: "主 Provider",
  fallback_providers: "Fallback",
  max_concurrency: "最大并发",
  rate_limit_per_minute: "每分钟限流",
  circuit_error_rate: "熔断错误率",
  circuit_p95_ms: "熔断 P95",
  circuit_cooldown_seconds: "熔断冷却秒",
  rollout_percent: "灰度 %",
  revision: "revision",
  policy_key: "policy_key",
  chain: "链",
  dataset: "Dataset",
  description: "描述",
  id: "ID",
  updated_at: "更新时间",
};

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

/**
 * 通道与操作类的唯一口径是 policy config（0075 迁移显式写入），
 * 运行时不按 policy_key 命名约定推断；旧行两者都缺时展示「—」。
 */
function policyChannel(row: RoutingPolicy): string {
  const direct = row.api_channel;
  if (typeof direct === "string" && direct !== "") return direct;
  const fromConfig = row.config?.api_channel;
  return typeof fromConfig === "string" && fromConfig !== "" ? fromConfig : "";
}

function policyOperationClass(row: RoutingPolicy): string {
  const direct = row.operation_class;
  if (typeof direct === "string" && direct !== "") return direct;
  const fromConfig = row.config?.operation_class;
  return typeof fromConfig === "string" && fromConfig !== "" ? fromConfig : "";
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

export default function RoutingPage() {
  const dialog = useDialog();
  const { client, can } = useAuth();
  const list = useAdminList<RoutingPolicy>("/api/admin/v1/routing-policies", {}, 50);
  const patch = useAdminPatch();
  const [historyKey, setHistoryKey] = useState<string | null>(null);
  const [chainFilter, setChainFilter] = useState<string>("all");

  const allRows = list.items;
  const chainOptions = useMemo(
    () => Array.from(new Set(allRows.map((row) => row.chain).filter((chain) => chain !== ""))),
    [allRows],
  );
  const rows = chainFilter === "all" ? allRows : allRows.filter((row) => row.chain === chainFilter);

  const summary = useMemo(() => {
    const enabled = rows.filter((row) => row.enabled).length;
    const canary = rows.filter((row) => row.rollout_percent > 0 && row.rollout_percent < 100).length;
    const noFallback = rows.filter((row) => (row.fallback_providers ?? []).length === 0).length;
    const solana = allRows.filter((row) => row.chain === "solana").length;
    return { enabled, canary, noFallback, solana };
  }, [allRows, rows]);

  const requirePermission = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", null, [{ label: "所需权限", value: permission }]);
    return false;
  };

  const askText = async (request: Parameters<typeof dialog.prompt>[0]) => {
    const raw = await dialog.prompt(request);
    if (raw === null) return { cancelled: true, value: "" };
    const value = raw.startsWith("input:") ? raw.slice("input:".length).trim() : raw.trim();
    return { cancelled: false, value };
  };

  const showScope = () =>
    dialog.notify("info", "路由策略口径", null, [
      { label: "数据来源", value: "postgres:routing_policies" },
      { label: "变更历史来源", value: "postgres:routing_policy_revisions（保留最近 20 条）" },
      { label: "熔断错误率", value: "0-1 比例；达到阈值后按熔断 P95 与冷却秒判定摘除" },
      { label: "灰度 rollout_percent", value: "0-100，表示新请求切到主 Provider 的百分比" },
      { label: "Fallback", value: "主 Provider 熔断或限流时按顺序尝试" },
      { label: "通道 / 操作类", value: "routing_policies.config 的 api_channel 与 operation_class（0075 起显式保存），运行时不按 policy_key 命名约定推断" },
      { label: "Solana Fallback", value: "本期 Solana 不设置跨 Provider 自动 fallback；失败时暂停并保留 Checkpoint，不在同一 Shard 静默换源" },
      { label: "读取权限", value: "admin:routing:read" },
      { label: "更新权限", value: "admin:routing:update（提交需携带 If-Match 版本）" },
    ]);

  const applyChanges = async (row: RoutingPolicy, body: Record<string, unknown>) => {
    if (Object.keys(body).length === 0) {
      dialog.notify("warning", "没有需要提交的变更", null, [{ label: "策略", value: row.policy_key }]);
      return;
    }
    await patch.run({
      path: `/api/admin/v1/routing-policies/${encodeURIComponent(row.id)}`,
      ifMatch: row.revision,
      body,
      confirmTitle: `提交路由策略变更 · ${row.policy_key}`,
      confirmBody: "变更立即影响新请求的路由、限流与熔断判定，并写入一条策略版本记录。",
      details: [
        { label: "链", value: row.chain },
        { label: "Dataset", value: row.dataset },
        { label: "原 revision", value: String(row.revision) },
        ...Object.entries(body).map(([key, value]) => ({ label: HISTORY_LABELS[key] ?? key, value: text(value) })),
      ],
      successTitle: "路由策略已更新",
      successDetails: [
        { label: "策略", value: row.policy_key },
        { label: "提交字段", value: Object.keys(body).map((key) => HISTORY_LABELS[key] ?? key).join("、") },
        { label: "原 revision", value: String(row.revision) },
      ],
      onDone: () => list.reload(),
    });
  };

  const editBasic = async (row: RoutingPolicy) => {
    const enabled = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 启用状态`,
      body: "输入 true 启用、false 停用；留空表示不修改。",
      input: {
        label: "enabled",
        defaultValue: String(row.enabled),
        pattern: /^(true|false)?$/,
        patternHint: "true / false / 留空保持不变",
      },
      submitLabel: "下一步",
    });
    if (enabled.cancelled) return;
    const primary = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 主 Provider`,
      body: "填写 Provider key；留空表示不修改。",
      input: { label: "primary_provider", defaultValue: row.primary_provider, pattern: /^[A-Za-z0-9_.:-]{0,120}$/, patternHint: "Provider key，留空保持不变" },
      submitLabel: "完成",
    });
    if (primary.cancelled) return;
    const body: Record<string, unknown> = {};
    if (enabled.value !== "") body.enabled = enabled.value === "true";
    if (primary.value !== "" && primary.value !== row.primary_provider) body.primary_provider = primary.value;
    await applyChanges(row, body);
  };

  const editFallback = async (row: RoutingPolicy) => {
    const current = (row.fallback_providers ?? []).join(",");
    const answer = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · Fallback 列表`,
      body: "逗号分隔的 Provider key，按顺序尝试；输入 - 表示清空；留空表示不修改。",
      input: {
        label: "fallback_providers",
        defaultValue: current,
        pattern: /^[-A-Za-z0-9_,.:\s]{0,500}$/,
        patternHint: "逗号分隔的 Provider key、- 或留空",
      },
      submitLabel: "完成",
    });
    if (answer.cancelled || answer.value === "") return;
    const parsed = answer.value === "-" ? [] : answer.value.split(",").map((item) => item.trim()).filter(Boolean);
    if (JSON.stringify(parsed) === JSON.stringify(row.fallback_providers ?? [])) {
      dialog.notify("warning", "Fallback 列表没有变化", null, [{ label: "当前值", value: current || "无" }]);
      return;
    }
    await applyChanges(row, { fallback_providers: parsed });
  };

  const editLimits = async (row: RoutingPolicy) => {
    const concurrency = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 最大并发`,
      body: "留空表示不修改。",
      input: {
        label: "max_concurrency",
        defaultValue: String(row.max_concurrency),
        pattern: /^(\d{0,7})?$/,
        patternHint: "0-9999999 的整数，留空保持不变",
      },
      submitLabel: "下一步",
    });
    if (concurrency.cancelled) return;
    const rateLimit = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 每分钟限流`,
      body: "留空表示不修改。",
      input: {
        label: "rate_limit_per_minute",
        defaultValue: String(row.rate_limit_per_minute),
        pattern: /^(\d{0,9})?$/,
        patternHint: "整数，留空保持不变",
      },
      submitLabel: "完成",
    });
    if (rateLimit.cancelled) return;
    const body: Record<string, unknown> = {};
    if (concurrency.value !== "") body.max_concurrency = Number(concurrency.value);
    if (rateLimit.value !== "") body.rate_limit_per_minute = Number(rateLimit.value);
    await applyChanges(row, body);
  };

  const editCircuit = async (row: RoutingPolicy) => {
    const errorRate = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 熔断错误率`,
      body: "取值 0-1 的比例（例如 0.25 表示 25%）；留空表示不修改。",
      input: {
        label: "circuit_error_rate",
        defaultValue: String(row.circuit_error_rate),
        pattern: /^((0(\.\d+)?)|(1(\.0+)?))?$/,
        patternHint: "0-1 之间的小数，留空保持不变",
      },
      submitLabel: "下一步",
    });
    if (errorRate.cancelled) return;
    const p95 = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 熔断 P95`,
      body: "单位毫秒；留空表示不修改。",
      input: { label: "circuit_p95_ms", defaultValue: String(row.circuit_p95_ms), pattern: /^(\d{0,7})?$/, patternHint: "整数毫秒，留空保持不变" },
      submitLabel: "下一步",
    });
    if (p95.cancelled) return;
    const cooldown = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 熔断冷却`,
      body: "单位秒；留空表示不修改。",
      input: {
        label: "circuit_cooldown_seconds",
        defaultValue: String(row.circuit_cooldown_seconds),
        pattern: /^(\d{0,6})?$/,
        patternHint: "整数秒，留空保持不变",
      },
      submitLabel: "完成",
    });
    if (cooldown.cancelled) return;
    const body: Record<string, unknown> = {};
    if (errorRate.value !== "") body.circuit_error_rate = Number(errorRate.value);
    if (p95.value !== "") body.circuit_p95_ms = Number(p95.value);
    if (cooldown.value !== "") body.circuit_cooldown_seconds = Number(cooldown.value);
    await applyChanges(row, body);
  };

  const editRollout = async (row: RoutingPolicy) => {
    const rollout = await askText({
      kind: "info",
      title: `编辑 · ${row.policy_key} · 灰度比例`,
      body: "0-100 的整数，表示新请求切到主 Provider 的百分比；留空表示不修改。",
      input: {
        label: "rollout_percent",
        defaultValue: String(row.rollout_percent),
        pattern: /^(\d{0,3})?$/,
        patternHint: "0-100 的整数，留空保持不变",
      },
      submitLabel: "完成",
    });
    if (rollout.cancelled || rollout.value === "") return;
    const percent = Number(rollout.value);
    if (!Number.isFinite(percent) || percent < 0 || percent > 100) {
      dialog.notify("warning", "灰度比例超出范围", null, [{ label: "允许范围", value: "0-100" }, { label: "输入值", value: rollout.value }]);
      return;
    }
    if (percent === row.rollout_percent) {
      dialog.notify("warning", "灰度比例没有变化", null, [{ label: "当前值", value: String(row.rollout_percent) }]);
      return;
    }
    await applyChanges(row, { rollout_percent: percent });
  };

  const openEditor = async (row: RoutingPolicy) => {
    if (!requirePermission("admin:routing:update")) return;
    const group = await dialog.open({
      kind: "confirm",
      title: `编辑路由策略 · ${row.policy_key}`,
      body: "选择要修改的字段分组，随后逐个字段录入；提交时自动携带 If-Match 版本。",
      details: [
        { label: "链 / Dataset", value: row.chain + " / " + row.dataset },
        { label: "通道 api_channel", value: policyChannel(row) || "—" },
        { label: "操作类 operation_class", value: policyOperationClass(row) || "—" },
        { label: "启用", value: row.enabled ? "true" : "false" },
        { label: "主 Provider", value: row.primary_provider || "—" },
        { label: "Fallback", value: (row.fallback_providers ?? []).join("、") || "无" },
        { label: "最大并发", value: fmtNumber(row.max_concurrency) },
        { label: "每分钟限流", value: fmtNumber(row.rate_limit_per_minute) },
        { label: "熔断错误率", value: fmtPercent(row.circuit_error_rate, 2) },
        { label: "熔断 P95", value: fmtMillis(row.circuit_p95_ms) },
        { label: "熔断冷却", value: `${fmtNumber(row.circuit_cooldown_seconds)} 秒` },
        { label: "灰度", value: `${row.rollout_percent}%` },
        { label: "revision", value: fmtNumber(row.revision) },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "基础", result: "basic", variant: "primary" },
        { label: "Fallback", result: "fallback" },
        { label: "并发与限流", result: "limits" },
        { label: "熔断阈值", result: "circuit" },
        { label: "灰度", result: "rollout" },
      ],
    });
    if (group === "basic") await editBasic(row);
    else if (group === "fallback") await editFallback(row);
    else if (group === "limits") await editLimits(row);
    else if (group === "circuit") await editCircuit(row);
    else if (group === "rollout") await editRollout(row);
  };

  const showHistory = async (row: RoutingPolicy) => {
    if (!requirePermission("admin:routing:read")) return;
    if (historyKey) return;
    setHistoryKey(row.id);
    try {
      const response = await client.get<PolicyDetail>(
        `/api/admin/v1/routing-policies/${encodeURIComponent(row.id)}`,
      );
      const detail = response.data;
      const history = detail.history ?? [];
      dialog.open({
        kind: "info",
        title: `变更历史 · ${detail.policy?.policy_key ?? row.policy_key}`,
        body: (
          <Timeline
            emptyLabel="该策略暂无版本记录"
            entries={history.map((entry) => ({
              id: String(entry.revision),
              title: `revision ${entry.revision}`,
              meta: `${fmtDateTime(entry.created_at)} · ${entry.actor_email || "—"}`,
              body: (
                <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
                  <span>{entry.change_note || "—"}</span>
                  <div className="table-scroll">
                    <DiffList
                      before={(entry.before ?? {}) as Record<string, unknown>}
                      after={(entry.after ?? {}) as Record<string, unknown>}
                      labels={HISTORY_LABELS}
                    />
                  </div>
                </div>
              ),
            }))}
          />
        ),
        actions: [{ label: "关闭", result: "", variant: "default" }],
      });
    } catch (error) {
      dialog.notify(
        "error",
        "读取变更历史失败",
        error instanceof ApiError ? error.userMessage : "请稍后重试",
        apiErrorDetails(error),
      );
    } finally {
      setHistoryKey(null);
    }
  };

  return (
    <>
      <PageHeader
        title="路由策略"
        subtitle={
          <>
            数据源 <span className="mono">routing_policies · routing_policy_revisions</span>
          </>
        }
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={list.reload}>
              刷新
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.hasPrevious} onClick={list.goPrevious}>
              上一页
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.nextCursor} onClick={list.goNext}>
              下一页
            </button>
            <select
              className="select"
              aria-label="按链筛选"
              style={{ width: "140px" }}
              value={chainFilter}
              onChange={(event) => setChainFilter(event.target.value)}
            >
              <option value="all">全部链</option>
              {chainOptions.map((chain) => (
                <option key={chain} value={chain}>
                  {chain}
                </option>
              ))}
            </select>
            <button type="button" className="btn btn--sm" onClick={showScope}>
              口径
            </button>
          </>
        }
      />

      <div className="metric-grid">
        <Metric label="路由策略" value={fmtNumber(rows.length)} hint={chainFilter === "all" ? "全部链" : chainFilter} />
        <Metric label="Solana 策略" value={fmtNumber(summary.solana)} hint="chain=solana" />
        <Metric label="已启用" value={fmtNumber(summary.enabled)} tone="ok" />
        <Metric label="灰度中" value={fmtNumber(summary.canary)} tone={summary.canary > 0 ? "warn" : "neutral"} />
        <Metric label="无 Fallback" value={fmtNumber(summary.noFallback)} tone={summary.noFallback > 0 ? "warn" : "ok"} />
      </div>

      <Card title="策略列表" flush>
        <DataTable<RoutingPolicy>
          rows={rows}
          rowKey={(row) => row.id}
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          columns={[
            {
              key: "policy",
              header: "策略",
              render: (row) => (
                <span>
                  <strong className="mono">{row.policy_key}</strong>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.description || "—"}</div>
                </span>
              ),
            },
            { key: "chain", header: "链", width: "110px", render: (row) => row.chain || "—" },
            { key: "dataset", header: "Dataset", width: "130px", render: (row) => <span className="mono">{row.dataset || "—"}</span> },
            {
              key: "channel",
              header: "通道",
              width: "120px",
              render: (row) =>
                policyChannel(row) !== "" ? (
                  <StatusBadge tone="info">{policyChannel(row)}</StatusBadge>
                ) : (
                  <span className="num">—</span>
                ),
            },
            {
              key: "operation_class",
              header: "操作类",
              width: "150px",
              render: (row) => <span className="mono">{policyOperationClass(row) || "—"}</span>,
            },
            {
              key: "enabled",
              header: "启用",
              width: "100px",
              render: (row) => <StatusBadge tone={row.enabled ? "ok" : "neutral"}>{row.enabled ? "enabled" : "disabled"}</StatusBadge>,
            },
            { key: "primary", header: "主 Provider", width: "150px", render: (row) => <span className="mono">{row.primary_provider || "—"}</span> },
            {
              key: "fallback",
              header: "Fallback",
              render: (row) =>
                (row.fallback_providers ?? []).length > 0 ? (
                  <span className="toolbar">
                    {(row.fallback_providers ?? []).map((provider) => (
                      <StatusBadge key={provider} tone="info">
                        {provider}
                      </StatusBadge>
                    ))}
                  </span>
                ) : (
                  <StatusBadge tone="warn">无</StatusBadge>
                ),
            },
            {
              key: "concurrency",
              header: "并发",
              width: "90px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.max_concurrency)}</span>,
            },
            {
              key: "rate",
              header: "限流/分钟",
              width: "110px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.rate_limit_per_minute)}</span>,
            },
            {
              key: "circuit",
              header: "熔断（错误率 / P95 / 冷却）",
              width: "230px",
              render: (row) => (
                <span className="num">
                  {fmtPercent(row.circuit_error_rate, 2)} · {fmtMillis(row.circuit_p95_ms)} · {fmtNumber(row.circuit_cooldown_seconds)}s
                </span>
              ),
            },
            {
              key: "rollout",
              header: "灰度",
              width: "90px",
              align: "right",
              render: (row) => <span className="num">{row.rollout_percent}%</span>,
            },
            {
              key: "revision",
              header: "版本",
              width: "80px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.revision)}</span>,
            },
            {
              key: "updated",
              header: "更新时间",
              width: "170px",
              render: (row) => <span className="num">{fmtDateTime(row.updated_at)}</span>,
            },
            {
              key: "actions",
              header: "操作",
              width: "160px",
              align: "right",
              render: (row) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!can("admin:routing:update") || patch.busy}
                    onClick={() => void openEditor(row)}
                  >
                    编辑
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={historyKey === row.id}
                    onClick={() => void showHistory(row)}
                  >
                    历史
                  </button>
                </span>
              ),
            },
          ]}
        />
      </Card>
    </>
  );
}
