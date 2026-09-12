import { useMemo, type ReactNode } from "react";
import { useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  Metric,
  PageHeader,
  StatusBadge,
  statusToneFor,
  type StatusTone,
  useDialog,
} from "@chain/ui";
import {
  fmtDateTime,
  fmtNumber,
  useAdminCommand,
  useAdminList,
} from "../lib/adminApi";

/**
 * A-KEY-01：API / MCP Key 管理（路由 /api-keys，读取权限 admin:apikey:read）。
 *
 * 真实接口：
 *   GET  /api/admin/v1/api-keys
 *   POST /api/admin/v1/api-keys
 *   POST /api/admin/v1/api-keys/{id}/commands/{rotate|revoke}
 *
 * 明文密钥只在创建 / 轮换成功的弹窗中出现一次；列表接口只返回前 10 位前缀。
 */

interface ApiKeyRow {
  id: string;
  name: string;
  key_prefix: string;
  scopes: string[];
  rate_limit_per_minute: number;
  allowed_ips: string[];
  expires_at?: string | null;
  last_used_at?: string | null;
  created_at: string;
  revoked_at?: string | null;
  revoke_reason?: string;
  status: string;
  description: string;
  revision: number;
  plaintext_available: boolean;
}

function text(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function keyTone(status: string): StatusTone {
  switch (status) {
    case "active":
      return "ok";
    case "expired":
      return "warn";
    case "revoked":
    case "disabled":
      return "danger";
    default:
      return statusToneFor(status);
  }
}

function scopesText(scopes: unknown): string {
  return Array.isArray(scopes) && scopes.length > 0 ? scopes.map((scope) => text(scope)).join("、") : "—";
}

function ipText(ips: unknown): string {
  return Array.isArray(ips) && ips.length > 0 ? ips.map((ip) => text(ip)).join("、") : "不限制";
}

export default function ApiKeysPage() {
  const dialog = useDialog();
  const { can } = useAuth();
  const list = useAdminList<ApiKeyRow>("/api/admin/v1/api-keys", {}, 50);
  const command = useAdminCommand();

  const rows = list.items;

  const summary = useMemo(() => {
    const active = rows.filter((row) => row.status === "active").length;
    const revoked = rows.filter((row) => row.status === "revoked").length;
    const soon = rows.filter((row) => {
      if (!row.expires_at || row.status === "revoked") return false;
      const diff = new Date(row.expires_at).getTime() - Date.now();
      return diff > 0 && diff < 30 * 24 * 3600 * 1000;
    }).length;
    return { active, revoked, soon };
  }, [rows]);

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
    dialog.notify("info", "API Key 口径", null, [
      { label: "数据来源", value: "postgres:gpt_action_api_keys（最近 200 条，按创建时间倒序）" },
      { label: "key_prefix", value: "明文前 10 位，用于识别；完整密钥只以 SHA-256 存储" },
      { label: "明文回显", value: "只在创建 / 轮换成功后的弹窗出现一次，关闭后无法再次获取" },
      { label: "status", value: "revoked_at 优先，其次 disabled_at，最后按 expires_at 判定 expired" },
      { label: "allowed_ips", value: "为空表示不限制来源 IP" },
      { label: "读取权限", value: "admin:apikey:read" },
      { label: "创建权限", value: "admin:apikey:create（需要填写原因）" },
      { label: "轮换 / 吊销权限", value: "admin:apikey:revoke（都需要二次身份确认）" },
    ]);

  const plaintextDetails = (
    data: Record<string, unknown>,
    actionLabel: string,
  ): Array<{ label: string; value: ReactNode }> => [
    { label: "名称", value: text(data.name) },
    { label: "key_prefix", value: <span className="mono">{text(data.key_prefix)}</span> },
    {
      label: "明文密钥",
      value: <span className="mono">{text(data.secret)}</span>,
    },
    { label: "明文说明", value: "只显示一次，关闭后无法再次获取" },
    { label: "scopes", value: scopesText(data.scopes) },
    { label: "每分钟限流", value: text(data.rate_limit_per_minute) },
    { label: "操作", value: actionLabel },
  ];

  const createKey = async () => {
    if (!requirePermission("admin:apikey:create")) return;

    const name = await askText({
      kind: "info",
      title: "新建 API Key · 名称",
      body: "名称会出现在列表、审计与调用方文档中。",
      input: { label: "名称", placeholder: "datasource-readonly", pattern: /^.{1,120}$/, patternHint: "1-120 个字符" },
      submitLabel: "下一步",
    });
    if (name.cancelled || name.value === "") return;

    const scopesRaw = await askText({
      kind: "info",
      title: "新建 API Key · scopes",
      body: "逗号分隔的权限范围，例如 actions:read, mcp:invoke。",
      input: { label: "scopes", placeholder: "actions:read,mcp:invoke", pattern: /^.{1,300}$/, patternHint: "1-300 个字符，逗号分隔" },
      submitLabel: "下一步",
    });
    if (scopesRaw.cancelled || scopesRaw.value === "") return;
    const scopes = scopesRaw.value.split(",").map((scope) => scope.trim()).filter(Boolean);

    const limitRaw = await askText({
      kind: "info",
      title: "新建 API Key · 每分钟限流",
      body: "超过该速率后服务端返回 429。",
      input: { label: "rate_limit_per_minute", defaultValue: "60", pattern: /^\d{1,7}$/, patternHint: "整数，例如 60" },
      submitLabel: "下一步",
    });
    if (limitRaw.cancelled || limitRaw.value === "") return;

    const expiresRaw = await askText({
      kind: "info",
      title: "新建 API Key · 有效期",
      body: "单位为天；0 表示不过期。",
      input: { label: "expires_in_days", defaultValue: "90", pattern: /^\d{1,4}$/, patternHint: "0-9999 的整数，0 表示不过期" },
      submitLabel: "下一步",
    });
    if (expiresRaw.cancelled || expiresRaw.value === "") return;

    const ipsRaw = await askText({
      kind: "info",
      title: "新建 API Key · 允许的 IP",
      body: "逗号分隔的 CIDR 或 IP；留空表示不限制来源。",
      input: { label: "allowed_ips", pattern: /^[-0-9a-fA-F:.,/\s]{0,500}$/, patternHint: "逗号分隔的 IP / CIDR，或留空" },
      submitLabel: "下一步",
    });
    if (ipsRaw.cancelled) return;
    const allowedIPs = ipsRaw.value === "" ? [] : ipsRaw.value.split(",").map((ip) => ip.trim()).filter(Boolean);

    const description = await askText({
      kind: "info",
      title: "新建 API Key · 描述",
      body: "描述用于说明用途，可留空。",
      input: { label: "description", pattern: /^.{0,300}$/, patternHint: "最多 300 个字符，或留空" },
      submitLabel: "完成",
    });
    if (description.cancelled) return;

    await command.run({
      path: "/api/admin/v1/api-keys",
      body: {
        name: name.value,
        scopes,
        rate_limit_per_minute: Number(limitRaw.value),
        allowed_ips: allowedIPs,
        expires_in_days: Number(expiresRaw.value),
        description: description.value,
      },
      confirmTitle: `创建 API Key · ${name.value}`,
      confirmBody: "创建后会返回一次明文密钥：只显示一次，关闭弹窗后无法再次获取，请立即交给调用方保管。",
      confirmLabel: "创建密钥",
      details: [
        { label: "名称", value: name.value },
        { label: "scopes", value: scopes.join("、") },
        { label: "每分钟限流", value: limitRaw.value },
        { label: "有效期", value: Number(expiresRaw.value) === 0 ? "不过期" : `${expiresRaw.value} 天` },
        { label: "允许 IP", value: ipText(allowedIPs) },
        { label: "描述", value: description.value || "—" },
      ],
      successTitle: "API Key 已创建",
      successDetails: (data) => plaintextDetails(data, "创建"),
      onDone: () => list.reload(),
    });
  };

  const rotateKey = async (row: ApiKeyRow) => {
    if (!requirePermission("admin:apikey:revoke")) return;
    const expires = await askText({
      kind: "warning",
      title: `轮换 API Key · ${row.name}`,
      body: "轮换会立即吊销旧密钥并生成新密钥，新明文只显示一次；0 表示新密钥不过期。",
      input: {
        label: "expires_in_days",
        defaultValue: row.expires_at ? "90" : "0",
        pattern: /^\d{1,4}$/,
        patternHint: "0-9999 的整数，0 表示不过期",
      },
      submitLabel: "下一步",
    });
    if (expires.cancelled || expires.value === "") return;
    await command.run({
      path: `/api/admin/v1/api-keys/${encodeURIComponent(row.id)}/commands/rotate`,
      body: { expires_in_days: Number(expires.value) },
      variant: "danger",
      requireStepUp: true,
      confirmTitle: `确认轮换 API Key · ${row.name}`,
      confirmBody: "旧密钥会立即失效，使用它的调用方需要换成新密钥；新明文只显示一次。",
      confirmLabel: "轮换",
      details: [
        { label: "名称", value: row.name },
        { label: "key_prefix", value: <span className="mono">{row.key_prefix}</span> },
        { label: "scopes", value: scopesText(row.scopes) },
        { label: "限流/分钟", value: fmtNumber(row.rate_limit_per_minute) },
        { label: "最后使用", value: fmtDateTime(row.last_used_at) },
      ],
      successTitle: "API Key 已轮换",
      successDetails: (data) => [
        ...plaintextDetails(data, "轮换"),
        { label: "被替换的密钥", value: <span className="mono">{text(data.rotated_from)}</span> },
      ],
      onDone: () => list.reload(),
    });
  };

  const revokeKey = async (row: ApiKeyRow) => {
    if (!requirePermission("admin:apikey:revoke")) return;
    await command.run({
      path: `/api/admin/v1/api-keys/${encodeURIComponent(row.id)}/commands/revoke`,
      variant: "danger",
      requireStepUp: true,
      confirmWord: "REVOKE",
      confirmTitle: `吊销 API Key · ${row.name}`,
      confirmBody: "吊销不可撤销：立即失效，使用该密钥的调用方会收到鉴权失败。",
      confirmLabel: "吊销密钥",
      details: [
        { label: "名称", value: row.name },
        { label: "key_prefix", value: <span className="mono">{row.key_prefix}</span> },
        { label: "状态", value: <StatusBadge tone={keyTone(row.status)}>{row.status}</StatusBadge> },
        { label: "scopes", value: scopesText(row.scopes) },
        { label: "创建时间", value: fmtDateTime(row.created_at) },
        { label: "最后使用", value: fmtDateTime(row.last_used_at) },
      ],
      successTitle: "API Key 已吊销",
      successDetails: (data) => [
        { label: "密钥 ID", value: <span className="mono">{text(data.id)}</span> },
        { label: "状态", value: <StatusBadge tone="danger">{text(data.status)}</StatusBadge> },
      ],
      onDone: () => list.reload(),
    });
  };

  return (
    <>
      <PageHeader
        title="API / MCP Key"
        subtitle={
          <>
            数据源 <span className="mono">gpt_action_api_keys</span>
          </>
        }
        actions={
          <>
            <button
              type="button"
              className="btn btn--sm btn--primary"
              disabled={!can("admin:apikey:create") || command.busy}
              onClick={() => void createKey()}
            >
              新建密钥
            </button>
            <button type="button" className="btn btn--sm" onClick={list.reload}>
              刷新
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.hasPrevious} onClick={list.goPrevious}>
              上一页
            </button>
            <button type="button" className="btn btn--sm" disabled={!list.nextCursor} onClick={list.goNext}>
              下一页
            </button>
            <button type="button" className="btn btn--sm" onClick={showScope}>
              口径
            </button>
          </>
        }
      />

      <div className="metric-grid">
        <Metric label="密钥总数" value={fmtNumber(rows.length)} />
        <Metric label="生效中" value={fmtNumber(summary.active)} tone="ok" />
        <Metric label="已吊销" value={fmtNumber(summary.revoked)} tone={summary.revoked > 0 ? "danger" : "neutral"} />
        <Metric label="30 天内过期" value={fmtNumber(summary.soon)} tone={summary.soon > 0 ? "warn" : "ok"} />
      </div>

      <Card title="密钥列表" flush>
        <DataTable<ApiKeyRow>
          rows={rows}
          rowKey={(row) => row.id}
          loading={list.loading}
          error={list.error}
          onRetry={list.reload}
          columns={[
            {
              key: "name",
              header: "名称",
              render: (row) => (
                <span>
                  <strong>{row.name}</strong>
                  <div style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>{row.description || "—"}</div>
                </span>
              ),
            },
            { key: "prefix", header: "key_prefix", width: "160px", render: (row) => <span className="mono">{row.key_prefix}…</span> },
            {
              key: "status",
              header: "状态",
              width: "120px",
              render: (row) => <StatusBadge tone={keyTone(row.status)}>{row.status}</StatusBadge>,
            },
            {
              key: "scopes",
              header: "scopes",
              render: (row) =>
                (row.scopes ?? []).length > 0 ? (
                  <span className="toolbar">
                    {(row.scopes ?? []).map((scope) => (
                      <StatusBadge key={scope} tone="info">
                        {scope}
                      </StatusBadge>
                    ))}
                  </span>
                ) : (
                  "—"
                ),
            },
            {
              key: "rate",
              header: "限流/分钟",
              width: "110px",
              align: "right",
              render: (row) => <span className="num">{fmtNumber(row.rate_limit_per_minute)}</span>,
            },
            {
              key: "ips",
              header: "允许 IP",
              width: "180px",
              render: (row) => <span className="mono">{ipText(row.allowed_ips)}</span>,
            },
            { key: "expires", header: "过期时间", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.expires_at)}</span> },
            { key: "used", header: "最后使用", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.last_used_at)}</span> },
            { key: "created", header: "创建时间", width: "170px", render: (row) => <span className="num">{fmtDateTime(row.created_at)}</span> },
            {
              key: "revoked",
              header: "吊销信息",
              width: "180px",
              render: (row) => (row.revoked_at ? `${fmtDateTime(row.revoked_at)} · ${row.revoke_reason || "—"}` : "—"),
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
                    disabled={!can("admin:apikey:revoke") || command.busy || row.status === "revoked"}
                    onClick={() => void rotateKey(row)}
                  >
                    轮换
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--danger"
                    disabled={!can("admin:apikey:revoke") || command.busy || row.status === "revoked"}
                    onClick={() => void revokeKey(row)}
                  >
                    吊销
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
