import { useId, useState } from "react";
import { useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  PageHeader,
  StatusBadge,
  type StatusTone,
  useDialog,
} from "@chain/ui";
import { fmtDateTime, fmtNumber, fmtUSD, useAdminList, useAdminPatch } from "../lib/adminApi";
import { CheckboxRow, NumberInput, SelectInput, TextInput } from "../components/s6";

/**
 * A-BUDGET-01：预算（路由 /budgets，权限 admin:budget:read，写入需 admin:budget:update）。
 *
 * 数据来源：GET /api/admin/v1/budgets（含 spent_usd 字符串与 utilization）。
 * 新建：POST /api/admin/v1/budgets；编辑：PATCH /api/admin/v1/budgets/{id}（If-Match: revision）。
 * 表单承载字段标签与控件；所有说明、成功与错误只通过弹窗呈现。
 */

interface BudgetRow {
  id: string;
  name?: string;
  provider?: string;
  period?: string;
  limit_usd?: number;
  limit_usd_display?: string;
  warn_percent?: number;
  enabled?: boolean;
  dimension?: string;
  scope_key?: string;
  action?: string;
  action_threshold?: number;
  currency?: string;
  notes?: string;
  revision?: number;
  updated_at?: string;
  spent_usd?: string;
  utilization?: number;
  spent_is_string?: boolean;
}

interface BudgetDraft {
  name: string;
  provider: string;
  period: string;
  limit_usd: string;
  warn_percent: string;
  dimension: string;
  scope_key: string;
  action: string;
  action_threshold: string;
  notes: string;
  enabled: boolean;
}

const PERIOD_OPTIONS = [
  { value: "day", label: "day（日）" },
  { value: "week", label: "week（周）" },
  { value: "month", label: "month（月）" },
  { value: "quarter", label: "quarter（季）" },
];

const ACTION_OPTIONS = [
  { value: "alert", label: "alert（仅告警）" },
  { value: "throttle", label: "throttle（限流）" },
  { value: "pause", label: "pause（暂停任务）" },
  { value: "block", label: "block（阻断请求）" },
];

const EMPTY_DRAFT: BudgetDraft = {
  name: "",
  provider: "all",
  period: "month",
  limit_usd: "1000",
  warn_percent: "80",
  dimension: "provider",
  scope_key: "",
  action: "alert",
  action_threshold: "100",
  notes: "",
  enabled: true,
};

function draftOf(row: BudgetRow): BudgetDraft {
  return {
    name: row.name ?? "",
    provider: row.provider ?? "all",
    period: row.period ?? "month",
    limit_usd: String(row.limit_usd ?? 0),
    warn_percent: String(row.warn_percent ?? 80),
    dimension: row.dimension ?? "provider",
    scope_key: row.scope_key ?? "",
    action: row.action ?? "alert",
    action_threshold: String(row.action_threshold ?? 100),
    notes: row.notes ?? "",
    enabled: row.enabled !== false,
  };
}

/** BudgetForm 只在弹窗正文里渲染字段与控件；取值通过 draft.current 回传给页面。 */
function BudgetForm({ draft, mode }: { draft: { current: BudgetDraft }; mode: "create" | "edit" }) {
  const baseId = useId();
  const [form, setForm] = useState<BudgetDraft>(draft.current);
  const update = (patch: Partial<BudgetDraft>) => {
    const next = { ...form, ...patch };
    setForm(next);
    draft.current = next;
  };
  return (
    <div className="toolbar" style={{ alignItems: "flex-end" }}>
      {mode === "create" ? (
        <TextInput
          id={baseId + "-name"}
          label="预算名称"
          value={form.name}
          onChange={(value) => update({ name: value })}
          placeholder="例如：Ankr 月度预算"
        />
      ) : null}
      <TextInput
        id={baseId + "-provider"}
        label="Provider"
        value={form.provider}
        onChange={(value) => update({ provider: value })}
        placeholder="all 或 provider key"
      />
      <SelectInput id={baseId + "-period"} label="周期" value={form.period} onChange={(value) => update({ period: value })} options={PERIOD_OPTIONS} />
      <NumberInput id={baseId + "-limit"} label="额度（USD）" value={Number(form.limit_usd)} onChange={(value) => update({ limit_usd: String(value) })} min={1} step={1} />
      <NumberInput id={baseId + "-warn"} label="预警百分比" value={Number(form.warn_percent)} onChange={(value) => update({ warn_percent: String(value) })} min={1} max={100} step={1} />
      <TextInput id={baseId + "-dimension"} label="维度" value={form.dimension} onChange={(value) => update({ dimension: value })} placeholder="provider / organization / workspace" />
      <TextInput id={baseId + "-scope"} label="作用域键" value={form.scope_key} onChange={(value) => update({ scope_key: value })} placeholder="留空表示全局" />
      <SelectInput id={baseId + "-action"} label="超限动作" value={form.action} onChange={(value) => update({ action: value })} options={ACTION_OPTIONS} />
      <NumberInput id={baseId + "-threshold"} label="动作阈值（%）" value={Number(form.action_threshold)} onChange={(value) => update({ action_threshold: String(value) })} min={1} max={200} step={1} />
      <TextInput id={baseId + "-notes"} label="备注" value={form.notes} onChange={(value) => update({ notes: value })} placeholder="可选" />
      {mode === "edit" ? (
        <CheckboxRow
          id={baseId + "-enabled"}
          label="启用该预算"
          checked={form.enabled}
          onChange={(checked) => update({ enabled: checked })}
        />
      ) : null}
    </div>
  );
}

export default function BudgetsPage() {
  const dialog = useDialog();
  const { can } = useAuth();
  const list = useAdminList<BudgetRow>("/api/admin/v1/budgets", {}, 100);
  const patch = useAdminPatch();

  const canUpdate = can("admin:budget:update");

  const guard = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", "该操作需要 " + permission + "。");
    return false;
  };

  const openCreate = async () => {
    if (!guard("admin:budget:update")) return;
    const draft = { current: { ...EMPTY_DRAFT } };
    const answer = await dialog.open({
      kind: "confirm",
      title: "新建预算",
      body: <BudgetForm draft={draft} mode="create" />,
      details: [{ label: "币种", value: "USD" }],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "下一步", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    const limit = Number(values.limit_usd);
    if (values.name.trim() === "" || !Number.isFinite(limit) || limit <= 0) {
      dialog.notify("warning", "表单不完整", "预算名称必填，额度必须是大于 0 的整数。");
      return;
    }
    await patch.run({
      path: "/api/admin/v1/budgets",
      method: "post",
      body: {
        name: values.name.trim(),
        provider: values.provider.trim() || "all",
        period: values.period,
        limit_usd: Math.round(limit),
        warn_percent: Math.round(Number(values.warn_percent)),
        dimension: values.dimension.trim() || "provider",
        scope_key: values.scope_key.trim(),
        action: values.action,
        action_threshold: Math.round(Number(values.action_threshold)),
        notes: values.notes,
      },
      confirmTitle: "创建预算",
      confirmBody: "创建后立即按所选周期统计用量，达到阈值会触发所选动作。",
      confirmLabel: "创建",
      details: [
        { label: "预算名称", value: values.name.trim() },
        { label: "Provider", value: values.provider.trim() || "all" },
        { label: "额度", value: "$" + Math.round(limit) },
        { label: "周期", value: values.period },
        { label: "动作", value: values.action },
      ],
      successTitle: "预算已创建",
      onDone: () => list.reload(),
    });
  };

  const openEdit = async (row: BudgetRow) => {
    if (!guard("admin:budget:update")) return;
    const draft = { current: draftOf(row) };
    const answer = await dialog.open({
      kind: "confirm",
      title: "编辑预算 · " + (row.name ?? ""),
      body: <BudgetForm draft={draft} mode="edit" />,
      details: [
        { label: "预算 ID", value: <span className="mono">{row.id}</span> },
        { label: "当前版本", value: String(row.revision ?? "—") },
        { label: "已用 / 额度", value: fmtUSD(row.spent_usd) + " / " + fmtUSD(row.limit_usd_display) },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "下一步", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    const limit = Number(values.limit_usd);
    if (!Number.isFinite(limit) || limit <= 0) {
      dialog.notify("warning", "表单不完整", "额度必须是大于 0 的整数。");
      return;
    }
    await patch.run({
      path: "/api/admin/v1/budgets/" + encodeURIComponent(row.id),
      ifMatch: row.revision,
      body: {
        limit_usd: Math.round(limit),
        warn_percent: Math.round(Number(values.warn_percent)),
        enabled: values.enabled,
        action: values.action,
        action_threshold: Math.round(Number(values.action_threshold)),
        notes: values.notes,
      },
      confirmTitle: "保存预算变更",
      confirmBody: "变更会写入审计日志，并立即影响后续用量判定。",
      details: [
        { label: "预算", value: row.name ?? row.id },
        { label: "If-Match 版本", value: String(row.revision ?? "—") },
        { label: "新额度", value: "$" + Math.round(limit) },
        { label: "新动作", value: values.action },
        { label: "启用状态", value: values.enabled ? "已启用" : "已停用" },
      ],
      successTitle: "预算已更新",
      onDone: () => list.reload(),
    });
  };

  const toggleEnabled = async (row: BudgetRow) => {
    if (!guard("admin:budget:update")) return;
    await patch.run({
      path: "/api/admin/v1/budgets/" + encodeURIComponent(row.id),
      ifMatch: row.revision,
      body: { enabled: !row.enabled },
      variant: row.enabled ? "danger" : "primary",
      confirmTitle: row.enabled ? "停用该预算？" : "启用该预算？",
      confirmBody: row.enabled ? "停用后不再按该预算触发告警或动作。" : "启用后重新按周期统计并触发对应动作。",
      confirmLabel: row.enabled ? "停用" : "启用",
      details: [
        { label: "预算", value: row.name ?? row.id },
        { label: "If-Match 版本", value: String(row.revision ?? "—") },
      ],
      successTitle: row.enabled ? "预算已停用" : "预算已启用",
      onDone: () => list.reload(),
    });
  };

  const explainScope = () => {
    dialog.notify("info", "预算口径", null, [
      { label: "接口", value: "GET /api/admin/v1/budgets" },
      { label: "数据源", value: "postgres:api_control_budgets + provider_usage" },
      { label: "已用金额", value: "按本月（date_trunc month）累计的估算成本，字符串返回" },
      { label: "利用率", value: "已用金额 ÷ 额度；超过预警百分比显示黄色，超过 100% 显示红色" },
      { label: "并发控制", value: "编辑必须携带 If-Match: revision；版本不符返回 412" },
      { label: "动作阈值", value: "达到阈值百分比后执行所选动作（alert / throttle / pause / block）" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="预算"
        subtitle={"共 " + list.items.length + " 条 · 来源 /api/admin/v1/budgets"}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainScope}>
              口径
            </button>
            <button type="button" className="btn btn--sm" onClick={list.reload} disabled={list.loading}>
              刷新
            </button>
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!canUpdate || patch.busy}
              onClick={() => void openCreate()}
            >
              新建预算
            </button>
          </>
        }
      />

      <Card title={"预算（本页 " + list.items.length + "）"} flush>
        <DataTable<BudgetRow>
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          rows={list.items}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有配置任何预算" />}
          columns={[
            {
              key: "name",
              header: "预算",
              width: "200px",
              render: (row) => (
                <span>
                  <strong>{row.name || "—"}</strong>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }} title={row.id}>
                    {row.id}
                  </div>
                </span>
              ),
            },
            {
              key: "provider",
              header: "Provider / 维度",
              width: "170px",
              render: (row) => (
                <span>
                  <StatusBadge tone="info">{row.provider || "all"}</StatusBadge>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                    {row.dimension || "—"}
                    {row.scope_key ? " · " + row.scope_key : ""}
                  </div>
                </span>
              ),
            },
            {
              key: "limit",
              header: "额度",
              width: "120px",
              align: "right",
              render: (row) => (
                <span className="num">
                  {row.limit_usd_display ? fmtUSD(row.limit_usd_display) : "$" + fmtNumber(row.limit_usd)}
                </span>
              ),
            },
            {
              key: "spent",
              header: "已用",
              width: "130px",
              align: "right",
              render: (row) => <span className="num">{fmtUSD(row.spent_usd)}</span>,
            },
            {
              key: "utilization",
              header: "利用率",
              width: "150px",
              render: (row) => {
                const utilization = Number(row.utilization ?? 0);
                const warnAt = Number(row.warn_percent ?? 0) / 100;
                const tone: StatusTone = utilization > 1 ? "danger" : utilization > warnAt ? "warn" : "ok";
                return (
                  <span>
                    <StatusBadge tone={tone}>{(utilization * 100).toFixed(1) + "%"}</StatusBadge>
                    <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>预警 {row.warn_percent ?? "—"}%</div>
                  </span>
                );
              },
            },
            {
              key: "period",
              header: "周期",
              width: "100px",
              render: (row) => <span>{row.period || "—"}</span>,
            },
            {
              key: "action",
              header: "动作 / 阈值",
              width: "160px",
              render: (row) => (
                <span>
                  <div>{row.action || "—"}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>阈值 {row.action_threshold ?? "—"}%</div>
                </span>
              ),
            },
            {
              key: "enabled",
              header: "启用",
              width: "110px",
              render: (row) => (
                <StatusBadge tone={row.enabled ? "ok" : "neutral"}>{row.enabled ? "已启用" : "已停用"}</StatusBadge>
              ),
            },
            {
              key: "revision",
              header: "版本",
              width: "140px",
              render: (row) => (
                <span>
                  <div className="num">rev {row.revision ?? "—"}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{fmtDateTime(row.updated_at)}</div>
                </span>
              ),
            },
            {
              key: "status",
              header: "备注",
              width: "150px",
              render: (row) => (
                <span style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.notes || "—"}</span>
              ),
            },
            {
              key: "actions",
              header: "操作",
              width: "180px",
              align: "right",
              render: (row) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!canUpdate || patch.busy}
                    onClick={() => void openEdit(row)}
                  >
                    编辑
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--ghost"
                    disabled={!canUpdate || patch.busy}
                    onClick={() => void toggleEnabled(row)}
                  >
                    {row.enabled ? "停用" : "启用"}
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
