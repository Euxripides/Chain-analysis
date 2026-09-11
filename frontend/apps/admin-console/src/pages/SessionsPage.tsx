import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type SessionRow } from "@chain/api-client";
import { Card, DataTable, EmptyState, EntityLink, Field, PageHeader, RelativeTime, StatusBadge, statusToneFor, useDialog } from "@chain/ui";
import type { StatusTone } from "@chain/ui";

/** §21.6 会话管理。强制下线是 critical 风险操作，需要二次确认 + 原因。 */
export default function SessionsPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const [rows, setRows] = useState<SessionRow[]>([]);
  const [total, setTotal] = useState(0);
  const [audience, setAudience] = useState("");
  const [activeOnly, setActiveOnly] = useState(true);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<SessionRow[]>("/api/admin/v1/sessions", {
        query: { limit: 200, audience, active: activeOnly ? "true" : "" },
      });
      setRows(res.data ?? []);
      setTotal(res.meta.total ?? res.data?.length ?? 0);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载会话列表");
    } finally {
      setLoading(false);
    }
  }, [client, audience, activeOnly]);

  useEffect(() => {
    void load();
  }, [load]);

  const revoke = async (row: SessionRow) => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "强制下线该会话？",
      body: "该会话会立即失效，用户需要重新登录。审计日志会记录操作者与原因。",
      details: [
        { label: "用户", value: row.identity?.display_name || row.identity?.email || row.identity_id },
        { label: "受众", value: row.audience },
        { label: "设备", value: row.device?.label ? String(row.device.label) : "未知" },
        { label: "客户端声明 IP", value: row.client_ip || "—" },
        { label: "会话 ID", value: <span className="mono">{row.id}</span> },
      ],
      confirmWord: "REVOKE",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "强制下线", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.post(`/api/admin/v1/sessions/${row.id}/commands/revoke`, { confirm: true, reason: "admin forced offline" });
      dialog.notify("success", "会话已撤销");
      await load();
    } catch (err) {
      dialog.notify("error", "撤销失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  return (
    <>
      <PageHeader title="登录与会话" subtitle={`共 ${total} 条会话 · 数据源 postgres:sessions`} actions={<button className="btn btn--sm" onClick={load}>刷新</button>} />
      <Card>
        <div className="toolbar">
          <Field label="受众" htmlFor="audience">
            <select id="audience" className="select" value={audience} onChange={(e) => setAudience(e.target.value)}>
              <option value="">全部</option>
              <option value="user">用户层</option>
              <option value="admin">后台</option>
            </select>
          </Field>
          <label className="field__label" style={{ display: "flex", alignItems: "center", gap: 8, alignSelf: "flex-end", paddingBottom: 8 }}>
            <input type="checkbox" checked={activeOnly} onChange={(e) => setActiveOnly(e.target.checked)} />
            仅显示有效会话
          </label>
        </div>
      </Card>
      <Card flush>
        <DataTable<SessionRow>
          loading={loading}
          error={error}
          onRetry={load}
          rows={rows}
          rowKey={(r) => r.id}
          empty={<EmptyState title="没有会话记录" />}
          columns={[
            {
              key: "user",
              header: "用户",
              render: (r) => (
                <span>
                  <strong>{r.identity?.display_name || "—"}</strong>
                  <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{r.identity?.email}</div>
                </span>
              ),
            },
            { key: "audience", header: "受众", width: "90px", render: (r) => <StatusBadge tone="info">{r.audience}</StatusBadge> },
            {
              key: "device",
              header: "设备（客户端声明）",
              render: (r) => (
                <span>
                  {r.device?.label ? String(r.device.label) : "未知"}
                  <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)", maxWidth: 280, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {r.user_agent || "—"}
                  </div>
                </span>
              ),
            },
            { key: "ip", header: "客户端 IP", width: "150px", render: (r) => <span className="mono">{r.client_ip || "—"}</span> },
            { key: "seen", header: "最近活动", width: "130px", render: (r) => <RelativeTime value={r.last_seen_at} /> },
            {
              key: "state",
              header: "状态",
              width: "110px",
              render: (r) =>
                r.revoked_at ? (
                  <StatusBadge tone={statusToneFor("REVOKED") as StatusTone}>已撤销</StatusBadge>
                ) : new Date(r.expires_at) < new Date() ? (
                  <StatusBadge tone="neutral">已过期</StatusBadge>
                ) : (
                  <StatusBadge tone="ok">有效</StatusBadge>
                ),
            },
            { key: "id", header: "会话 ID", width: "180px", render: (r) => <EntityLink value={r.id} /> },
            {
              key: "actions",
              header: "",
              width: "120px",
              align: "right",
              render: (r) =>
                r.revoked_at ? null : (
                  <button className="btn btn--sm" disabled={!can("admin:session:revoke")} onClick={() => void revoke(r)}>
                    强制下线
                  </button>
                ),
            },
          ]}
        />
      </Card>
    </>
  );
}
