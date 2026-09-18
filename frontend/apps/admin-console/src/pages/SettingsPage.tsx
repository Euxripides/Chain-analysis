import { useEffect, useId, useMemo, useState, type ReactNode } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  dialogInputValue,
  EmptyState,
  ErrorState,
  Field,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { fmtDateTime, useAdminList } from "../lib/adminApi";
import { CheckboxRow, JsonBlock, KeyValueList, NumberInput, TextInput } from "../components/s6";

/**
 * A-CONFIG-01：系统设置（路由 /settings，权限 admin:config:read）。
 *
 * 数据来源：GET /api/admin/v1/settings?category=（configuration_settings）。
 * 修改：PATCH /api/admin/v1/settings/{key}（If-Match: revision；secret-ref 必须 step_up_password）。
 * 历史：GET /api/admin/v1/config/revisions；回滚：POST /api/admin/v1/settings/{key}/commands/rollback。
 * 对比：POST /api/admin/v1/settings/{key}/commands/diff。
 * 所有提示、冲突说明与错误只通过弹窗呈现；页面正文只有业务数据、字段标签与操作控件。
 */

interface Setting {
  key: string;
  category?: string;
  value_type?: string;
  value?: unknown;
  secret_ref?: string;
  is_secret?: boolean;
  description?: string;
  revision?: number;
  updated_at?: string;
  source?: string;
}

interface RevisionRow {
  id?: string;
  key?: string;
  revision?: number;
  value?: unknown;
  secret_ref?: string;
  is_secret?: boolean;
  before_hash?: string;
  after_hash?: string;
  change_note?: string;
  actor_email?: string;
  request_id?: string;
  trace_id?: string;
  approval_id?: string;
  created_at?: string;
}

interface SettingDraft {
  valueText: string;
  boolValue: boolean;
  secretRef: string;
  reason: string;
}

/** GET /api/auth/v1/methods 的只读认证能力声明（既有的公开端点，不含任何密钥）。 */
interface AuthMethods {
  local?: boolean;
  oidc?: boolean;
  /** AUTH_REQUIRE_ADMIN_MFA：后台是否强制 MFA。 */
  mfa_required?: boolean;
  audiences?: string[];
}

function isSecretSetting(setting: Setting): boolean {
  return Boolean(setting.is_secret) || setting.value_type === "secret-ref";
}

function renderValue(value: unknown): ReactNode {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return <JsonBlock value={value} label="展开值" />;
  return <span className="mono">{String(value)}</span>;
}

function valueToText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (typeof value === "object") return JSON.stringify(value, null, 2);
  return String(value);
}

function errorDetails(error: unknown): Array<{ label: string; value: ReactNode }> {
  if (!(error instanceof ApiError)) return [];
  const details: Array<{ label: string; value: ReactNode }> = [];
  if (error.status) details.push({ label: "HTTP", value: String(error.status) });
  if (error.code) details.push({ label: "错误码", value: error.code });
  if (error.traceId) details.push({ label: "Trace", value: error.traceId });
  if (error.details) {
    Object.entries(error.details).forEach(([key, value]) => {
      details.push({ label: key, value: typeof value === "object" ? JSON.stringify(value) : String(value) });
    });
  }
  return details;
}

/** SettingValueField 依据 value_type 渲染对应控件；secret-ref 只接受引用名。 */
function SettingValueField({
  setting,
  draft,
  onChange,
}: {
  setting: Setting;
  draft: SettingDraft;
  onChange: (patch: Partial<SettingDraft>) => void;
}) {
  const baseId = useId();
  if (isSecretSetting(setting)) {
    return (
      <TextInput
        id={baseId + "-secret"}
        label="secret_ref 引用名"
        value={draft.secretRef}
        onChange={(value) => onChange({ secretRef: value })}
        placeholder="例如：ANKR_API_KEY"
      />
    );
  }
  if (setting.value_type === "bool") {
    return (
      <CheckboxRow
        id={baseId + "-bool"}
        label="布尔值"
        checked={draft.boolValue}
        onChange={(checked) => onChange({ boolValue: checked })}
      />
    );
  }
  if (setting.value_type === "int") {
    return (
      <NumberInput
        id={baseId + "-int"}
        label="整数值"
        value={Number(draft.valueText || "0")}
        onChange={(value) => onChange({ valueText: String(value) })}
        step={1}
      />
    );
  }
  return (
    <TextInput
      id={baseId + "-text"}
      label={setting.value_type === "json" ? "JSON 值" : "字符串值"}
      value={draft.valueText}
      onChange={(value) => onChange({ valueText: value })}
    />
  );
}

/** SettingForm 只在弹窗正文里渲染字段与控件；取值通过 draft.current 回传给页面。 */
function SettingForm({ setting, draft }: { setting: Setting; draft: { current: SettingDraft } }) {
  const baseId = useId();
  const [form, setForm] = useState<SettingDraft>(draft.current);
  const update = (patch: Partial<SettingDraft>) => {
    const next = { ...form, ...patch };
    setForm(next);
    draft.current = next;
  };
  return (
    <div className="toolbar" style={{ alignItems: "flex-end" }}>
      <SettingValueField setting={setting} draft={form} onChange={update} />
      <TextInput
        id={baseId + "-reason"}
        label="变更原因"
        value={form.reason}
        onChange={(value) => update({ reason: value })}
        placeholder="会写入配置版本历史"
      />
    </div>
  );
}

/** RevisionPicker 在弹窗内选择要回滚到的版本；选择结果同步写回 draft.current。 */
function RevisionPicker({
  revisions,
  draft,
  selectable = true,
}: {
  revisions: RevisionRow[];
  draft: { current: number | null };
  selectable?: boolean;
}) {
  const baseId = useId();
  const [selected, setSelected] = useState<number | null>(draft.current);
  const choose = (revision: number | null) => {
    setSelected(revision);
    draft.current = revision;
  };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
      {revisions.map((row) => (
        <label
          key={String(row.id ?? row.revision)}
          htmlFor={baseId + "-rev-" + String(row.revision)}
          style={{ display: "flex", gap: "var(--sp-2)", alignItems: "flex-start", cursor: selectable ? "pointer" : "default" }}
        >
          {selectable ? (
            <input
              id={baseId + "-rev-" + String(row.revision)}
              type="radio"
              name={baseId + "-revision"}
              checked={selected === row.revision}
              onChange={() => choose(row.revision ?? null)}
            />
          ) : (
            <span aria-hidden="true" style={{ color: "var(--c-slate)" }}>
              •
            </span>
          )}
          <span style={{ minWidth: 0 }}>
            <span className="mono">rev {row.revision ?? "—"}</span>
            {" · "}
            <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{fmtDateTime(row.created_at)}</span>
            {" · "}
            <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>{row.actor_email || "—"}</span>
            <div style={{ fontSize: "var(--fs-12)", wordBreak: "break-word" }}>
              {row.is_secret ? "secret_ref " + (row.secret_ref || "—") : valueToText(row.value) || "—"}
            </div>
            {row.change_note ? (
              <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", wordBreak: "break-word" }}>{row.change_note}</div>
            ) : null}
          </span>
        </label>
      ))}
    </div>
  );
}

export default function SettingsPage() {
  const dialog = useDialog();
  const { client, can } = useAuth();
  const list = useAdminList<Setting>("/api/admin/v1/settings", {}, 200);

  const [category, setCategory] = useState("all");
  const [categories, setCategories] = useState<string[]>([]);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const canUpdate = can("admin:config:update");
  const canHistory = can("admin:config:history:read");

  /**
   * 认证能力只读面板的数据。
   *
   * 来源仅限既有公开端点 GET /api/auth/v1/methods（不含密钥、不需要新后端）。
   * 读取失败时保持"—"，不打断配置页的主任务，也不在页面里贴提示条（AGENTS.md）。
   */
  const [authMethods, setAuthMethods] = useState<AuthMethods | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    client
      .get<AuthMethods>("/api/auth/v1/methods", { signal: controller.signal })
      .then((response) => setAuthMethods(response.data ?? null))
      .catch(() => undefined);
    return () => controller.abort();
  }, [client]);

  useEffect(() => {
    if (category !== "all" || list.items.length === 0) return;
    setCategories((current) => {
      const next = new Set(current);
      list.items.forEach((item) => {
        if (item.category) next.add(item.category);
      });
      const merged = Array.from(next).sort();
      return merged.length === current.length ? current : merged;
    });
  }, [category, list.items]);

  const grouped = useMemo(() => {
    const map = new Map<string, Setting[]>();
    list.items.forEach((item) => {
      const key = item.category || "未分类";
      const bucket = map.get(key);
      if (bucket) bucket.push(item);
      else map.set(key, [item]);
    });
    return Array.from(map.entries()).sort((left, right) => left[0].localeCompare(right[0]));
  }, [list.items]);

  const guard = (permission: string): boolean => {
    if (can(permission)) return true;
    dialog.notify("warning", "缺少控制权限", "该操作需要 " + permission + "。");
    return false;
  };

  const saveSetting = async (setting: Setting) => {
    if (!guard("admin:config:update")) return;
    const secret = isSecretSetting(setting);
    const draft: { current: SettingDraft } = {
      current: {
        valueText: secret ? "" : valueToText(setting.value),
        boolValue: Boolean(setting.value),
        secretRef: "",
        reason: "",
      },
    };
    const answer = await dialog.open({
      kind: "confirm",
      title: "修改配置 · " + setting.key,
      body: <SettingForm setting={setting} draft={draft} />,
      details: [
        { label: "配置键", value: <span className="mono">{setting.key}</span> },
        { label: "分类", value: setting.category || "—" },
        { label: "类型", value: setting.value_type || "—" },
        { label: "当前版本", value: String(setting.revision ?? "—") },
        { label: "If-Match 版本", value: String(setting.revision ?? "—") },
        { label: "当前值", value: secret ? <span className="mono">{setting.secret_ref || "—"}</span> : renderValue(setting.value) },
        { label: "数据来源", value: setting.source || "configuration_settings" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "保存", result: "ok", variant: "primary" },
      ],
    });
    if (answer !== "ok") return;
    const values = draft.current;
    if (values.reason.trim() === "") {
      dialog.notify("warning", "缺少变更原因", "变更原因会写入配置版本历史，不能为空。");
      return;
    }
    const body: Record<string, unknown> = { reason: values.reason.trim() };
    if (secret) {
      if (values.secretRef.trim() === "") {
        dialog.notify("warning", "缺少引用名", "secret 类型配置只接受 secret_ref 引用名，不接受明文。");
        return;
      }
      body.secret_ref = values.secretRef.trim();
    } else if (setting.value_type === "bool") {
      body.value = values.boolValue;
    } else if (setting.value_type === "int") {
      const parsed = Number(values.valueText);
      if (!Number.isFinite(parsed) || !Number.isInteger(parsed)) {
        dialog.notify("warning", "取值不合法", "该配置项为整数类型，请输入整数。");
        return;
      }
      body.value = parsed;
    } else if (setting.value_type === "json") {
      try {
        body.value = values.valueText.trim() === "" ? null : JSON.parse(values.valueText);
      } catch {
        dialog.notify("warning", "JSON 不合法", "请输入合法的 JSON 内容。");
        return;
      }
    } else {
      body.value = values.valueText;
    }
    if (secret) {
      const password = dialogInputValue(
        await dialog.prompt({
          kind: "warning",
          title: "二次身份确认",
          body: "修改 secret 类型配置需要在当前会话重新确认身份。密码只用于本次校验，不会写入日志。",
          input: {
            label: "当前账号密码",
            placeholder: "请输入当前账号密码",
            pattern: /^.{1,200}$/,
            patternHint: "请输入当前账号密码",
          },
          submitLabel: "确认身份并保存",
        }),
      );
      if (!password) return;
      body.step_up_password = password;
    }
    setBusyKey(setting.key);
    try {
      const response = await client.patch<Setting>("/api/admin/v1/settings/" + encodeURIComponent(setting.key), body, {
        ifMatch: String(setting.revision ?? ""),
      });
      dialog.notify("success", "配置已保存", null, [
        { label: "配置键", value: <span className="mono">{setting.key}</span> },
        { label: "新版本", value: String(response.data?.revision ?? "—") },
        { label: "类型", value: response.data?.value_type ?? setting.value_type ?? "—" },
      ]);
      list.reload();
    } catch (patchError) {
      if (patchError instanceof ApiError && (patchError.status === 412 || patchError.status === 428)) {
        dialog.notify(
          "error",
          patchError.status === 412 ? "版本冲突，未保存" : "缺少 If-Match 版本",
          patchError.userMessage,
          errorDetails(patchError),
        );
      } else {
        dialog.notify(
          "error",
          "保存配置失败",
          patchError instanceof ApiError ? patchError.userMessage : "请稍后重试",
          errorDetails(patchError),
        );
      }
    } finally {
      setBusyKey(null);
    }
  };

  const openHistory = async (setting: Setting) => {
    if (!guard("admin:config:history:read")) return;
    setBusyKey(setting.key);
    let revisions: RevisionRow[] = [];
    try {
      const response = await client.get<{ items: RevisionRow[] }>("/api/admin/v1/config/revisions", {
        query: { key: setting.key, limit: 50 },
      });
      revisions = response.data?.items ?? [];
    } catch (loadError) {
      dialog.notify(
        "error",
        "读取配置历史失败",
        loadError instanceof ApiError ? loadError.userMessage : "请稍后重试",
        errorDetails(loadError),
      );
      setBusyKey(null);
      return;
    }
    setBusyKey(null);
    const draft: { current: number | null } = { current: null };
    const answer = await dialog.open({
      kind: "info",
      title: "配置版本历史 · " + setting.key,
      body:
        revisions.length === 0 ? (
          <p style={{ margin: 0 }}>该配置项还没有历史版本。</p>
        ) : (
          <RevisionPicker revisions={revisions} draft={draft} />
        ),
      details: [
        { label: "配置键", value: <span className="mono">{setting.key}</span> },
        { label: "当前版本", value: String(setting.revision ?? "—") },
        { label: "历史条数", value: String(revisions.length) },
        { label: "接口", value: "GET /api/admin/v1/config/revisions" },
      ],
      actions: [
        { label: "关闭", result: "", variant: "default" },
        { label: "回滚到选中版本", result: "rollback", variant: "danger" },
      ],
    });
    if (answer !== "rollback") return;
    await rollback(setting, draft.current, revisions);
  };

  const rollback = async (setting: Setting, revision: number | null, revisions: RevisionRow[]) => {
    if (!guard("admin:config:update")) return;
    if (revision === null) {
      dialog.notify("warning", "未选择版本", "请先在历史列表中选择要回滚到的版本。");
      return;
    }
    const target = revisions.find((row) => row.revision === revision);
    const reason = dialogInputValue(
      await dialog.prompt({
        kind: "warning",
        title: "回滚配置版本",
        body: "回滚会生成新的版本号，历史版本保持不可变。",
        input: {
          label: "回滚原因",
          placeholder: "例如：新值导致上游 429，回退到稳定版本",
          pattern: /^.{1,500}$/,
          patternHint: "1-500 个字符",
        },
        submitLabel: "继续",
      }),
    );
    if (!reason) return;
    const body: Record<string, unknown> = { revision, reason };
    if (target?.is_secret || isSecretSetting(setting)) {
      const password = dialogInputValue(
        await dialog.prompt({
          kind: "warning",
          title: "二次身份确认",
          body: "回滚 secret 类型配置需要在当前会话重新确认身份。",
          input: {
            label: "当前账号密码",
            placeholder: "请输入当前账号密码",
            pattern: /^.{1,200}$/,
            patternHint: "请输入当前账号密码",
          },
          submitLabel: "确认身份并回滚",
        }),
      );
      if (!password) return;
      body.step_up_password = password;
    }
    setBusyKey(setting.key);
    try {
      const response = await client.post<Setting>(
        "/api/admin/v1/settings/" + encodeURIComponent(setting.key) + "/commands/rollback",
        body,
      );
      dialog.notify("success", "配置已回滚", null, [
        { label: "配置键", value: <span className="mono">{setting.key}</span> },
        { label: "回滚到版本", value: String(revision) },
        { label: "新版本", value: String(response.data?.revision ?? "—") },
      ]);
      list.reload();
    } catch (commandError) {
      dialog.notify(
        "error",
        "回滚失败",
        commandError instanceof ApiError ? commandError.userMessage : "请稍后重试",
        errorDetails(commandError),
      );
    } finally {
      setBusyKey(null);
    }
  };

  const openDiff = async (setting: Setting) => {
    if (!guard("admin:config:update")) return;
    setBusyKey(setting.key);
    try {
      const response = await client.post<{ key: string; current: Setting; revisions: RevisionRow[] }>(
        "/api/admin/v1/settings/" + encodeURIComponent(setting.key) + "/commands/diff",
        { reason: "查看配置差异" },
      );
      const current = response.data?.current ?? setting;
      const revisions = response.data?.revisions ?? [];
      await dialog.open({
        kind: "info",
        title: "配置对比 · " + setting.key,
        body: (
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
            <KeyValueList
              items={[
                { label: "当前版本", value: String(current.revision ?? "—") },
                { label: "当前值", value: isSecretSetting(current) ? current.secret_ref || "—" : renderValue(current.value) },
                { label: "最近更新", value: fmtDateTime(current.updated_at) },
                { label: "历史条数", value: String(revisions.length) },
              ]}
            />
            <RevisionPicker revisions={revisions} draft={{ current: null }} selectable={false} />
          </div>
        ),
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      });
    } catch (commandError) {
      dialog.notify(
        "error",
        "读取配置差异失败",
        commandError instanceof ApiError ? commandError.userMessage : "请稍后重试",
        errorDetails(commandError),
      );
    } finally {
      setBusyKey(null);
    }
  };

  const explainAuth = () => {
    dialog.notify("info", "认证设置取值口径", null, [
      { label: "接口", value: "GET /api/auth/v1/methods（公开只读，无密钥）" },
      { label: "本地账户", value: "local：邮箱 + 密码（Argon2id）登录是否可用" },
      { label: "组织账号", value: "oidc：未配置时后端返回 501，前端不伪装可用" },
      { label: "后台强制 MFA", value: "AUTH_REQUIRE_ADMIN_MFA：true 时后台账号登录必须完成 MFA 第二步" },
      { label: "修改方式", value: "部署级环境变量，本页只读；不在配置中心里改" },
      { label: "账号注册", value: "各账号是否已绑定认证器由身份本人在用户端自助管理" },
    ]);
  };

  const explainScope = () => {
    dialog.notify("info", "配置口径", null, [
      { label: "接口", value: "GET /api/admin/v1/settings" },
      { label: "数据源", value: "postgres:configuration_settings" },
      { label: "类型", value: "string / int / bool / json / secret-ref" },
      { label: "secret", value: "后端恒返回 value=null，只暴露 secret_ref 摘要；修改必须二次身份确认" },
      { label: "并发控制", value: "修改必须携带 If-Match: revision；缺版本返回 428，版本不符返回 412" },
      { label: "历史", value: "configuration_revisions 为 append-only，回滚会生成新版本而不是改写历史" },
    ]);
  };

  return (
    <>
      <PageHeader
        title="系统设置"
        subtitle={"共 " + list.items.length + " 项 · 分类 " + grouped.length + " 组 · 来源 /api/admin/v1/settings"}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainScope}>
              口径
            </button>
            <button type="button" className="btn btn--sm" onClick={list.reload} disabled={list.loading}>
              刷新
            </button>
          </>
        }
      />

      <Card
        title="认证设置（只读）"
        actions={
          <button type="button" className="btn btn--sm" onClick={explainAuth}>
            口径
          </button>
        }
      >
        <div className="metric-grid">
          <Metric
            label="本地账户登录"
            value={authMethods ? (authMethods.local ? "启用" : "停用") : "—"}
            tone={authMethods?.local ? "ok" : "neutral"}
            hint="邮箱 + 密码（Argon2id）"
          />
          <Metric
            label="组织账号（OIDC）"
            value={authMethods ? (authMethods.oidc ? "启用" : "未配置") : "—"}
            tone={authMethods?.oidc ? "ok" : "neutral"}
            hint="未配置时后端显式返回 501"
          />
          <Metric
            label="后台强制 MFA"
            value={authMethods ? (authMethods.mfa_required ? "要求" : "未要求") : "—"}
            tone={authMethods?.mfa_required ? "warn" : "neutral"}
            hint="AUTH_REQUIRE_ADMIN_MFA"
          />
          <Metric
            label="会话受众"
            value={authMethods?.audiences?.join(" / ") || "—"}
            hint="用户层与后台会话互不通用"
          />
        </div>
      </Card>

      <Card title="筛选">
        <div className="toolbar" style={{ alignItems: "flex-end" }}>
          <div style={{ flex: "0 1 220px", minWidth: 0 }}>
            <Field label="分类" htmlFor="settings-category">
              <select
                id="settings-category"
                className="select"
                value={category}
                onChange={(event) => {
                  const value = event.target.value;
                  setCategory(value);
                  list.setFilter({ category: value === "all" ? undefined : value });
                }}
              >
                <option value="all">全部分类</option>
                {categories.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </Field>
          </div>
        </div>
      </Card>

      {list.loading && list.items.length === 0 ? (
        <Card>
          <SkeletonRows rows={6} columns={5} />
        </Card>
      ) : null}

      {list.error && list.items.length === 0 ? (
        <Card>
          <ErrorState title="配置读取失败" message={list.error} onRetry={list.reload} />
        </Card>
      ) : null}

      {!list.error && !list.loading && grouped.length === 0 ? (
        <Card>
          <EmptyState title="没有符合条件的配置项" />
        </Card>
      ) : null}

      {grouped.map(([groupName, items]) => (
        <Card key={groupName} title={groupName + "（" + items.length + "）"} flush>
          <DataTable<Setting>
            rows={items}
            rowKey={(row) => row.key}
            columns={[
              {
                key: "key",
                header: "配置键",
                width: "260px",
                render: (row) => (
                  <span>
                    <strong className="mono">{row.key}</strong>
                    <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.description || "—"}</div>
                  </span>
                ),
              },
              {
                key: "type",
                header: "类型",
                width: "120px",
                render: (row) => (
                  <StatusBadge tone={isSecretSetting(row) ? "warn" : "neutral"}>
                    {row.value_type || "—"}
                  </StatusBadge>
                ),
              },
              {
                key: "value",
                header: "当前值",
                width: "240px",
                render: (row) =>
                  isSecretSetting(row) ? (
                    <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
                      {row.secret_ref || "—"}
                    </span>
                  ) : (
                    renderValue(row.value)
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
                key: "source",
                header: "来源",
                width: "160px",
                render: (row) => (
                  <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{row.source || "configuration_settings"}</span>
                ),
              },
              {
                key: "actions",
                header: "操作",
                width: "220px",
                align: "right",
                render: (row) => (
                  <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                    <button
                      type="button"
                      className="btn btn--sm"
                      disabled={!canUpdate || busyKey === row.key}
                      onClick={() => void saveSetting(row)}
                    >
                      编辑
                    </button>
                    <button
                      type="button"
                      className="btn btn--sm"
                      disabled={!canHistory || busyKey === row.key}
                      onClick={() => void openHistory(row)}
                    >
                      历史
                    </button>
                    <button
                      type="button"
                      className="btn btn--sm btn--ghost"
                      disabled={!canUpdate || busyKey === row.key}
                      onClick={() => void openDiff(row)}
                    >
                      对比
                    </button>
                  </span>
                ),
              },
            ]}
          />
        </Card>
      ))}
    </>
  );
}
