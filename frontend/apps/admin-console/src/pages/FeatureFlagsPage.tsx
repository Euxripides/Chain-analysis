import { useEffect, useId, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  Field,
  PageHeader,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { fmtDateTime, useAdminList, useAdminPatch, useAdminResource } from "../lib/adminApi";
import { CheckboxRow, JsonBlock, KeyValueList, NumberInput, SelectInput, TextInput } from "../components/s6";

/**
 * A-FLAG-01：功能开关（路由 /feature-flags，权限 admin:flag:read，写入需 admin:flag:update）。
 *
 * 列表：GET /api/admin/v1/feature-flags（含 environment）。
 * 新建 / 更新：POST /api/admin/v1/feature-flags、PATCH /api/admin/v1/feature-flags/{key}（If-Match: revision）。
 * 试算：POST /api/admin/v1/feature-flags/{key}/commands/evaluate；评估记录：GET .../evaluations。
 * 回滚：POST /api/admin/v1/feature-flags/{key}/commands/rollback（百分比归零）。
 * 所有提示、口径与错误只通过弹窗呈现；页面正文只有业务数据、字段标签与操作控件。
 */

interface FlagRow {
  key: string;
  description?: string;
  environment?: string;
  enabled?: boolean;
  default_value?: boolean;
  percentage?: number;
  scope_type?: string;
  scope_org_id?: string;
  scope_workspace_id?: string;
  rules?: unknown[];
  revision?: number;
  updated_at?: string;
}

interface EvaluationRow {
  flag_key?: string;
  flag_revision?: number;
  environment?: string;
  subject_type?: string;
  subject_id?: string;
  result?: boolean;
  reason?: string;
  bucket?: number;
  evaluated_at?: string;
}

interface FlagDraft {
  key: string;
  description: string;
  environment: string;
  enabled: boolean;
  defaultValue: boolean;
  percentage: string;
  scopeType: string;
  scopeOrgId: string;
  scopeWorkspaceId: string;
}

interface EvalDraft {
  subjectId: string;
  subjectType: string;
  organizationId: string;
  workspaceId: string;
}

const SCOPE_OPTIONS = [
  { value: "global", label: "global（全局）" },
  { value: "organization", label: "organization（组织）" },
  { value: "workspace", label: "workspace（工作区）" },
  { value: "user", label: "user（用户）" },
];

const SUBJECT_OPTIONS = [
  { value: "user", label: "user" },
  { value: "organization", label: "organization" },
  { value: "workspace", label: "workspace" },
];

function errorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.status) details.push({ label: "HTTP", value: String(error.status) });
  if (error.code) details.push({ label: "错误码", value: error.code });
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  return details;
}

function draftOf(row: FlagRow): FlagDraft {
  return {
    key: row.key,
    description: row.description ?? "",
    environment: row.environment ?? "",
    enabled: Boolean(row.enabled),
    defaultValue: Boolean(row.default_value),
    percentage: String(row.percentage ?? 0),
    scopeType: row.scope_type ?? "global",
    scopeOrgId: row.scope_org_id ?? "",
    scopeWorkspaceId: row.scope_workspace_id ?? "",
  };
}

/** FlagForm 只在弹窗正文里渲染字段与控件；取值通过 draft.current 回传给页面。 */
function FlagForm({ draft, mode }: { draft: { current: FlagDraft }; mode: "create" | "edit" }) {
  const baseId = useId();
  const [form, setForm] = useState<FlagDraft>(draft.current);
  const update = (patch: Partial<FlagDraft>) => {
    const next = { ...form, ...patch };
    setForm(next);
    draft.current = next;
  };
  return (
    <div className="toolbar" style={{ alignItems: "flex-end" }}>
      {mode === "create" ? (
        <TextInput
          id={baseId + "-key"}
          label="开关 key"
          value={form.key}
          onChange={(value) => update({ key: value })}
          placeholder="例如：fund_flow_v2"
        />
      ) : null}
      <TextInput
        id={baseId + "-description"}
        label="描述"
        value={form.description}
        onChange={(value) => update({ description: value })}
        placeholder="该开关控制的能力"
      />
      <TextInput
        id={baseId + "-env"}
        label="环境"
        value={form.environment}
        onChange={(value) => update({ environment: value })}
        placeholder="all / dev / prod"
      />
      <SelectInput
        id={baseId + "-scope"}
        label="作用域"
        value={form.scopeType}
        onChange={(value) => update({ scopeType: value })}
        options={SCOPE_OPTIONS}
      />
      <NumberInput
        id={baseId + "-percentage"}
        label="灰度百分比"
        value={Number(form.percentage || "0")}
        onChange={(value) => update({ percentage: String(value) })}
        min={0}
        max={100}
        step={1}
      />
      <TextInput
        id={baseId + "-org"}
        label="作用域组织 ID"
        value={form.scopeOrgId}
        onChange={(value) => update({ scopeOrgId: value })}
        placeholder="scope_type=organization 时必填"
      />
      <TextInput
        id={baseId + "-ws"}
        label="作用域工作区 ID"
        value={form.scopeWorkspaceId}
        onChange={(value) => update({ scopeWorkspaceId: value })}
        placeholder="scope_type=workspace 时必填"
      />
      <CheckboxRow id={baseId + "-enabled"} label="启用开关" checked={form.enabled} onChange={(checked) => update({ enabled: checked })} />
      <CheckboxRow
        id={baseId + "-default"}
        label="默认值"
        checked={form.defaultValue}
        onChange={(checked) => update({ defaultValue: checked })}
      />
    </div>
  );
}

/** EvalForm 收集试算主体；取值通过 draft.current 回传给页面。 */
function EvalForm({ draft }: { draft: { current: EvalDraft } }) {
  const baseId = useId();
  const [form, setForm] = useState<EvalDraft>(draft.current);
  const update = (patch: Partial<EvalDraft>) => {
    const next = { ...form, ...patch };
    setForm(next);
    draft.current = next;
  };
  return (
    <div className="toolbar" style={{ alignItems: "flex-end" }}>
      <TextInput
        id={baseId + "-subject"}
        label="主体 ID"
        value={form.subjectId}
        onChange={(value) => update({ subjectId: value })}
        placeholder="user / organization / workspace 的 ID"
      />
      <SelectInput
        id={baseId + "-subject-type"}
        label="主体类型"
        value={form.subjectType}
        onChange={(value) => update({ subjectType: value })}
        options={SUBJECT_OPTIONS}
      />
      <TextInput
        id={baseId + "-org"}
        label="组织 ID"
        value={form.organizationId}
        onChange={(value) => update({ organizationId: value })}
        placeholder="可选"
      />
      <TextInput
        id={baseId + "-ws"}
        label="工作区 ID"
        value={form.workspaceId}
        onChange={(value) => update({ workspaceId: value })}
        placeholder="可选"
      />
    </div>
  );
}

export default function FeatureFlagsPage() {
  const dialog = useDialog();
  const { client, can } = useAuth();
  const list = useAdminList<FlagRow>("/api/admin/v1/feature-flags", {}, 200);
  const patch = useAdminPatch();

  const [evalKey, setEvalKey] = useState<string | null>(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const canUpdate = can("admin:flag:update");
  const environment = String(list.extra.environment ?? "—");
  const flags = list.items;

  useEffect(() => {
    if (evalKey !== null) return;
    if (flags.length === 0) return;
    setEvalKey(flags[0].key);
  }, [evalKey, flags]);

  const evalPath = evalKey
    ? "/api/admin/v1/feature-flags/" + encodeURIComponent(evalKey) + "/evaluations?limit=50"
    : null;
  const evaluationsState = useAdminResource<{ items: EvaluationRow[] }>(evalPath);
  const evaluationRows = evaluationsState.data?.items ?? [];

  const guard = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", "该操作需要 " + permission + "。");
    return false;
  };

  const openCreate = async () => {
    if (!guard("admin:flag:update")) return;
    const draft: { current: FlagDraft } = {
      current: {
        key: "",
        description: "",
        environment,
        enabled: false,
        defaultValue: false,
        percentage: "0",
        scopeType: "global",
        scopeOrgId: "",
        scopeWorkspaceId: "",
      },
    };
    const answer = await dialog.open({
      kind: "confirm",
      title: "新建功能开关",
      body: <FlagForm draft={draft} mode="create" />,
      details: [{ label: "环境", value: environment }],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "下一步", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    if (values.key.trim() === "") {
      dialog.notify("warning", "缺少 key", "功能开关必须提供唯一的 key。");
      return;
    }
    await patch.run({
      path: "/api/admin/v1/feature-flags",
      method: "post",
      body: {
        key: values.key.trim(),
        description: values.description,
        environment: values.environment.trim() || environment,
        enabled: values.enabled,
        default_value: values.defaultValue,
        percentage: Math.round(Number(values.percentage || "0")),
        scope_type: values.scopeType,
        scope_org_id: values.scopeOrgId.trim(),
        scope_workspace_id: values.scopeWorkspaceId.trim(),
      },
      confirmTitle: "创建功能开关",
      confirmBody: "创建后按作用域与灰度百分比生效。",
      confirmLabel: "创建",
      details: [
        { label: "key", value: <span className="mono">{values.key.trim()}</span> },
        { label: "环境", value: values.environment.trim() || environment },
        { label: "作用域", value: values.scopeType },
        { label: "灰度", value: values.percentage + "%" },
      ],
      successTitle: "功能开关已创建",
      onDone: () => list.reload(),
    });
  };

  const openEdit = async (row: FlagRow) => {
    if (!guard("admin:flag:update")) return;
    const draft: { current: FlagDraft } = { current: draftOf(row) };
    const answer = await dialog.open({
      kind: "confirm",
      title: "编辑功能开关 · " + row.key,
      body: <FlagForm draft={draft} mode="edit" />,
      details: [
        { label: "key", value: <span className="mono">{row.key}</span> },
        { label: "当前版本", value: String(row.revision ?? "—") },
        { label: "If-Match 版本", value: String(row.revision ?? "—") },
        { label: "规则", value: <JsonBlock value={row.rules ?? []} label="展开规则" /> },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "下一步", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    await patch.run({
      path: "/api/admin/v1/feature-flags/" + encodeURIComponent(row.key),
      ifMatch: row.revision,
      body: {
        description: values.description,
        environment: values.environment.trim(),
        enabled: values.enabled,
        default_value: values.defaultValue,
        percentage: Math.round(Number(values.percentage || "0")),
        scope_type: values.scopeType,
        scope_org_id: values.scopeOrgId.trim(),
        scope_workspace_id: values.scopeWorkspaceId.trim(),
      },
      confirmTitle: "保存功能开关",
      confirmBody: "变更会写入审计日志，并立即影响后续评估结果。",
      details: [
        { label: "key", value: <span className="mono">{row.key}</span> },
        { label: "If-Match 版本", value: String(row.revision ?? "—") },
        { label: "启用", value: values.enabled ? "是" : "否" },
        { label: "灰度", value: values.percentage + "%" },
        { label: "作用域", value: values.scopeType },
      ],
      successTitle: "功能开关已更新",
      onDone: () => list.reload(),
    });
  };

  const openEvaluate = async (row: FlagRow) => {
    const draft: { current: EvalDraft } = {
      current: { subjectId: "", subjectType: "user", organizationId: "", workspaceId: "" },
    };
    const answer = await dialog.open({
      kind: "confirm",
      title: "试算功能开关 · " + row.key,
      body: <EvalForm draft={draft} />,
      details: [
        { label: "key", value: <span className="mono">{row.key}</span> },
        { label: "当前版本", value: String(row.revision ?? "—") },
        { label: "灰度百分比", value: String(row.percentage ?? 0) + "%" },
        { label: "作用域", value: row.scope_type ?? "global" },
        { label: "评估算法", value: "sha256(key:subject_id) 分桶，结果可复现" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "执行试算", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    if (values.subjectId.trim() === "") {
      dialog.notify("warning", "缺少主体 ID", "试算必须提供主体 ID。");
      return;
    }
    setBusyKey(row.key);
    try {
      const response = await client.post<{
        key?: string;
        revision?: number;
        subject_id?: string;
        result?: boolean;
        reason?: string;
        bucket?: number;
        percentage?: number;
      }>("/api/admin/v1/feature-flags/" + encodeURIComponent(row.key) + "/commands/evaluate", {
        subject_id: values.subjectId.trim(),
        subject_type: values.subjectType,
        organization_id: values.organizationId.trim(),
        workspace_id: values.workspaceId.trim(),
      });
      const data = response.data ?? {};
      dialog.notify("success", "试算完成", null, [
        { label: "key", value: <span className="mono">{data.key ?? row.key}</span> },
        { label: "版本", value: String(data.revision ?? row.revision ?? "—") },
        { label: "主体", value: <span className="mono">{data.subject_id ?? values.subjectId.trim()}</span> },
        { label: "结果", value: <StatusBadge tone={data.result ? "ok" : "neutral"}>{data.result ? "命中（true）" : "未命中（false）"}</StatusBadge> },
        { label: "原因", value: data.reason ?? "—" },
        { label: "分桶", value: String(data.bucket ?? "—") },
        { label: "灰度百分比", value: String(data.percentage ?? row.percentage ?? 0) + "%" },
      ]);
      if (evalKey === row.key) evaluationsState.reload();
      else setEvalKey(row.key);
    } catch (commandError) {
      dialog.notify(
        "error",
        "试算失败",
        commandError instanceof ApiError ? commandError.userMessage : "请稍后重试",
        errorDetails(commandError),
      );
    } finally {
      setBusyKey(null);
    }
  };

  const openRollback = async (row: FlagRow) => {
    if (!guard("admin:flag:update")) return;
    await patch.run({
      path: "/api/admin/v1/feature-flags/" + encodeURIComponent(row.key) + "/commands/rollback",
      method: "post",
      body: { percentage: 0 },
      variant: "danger",
      confirmTitle: "回滚该功能开关？",
      confirmBody: "灰度百分比会归零，开关按默认值评估；操作会写入审计日志。",
      confirmLabel: "回滚",
      details: [
        { label: "key", value: <span className="mono">{row.key}</span> },
        { label: "当前灰度", value: String(row.percentage ?? 0) + "%" },
        { label: "回滚后灰度", value: "0%" },
        { label: "当前版本", value: String(row.revision ?? "—") },
      ],
      successTitle: "功能开关已回滚",
      onDone: () => list.reload(),
    });
  };

  const explainScope = () => {
    dialog.notify("info", "功能开关口径", null, [
      { label: "接口", value: "GET /api/admin/v1/feature-flags" },
      { label: "数据源", value: "postgres:feature_flags + feature_flag_evaluations" },
      { label: "环境", value: environment },
      { label: "灰度", value: "percentage 0-100；0 表示不灰度，100 表示全量" },
      { label: "作用域", value: "global / organization / workspace / user，不匹配时返回默认值" },
      { label: "评估记录", value: "每次试算落库一条，含版本、分桶与原因，可追溯" },
      { label: "并发控制", value: "更新必须携带 If-Match: revision；版本不符返回 412" },
      { label: "回滚", value: "回滚把灰度百分比归零并按默认值评估，历史记录保留" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="功能开关"
        subtitle={"环境 " + environment + " · 共 " + flags.length + " 个开关 · 来源 /api/admin/v1/feature-flags"}
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
              新建开关
            </button>
          </>
        }
      />

      <Card title={"开关（" + flags.length + "）"} flush>
        <DataTable<FlagRow>
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          rows={flags}
          rowKey={(row) => row.key}
          empty={<EmptyState title="没有配置任何功能开关" />}
          columns={[
            {
              key: "key",
              header: "key / 描述",
              width: "260px",
              render: (row) => (
                <span>
                  <strong className="mono">{row.key}</strong>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.description || "—"}</div>
                </span>
              ),
            },
            {
              key: "environment",
              header: "环境",
              width: "110px",
              render: (row) => <StatusBadge tone="info">{row.environment || "all"}</StatusBadge>,
            },
            {
              key: "enabled",
              header: "启用",
              width: "140px",
              render: (row) => (
                <span>
                  <StatusBadge tone={row.enabled ? "ok" : "neutral"}>{row.enabled ? "已启用" : "已停用"}</StatusBadge>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>默认 {row.default_value ? "true" : "false"}</div>
                </span>
              ),
            },
            {
              key: "percentage",
              header: "灰度",
              width: "110px",
              align: "right",
              render: (row) => <span className="num">{String(row.percentage ?? 0) + "%"}</span>,
            },
            {
              key: "scope",
              header: "作用域",
              width: "220px",
              render: (row) => (
                <span>
                  <div>{row.scope_type || "global"}</div>
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }} title={(row.scope_org_id || "") + " " + (row.scope_workspace_id || "")}>
                    {row.scope_org_id || row.scope_workspace_id || "—"}
                  </div>
                </span>
              ),
            },
            {
              key: "revision",
              header: "版本",
              width: "150px",
              render: (row) => (
                <span>
                  <div className="num">rev {row.revision ?? "—"}</div>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{fmtDateTime(row.updated_at)}</div>
                </span>
              ),
            },
            {
              key: "actions",
              header: "操作",
              width: "240px",
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
                    className="btn btn--sm"
                    disabled={busyKey === row.key}
                    onClick={() => void openEvaluate(row)}
                  >
                    试算
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--ghost"
                    disabled={!canUpdate || patch.busy}
                    onClick={() => void openRollback(row)}
                  >
                    回滚
                  </button>
                </span>
              ),
            },
          ]}
        />
      </Card>

      <Card
        title={"评估记录 · " + (evalKey ?? "未选择开关")}
        actions={
          <button
            type="button"
            className="btn btn--sm"
            disabled={!evalKey || evaluationsState.loading}
            onClick={evaluationsState.reload}
          >
            刷新
          </button>
        }
        flush
      >
        <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)" }}>
          <div style={{ flex: "0 1 240px", minWidth: 0 }}>
            <Field label="开关" htmlFor="flag-eval-key">
              <select
                id="flag-eval-key"
                className="select"
                value={evalKey ?? ""}
                onChange={(event) => setEvalKey(event.target.value || null)}
              >
                {flags.length === 0 ? <option value="">未选择</option> : null}
                {flags.map((flag) => (
                  <option key={flag.key} value={flag.key}>
                    {flag.key}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </div>
        <DataTable<EvaluationRow>
            loading={evaluationsState.loading}
            error={evaluationsState.error}
            onRetry={evaluationsState.reload}
            rows={evaluationRows}
            rowKey={(row) => String(row.subject_id ?? "") + "-" + String(row.evaluated_at ?? "")}
            empty={<EmptyState title="该开关还没有评估记录" />}
            columns={[
              {
                key: "result",
                header: "结果",
                width: "120px",
                render: (row) => (
                  <StatusBadge tone={row.result ? "ok" : "neutral"}>{row.result ? "true" : "false"}</StatusBadge>
                ),
              },
              {
                key: "subject",
                header: "主体",
                width: "240px",
                render: (row) => (
                  <span>
                    <div className="mono" style={{ fontSize: "var(--fs-12)" }}>{row.subject_id || "—"}</div>
                    <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.subject_type || "—"}</div>
                  </span>
                ),
              },
              { key: "reason", header: "原因", width: "220px", render: (row) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.reason || "—"}</span> },
              { key: "bucket", header: "分桶", width: "90px", align: "right", render: (row) => <span className="num">{row.bucket ?? "—"}</span> },
              { key: "revision", header: "版本", width: "90px", align: "right", render: (row) => <span className="num">rev {row.flag_revision ?? "—"}</span> },
              { key: "environment", header: "环境", width: "110px", render: (row) => <span>{row.environment || "—"}</span> },
              { key: "evaluated_at", header: "评估时间", width: "180px", render: (row) => <span>{fmtDateTime(row.evaluated_at)}</span> },
          ]}
        />
      </Card>
    </>
  );
}
