import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  Metric,
  PageHeader,
  RelativeTime,
  StatusBadge,
  dialogInputValue,
  useDialog,
} from "@chain/ui";
import { Building2 } from "lucide-react";

interface OrganizationRow {
  id: string;
  name: string;
  slug: string;
  created_at: string;
  member_count: number;
  workspace_count: number;
}

interface WorkspaceRow {
  id: string;
  organization_id: string;
  name: string;
  slug: string;
  created_at: string;
  case_count: number;
  member_count: number;
}

interface MemberRow {
  identity_id: string;
  email: string;
  display_name: string;
  identity_status: string;
  membership_status: string;
  joined_at: string;
  roles: string[];
}

interface RoleRow {
  id: string;
  key: string;
  name: string;
  is_system: boolean;
}

interface BindingRow {
  id: string;
  role_id: string;
  role_key: string;
  role_name: string;
  scope_type: string;
  scope_id: string;
  granted_at: string;
}

/**
 * §19 A-ORG-01 / A-WS-01：组织与工作区管理（S3-ADMIN-01）。
 *
 * 所有变更都走弹窗二次确认，原因字段可追溯；组织/工作区这类对象改名会影响
 * 所有成员的界面与审计记录，因此不做"直接保存"。
 */
export default function OrganizationsPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const [orgs, setOrgs] = useState<OrganizationRow[]>([]);
  const [workspaces, setWorkspaces] = useState<WorkspaceRow[]>([]);
  const [members, setMembers] = useState<MemberRow[]>([]);
  const [roles, setRoles] = useState<RoleRow[]>([]);
  const [selectedOrg, setSelectedOrg] = useState<string>("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const orgRes = await client.get<OrganizationRow[]>("/api/admin/v1/organizations");
      const list = orgRes.data ?? [];
      setOrgs(list);
      const orgID = selectedOrg || list[0]?.id || "";
      setSelectedOrg(orgID);
      const [wsRes, memberRes, roleRes] = await Promise.all([
        client.get<WorkspaceRow[]>("/api/admin/v1/workspaces", { query: { organization_id: orgID } }),
        client.get<MemberRow[]>("/api/admin/v1/members", { query: { organization_id: orgID } }),
        client.get<RoleRow[]>("/api/admin/v1/roles"),
      ]);
      setWorkspaces(wsRes.data ?? []);
      setMembers(memberRes.data ?? []);
      setRoles(roleRes.data ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载组织数据");
    } finally {
      setLoading(false);
    }
  }, [client, selectedOrg]);

  useEffect(() => {
    void load();
    // 只在挂载与切换组织时加载
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [client, selectedOrg]);

  const renameOrg = async (org: OrganizationRow) => {
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "重命名组织？",
      body: "名称会出现在所有成员的界面与审计记录中，历史审计不会回溯修改。",
      details: [
        { label: "当前名称", value: org.name },
        { label: "slug（不可改）", value: <span className="mono">{org.slug}</span> },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "继续", result: "next", variant: "primary" },
      ],
    });
    if (answer !== "next") return;

    const name = dialogInputValue(
      await dialog.prompt({
        title: "输入新的组织名称",
        body: "名称会出现在所有成员的界面与审计记录中。",
        input: { label: "组织名称", defaultValue: org.name, pattern: /^.{1,120}$/, patternHint: "1-120 个字符" },
        submitLabel: "重命名",
      }),
    );
    if (!name) return;
    try {
      await client.patch(`/api/admin/v1/organizations/${org.id}`, { name, confirm: true, reason: "admin console rename" });
      dialog.notify("success", "组织已重命名", `新名称：${name}`);
      await load();
    } catch (err) {
      dialog.notify("error", "重命名失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const createWorkspace = async () => {
    const clean = (
      dialogInputValue(
        await dialog.prompt({
          title: "新建工作区",
          body: "slug 一旦创建不可修改，会出现在对象路径与审计中。",
          input: {
            label: "slug",
            placeholder: "acme-lab",
            pattern: /^[a-z0-9-]{1,63}$/,
            patternHint: "只允许小写字母、数字与连字符，长度 1-63",
          },
          submitLabel: "下一步",
        }),
      ) ?? ""
    ).toLowerCase();
    if (!clean) return;
    const name = dialogInputValue(
      await dialog.prompt({
        title: "工作区名称",
        body: <>为 <span className="mono">{clean}</span> 指定显示名称。</>,
        input: { label: "名称", defaultValue: clean, pattern: /^.{1,120}$/ },
        submitLabel: "创建",
      }),
    );
    if (!name) return;
    try {
      await client.post("/api/admin/v1/workspaces", {
        organization_id: selectedOrg,
        slug: clean,
        name: name.trim() || clean,
        confirm: true,
      });
      dialog.notify("success", "工作区已创建", `slug：${clean}`);
      await load();
    } catch (err) {
      dialog.notify("error", "创建失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const manageBindings = async (member: MemberRow) => {
    try {
      const res = await client.get<BindingRow[]>(`/api/admin/v1/users/${member.identity_id}/roles`);
      const bindings = res.data ?? [];
      const detail = bindings.length
        ? bindings
            .map((b) => `${b.role_key}（${b.scope_type}）`)
            .join("、")
        : "无";
      const answer = await dialog.open({
        kind: "info",
        title: `${member.display_name || member.email} 的角色绑定`,
        body: null,
        details: [
          { label: "身份 ID", value: <span className="mono">{member.identity_id}</span> },
          { label: "当前绑定", value: detail },
          { label: "账号状态", value: <StatusBadge tone={member.identity_status === "active" ? "ok" : "warn"}>{member.identity_status}</StatusBadge> },
          { label: "成员状态", value: member.membership_status },
        ],
        actions: [
          { label: "关闭", result: "", variant: "default" },
          ...(can("admin:member:manage")
            ? [
                { label: "授予角色", result: "grant", variant: "primary" as const },
                ...(bindings.length ? [{ label: "撤销最后一个绑定", result: "revoke", variant: "danger" as const }] : []),
              ]
            : []),
        ],
      });
      if (answer === "grant") {
        const key = dialogInputValue(
          await dialog.prompt({
            title: "授予角色",
            body: `可选角色：${roles.map((r) => r.key).join(" / ")}`,
            input: {
              label: "角色键",
              defaultValue: "analyst",
              pattern: new RegExp(`^(${roles.map((r) => r.key).join("|")})$`),
              patternHint: "必须是已存在的角色键",
            },
            submitLabel: "下一步",
          }),
        );
        if (!key) return;
        const scope = dialogInputValue(
          await dialog.prompt({
            title: "作用域",
            body: "organization = 组织内全部工作区；workspace = 仅指定工作区。",
            input: { label: "scope_type", defaultValue: "organization", pattern: /^(organization|workspace)$/, patternHint: "organization 或 workspace" },
            submitLabel: "授予",
          }),
        );
        if (!scope) return;
        await client.post(`/api/admin/v1/users/${member.identity_id}/roles`, {
          role_key: key,
          scope_type: scope,
          scope_id: scope === "workspace" ? selectedOrgWorkspace() : selectedOrg,
          confirm: true,
          reason: "admin console grant",
        });
        dialog.notify("success", "角色已授予", "授权缓存已失效，新权限在数秒内生效。");
      } else if (answer === "revoke" && bindings.length) {
        const target = bindings[bindings.length - 1];
        const confirmed = await dialog.confirm({
          kind: "warning",
          title: "撤销该角色绑定？",
          body: "撤销后该身份将立即失去对应权限。若这是最后一个 Owner 绑定，服务端会拒绝。",
          confirmWord: "REVOKE",
          details: [
            { label: "角色", value: target.role_key },
            { label: "作用域", value: `${target.scope_type} · ${target.scope_id}` },
          ],
          actions: [
            { label: "取消", result: "", variant: "default" },
            { label: "撤销绑定", result: "ok", variant: "danger" },
          ],
        });
        if (confirmed !== "ok") return;
        await client.delete(`/api/admin/v1/users/${member.identity_id}/roles/${target.id}`, {
          body: { confirm: true, reason: "admin console revoke" },
        });

        dialog.notify("success", "角色绑定已撤销");
      }
      await load();
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  function selectedOrgWorkspace(): string {
    return workspaces[0]?.id ?? "";
  }

  const current = orgs.find((o) => o.id === selectedOrg);

  return (
    <>
      <PageHeader
        title="组织与工作区"
        subtitle="租户边界的事实来源。对象级授权按 organization / workspace 绑定收窄，前端无法提交任意 tenant ID。"
        actions={
          <>
            <button className="btn btn--sm" onClick={load}>
              刷新
            </button>
            <button className="btn btn--sm" disabled={!can("admin:workspace:manage")} onClick={createWorkspace}>
              新建工作区
            </button>
          </>
        }
      />

      {loading ? (
        <Card title="正在加载">读取组织、工作区与成员…</Card>
      ) : error ? (
        <Card>
          <EmptyState title="加载失败" body={error} action={<button className="btn btn--sm" onClick={load}>重试</button>} />
        </Card>
      ) : (
        <>
          <div className="metric-grid">
            <Metric label="可见组织" value={orgs.length} />
            <Metric label="当前组织成员" value={members.length} />
            <Metric label="当前组织工作区" value={workspaces.length} />
            <Metric label="案件总数" value={workspaces.reduce((sum, w) => sum + w.case_count, 0)} />
          </div>

          <Card title="组织" flush>
            <DataTable<OrganizationRow>
              rows={orgs}
              rowKey={(r) => r.id}
              columns={[
                {
                  key: "name",
                  header: "组织",
                  render: (r) => (
                    <span>
                      <Building2 size={13} aria-hidden="true" style={{ marginRight: 6, verticalAlign: -2 }} />
                      <strong>{r.name}</strong>
                      <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)" }}>
                        {r.slug}
                      </div>
                    </span>
                  ),
                },
                { key: "members", header: "成员", width: "90px", align: "right", render: (r) => <span className="num">{r.member_count}</span> },
                { key: "ws", header: "工作区", width: "90px", align: "right", render: (r) => <span className="num">{r.workspace_count}</span> },
                { key: "created", header: "创建时间", width: "140px", render: (r) => <RelativeTime value={r.created_at} /> },
                {
                  key: "actions",
                  header: "",
                  width: "120px",
                  align: "right",
                  render: (r) => (
                    <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                      <button className="btn btn--sm" onClick={() => setSelectedOrg(r.id)} disabled={r.id === selectedOrg}>
                        {r.id === selectedOrg ? "当前" : "切换"}
                      </button>
                      <button className="btn btn--sm" disabled={!can("admin:org:manage")} onClick={() => void renameOrg(r)}>
                        重命名
                      </button>
                    </span>
                  ),
                },
              ]}
            />
          </Card>

          <Card title={`工作区（${current?.name ?? "—"}）`} flush>
            <DataTable<WorkspaceRow>
              rows={workspaces}
              rowKey={(r) => r.id}
              empty={<EmptyState title="该组织还没有工作区" />}
              columns={[
                { key: "name", header: "名称", render: (r) => <strong>{r.name}</strong> },
                { key: "slug", header: "slug", width: "180px", render: (r) => <span className="mono">{r.slug}</span> },
                { key: "cases", header: "案件数", width: "90px", align: "right", render: (r) => <span className="num">{r.case_count}</span> },
                { key: "members", header: "成员", width: "90px", align: "right", render: (r) => <span className="num">{r.member_count}</span> },
                { key: "id", header: "工作区 ID", width: "300px", render: (r) => <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{r.id}</span> },
              ]}
            />
          </Card>

          <Card title={`成员与角色（${members.length}）`} flush>
            <DataTable<MemberRow>
              rows={members}
              rowKey={(r) => r.identity_id}
              empty={<EmptyState title="该组织还没有成员" />}
              columns={[
                {
                  key: "who",
                  header: "成员",
                  render: (r) => (
                    <span>
                      <strong>{r.display_name || "—"}</strong>
                      <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{r.email}</div>
                    </span>
                  ),
                },
                {
                  key: "roles",
                  header: "角色",
                  render: (r) =>
                    r.roles.length ? (
                      <span className="toolbar">
                        {r.roles.map((role) => (
                          <StatusBadge key={role} tone="info">
                            {role}
                          </StatusBadge>
                        ))}
                      </span>
                    ) : (
                      <span style={{ color: "var(--c-warn, #d97706)" }}>无角色</span>
                    ),
                },
                {
                  key: "status",
                  header: "状态",
                  width: "140px",
                  render: (r) => (
                    <span className="toolbar">
                      <StatusBadge tone={r.identity_status === "active" ? "ok" : "warn"}>{r.identity_status}</StatusBadge>
                      <StatusBadge tone={r.membership_status === "active" ? "ok" : "neutral"}>{r.membership_status}</StatusBadge>
                    </span>
                  ),
                },
                { key: "joined", header: "加入时间", width: "140px", render: (r) => <RelativeTime value={r.joined_at} /> },
                {
                  key: "actions",
                  header: "",
                  width: "150px",
                  align: "right",
                  render: (r) => (
                    <button className="btn btn--sm" disabled={!can("admin:member:read")} onClick={() => void manageBindings(r)}>
                      管理角色
                    </button>
                  ),
                },
              ]}
            />
          </Card>

          <Card title="可用角色">
            <div className="toolbar">
              {roles.map((r) => (
                <StatusBadge key={r.id} tone={r.is_system ? "info" : "neutral"}>
                  {r.key}
                </StatusBadge>
              ))}
            </div>
          </Card>
        </>
      )}
    </>
  );
}
