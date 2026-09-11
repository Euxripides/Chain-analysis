import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type AdminIdentity } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  Field,
  PageHeader,
  RelativeTime,
  StatusBadge,
  statusToneFor,
  useDialog,
  type DialogRequest,
} from "@chain/ui";
import type { StatusTone } from "@chain/ui";

/** §19.3 用户与成员管理（A-USER-01/02）。高风险操作带二次确认与原因。 */
export default function UsersPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const [rows, setRows] = useState<AdminIdentity[]>([]);
  const [total, setTotal] = useState(0);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await client.get<AdminIdentity[]>("/api/admin/v1/users", { query: { limit: 200, search } });
      setRows(res.data ?? []);
      setTotal(res.meta.total ?? res.data?.length ?? 0);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载用户列表");
    } finally {
      setLoading(false);
    }
  }, [client, search]);

  useEffect(() => {
    void load();
  }, [load]);

  const openUser = async (row: AdminIdentity) => {
    try {
      const res = await client.get<{ identity: AdminIdentity; roles: string[]; active_sessions: unknown[] }>(
        `/api/admin/v1/users/${row.id}`,
      );
      const detail = res.data;
      await dialog.open({
        kind: "info",
        title: detail.identity.display_name || detail.identity.email,
        body: null,
        details: [
          { label: "用户 ID", value: <span className="mono">{detail.identity.id}</span> },
          { label: "邮箱", value: detail.identity.email },
          { label: "状态", value: <StatusBadge tone={statusToneFor(detail.identity.status) as StatusTone}>{detail.identity.status}</StatusBadge> },
          { label: "角色", value: detail.roles.join(", ") || "—" },
          { label: "组织", value: <span className="mono">{detail.identity.organization_id}</span> },
          { label: "已有密码凭据", value: detail.identity.has_password ? "是" : "否" },
          { label: "需要修改密码", value: detail.identity.must_change_password ? "是" : "否" },
          { label: "失败登录次数", value: String(detail.identity.failed_login_count) },
          { label: "最近登录", value: detail.identity.last_login_at ? new Date(detail.identity.last_login_at).toLocaleString("zh-CN") : "—" },
          { label: "活跃会话", value: String((detail.active_sessions ?? []).length) },
        ],
        actions: [{ label: "关闭", variant: "primary", result: "ok" }],
      } satisfies DialogRequest);
    } catch (err) {
      dialog.notify("error", "读取用户详情失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const setStatus = async (row: AdminIdentity, next: "active" | "disabled") => {
    const answer = await dialog.confirm({
      kind: next === "disabled" ? "warning" : "confirm",
      title: next === "disabled" ? "停用该用户？" : "启用该用户？",
      body:
        next === "disabled"
          ? "停用会立即撤销该用户的全部会话，并阻止后续登录。此操作会写入审计日志。"
          : "启用后该用户可以重新登录。",
      details: [
        { label: "用户", value: row.display_name || row.email },
        { label: "用户 ID", value: <span className="mono">{row.id}</span> },
        { label: "当前状态", value: row.status },
      ],
      confirmWord: next === "disabled" ? "DISABLE" : undefined,
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: next === "disabled" ? "停用" : "启用", result: "ok", variant: next === "disabled" ? "danger" : "primary" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.post(`/api/admin/v1/users/${row.id}/commands/${next === "disabled" ? "disable" : "enable"}`, {
        confirm: true,
        reason: "manual admin action",
      });
      dialog.notify("success", next === "disabled" ? "用户已停用" : "用户已启用");
      await load();
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  return (
    <>
      <PageHeader
        title="用户与成员"
        subtitle={`共 ${total} 个身份 · 数据源 postgres:identities`}
        actions={
          <button type="button" className="btn btn--sm" disabled={!can("admin:user:create")} title="邀请流程在 S4 开放">
            邀请用户（S4）
          </button>
        }
      />
      <Card>
        <form
          className="toolbar"
          onSubmit={(e) => {
            e.preventDefault();
            void load();
          }}
          role="search"
        >
          <Field label="搜索" htmlFor="user-search">
            <input
              id="user-search"
              className="input"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="邮箱或显示名称"
              style={{ minWidth: 260 }}
            />
          </Field>
          <button className="btn btn--primary btn--sm" type="submit" style={{ alignSelf: "flex-end" }}>
            查询
          </button>
        </form>
      </Card>
      <Card flush>
        <DataTable<AdminIdentity>
          loading={loading}
          error={error}
          onRetry={load}
          rows={rows}
          rowKey={(r) => r.id}
          columns={[
            {
              key: "user",
              header: "用户",
              render: (r) => (
                <button type="button" className="btn btn--ghost btn--sm" onClick={() => void openUser(r)}>
                  <span style={{ textAlign: "left" }}>
                    <strong>{r.display_name || "—"}</strong>
                    <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{r.email}</div>
                  </span>
                </button>
              ),
            },
            { key: "status", header: "状态", width: "120px", render: (r) => <StatusBadge tone={statusToneFor(r.status)}>{r.status}</StatusBadge> },
            { key: "roles", header: "角色", width: "200px", render: (r) => <span className="mono">{r.roles?.join(", ") || "—"}</span> },
            { key: "login", header: "最近登录", width: "140px", render: (r) => <RelativeTime value={r.last_login_at ?? null} /> },
            { key: "id", header: "身份 ID", width: "180px", render: (r) => <EntityLink value={r.id} /> },
            {
              key: "actions",
              header: "",
              width: "180px",
              align: "right",
              render: (r) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  {r.status === "disabled" ? (
                    <button className="btn btn--sm" disabled={!can("admin:user:disable")} onClick={() => void setStatus(r, "active")}>
                      启用
                    </button>
                  ) : (
                    <button className="btn btn--sm" disabled={!can("admin:user:disable")} onClick={() => void setStatus(r, "disabled")}>
                      停用
                    </button>
                  )}
                </span>
              ),
            },
          ]}
          empty={<EmptyState title="没有匹配的用户" body="调整搜索条件后重试。" />}
        />
      </Card>
    </>
  );
}
