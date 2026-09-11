import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type PermissionRow, type RoleRow } from "@chain/api-client";
import { Card, DataTable, EmptyState, Field, PageHeader, StatusBadge, SkeletonRows, statusToneFor, useDialog } from "@chain/ui";

/** §19.4 角色与权限（A-ROLE-01/02）。只读展示，角色编辑在 S6 开放。 */
export default function RolesPage() {
  const { client, can } = useAuth();
  const dialog = useDialog();
  const [roles, setRoles] = useState<RoleRow[]>([]);
  const [permissions, setPermissions] = useState<PermissionRow[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [rolePerms, setRolePerms] = useState<PermissionRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [roleRes, permRes] = await Promise.all([
        client.get<RoleRow[]>("/api/admin/v1/roles"),
        can("admin:permission:read") ? client.get<PermissionRow[]>("/api/admin/v1/permissions") : Promise.resolve({ data: [] as PermissionRow[] }),
      ]);
      setRoles(roleRes.data ?? []);
      setPermissions(permRes.data ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载角色与权限");
    } finally {
      setLoading(false);
    }
  }, [client, can]);

  useEffect(() => {
    void load();
  }, [load]);

  const inspect = async (role: RoleRow) => {
    setSelected(role.id);
    try {
      const res = await client.get<PermissionRow[]>(`/api/admin/v1/roles/${role.id}/permissions`);
      setRolePerms(res.data ?? []);
    } catch (err) {
      dialog.notify("error", "读取角色权限失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  if (loading) return <SkeletonRows rows={6} columns={4} />;
  if (error) {
    return (
      <Card>
        <EmptyState title="加载失败" body={error} action={<button className="btn btn--sm" onClick={load}>重试</button>} />
      </Card>
    );
  }

  return (
    <>
      <PageHeader
        title="角色与权限"
        subtitle={`${roles.length} 个角色 · ${permissions.length} 个权限键 · 权限命名规则 resource:action`}
        actions={
          <button type="button" className="btn btn--sm" disabled title="角色编辑在 S6 开放">
            新建组织角色（S6）
          </button>
        }
      />
      <Card flush>
        <DataTable<RoleRow>
          rows={roles}
          rowKey={(r) => r.id}
          empty={<EmptyState title="没有角色" />}
          columns={[
            { key: "key", header: "角色键", width: "180px", render: (r) => <span className="mono">{r.key}</span> },
            { key: "name", header: "名称", width: "160px", render: (r) => <strong>{r.name}</strong> },
            { key: "desc", header: "说明", render: (r) => r.description },
            { key: "scope", header: "归属", width: "110px", render: (r) => <StatusBadge tone={r.is_system ? "info" : "neutral"}>{r.is_system ? "系统角色" : "组织角色"}</StatusBadge> },
            { key: "count", header: "权限数", width: "100px", align: "right", render: (r) => <span className="num">{r.permission_count}</span> },
            {
              key: "actions",
              header: "",
              width: "110px",
              align: "right",
              render: (r) => (
                <button className="btn btn--sm" onClick={() => void inspect(r)}>
                  查看权限
                </button>
              ),
            },
          ]}
        />
      </Card>

      {selected ? (
        <Card title={`角色权限明细（${rolePerms.length}）`} flush>
          <DataTable<PermissionRow>
            rows={rolePerms}
            rowKey={(r) => r.key}
            empty={<EmptyState title="该角色还没有被授予权限" />}
            columns={[
              { key: "key", header: "权限键", width: "240px", render: (r) => <span className="mono">{r.key}</span> },
              { key: "category", header: "分类", width: "160px", render: (r) => <span className="mono">{r.category}</span> },
              { key: "desc", header: "说明", render: (r) => r.description },
              { key: "risk", header: "风险等级", width: "110px", render: (r) => <StatusBadge tone={statusToneFor(r.risk_level.toUpperCase())}>{r.risk_level}</StatusBadge> },
              { key: "sensitive", header: "敏感读取", width: "100px", render: (r) => (r.sensitive ? <StatusBadge tone="warn">是</StatusBadge> : "否") },
            ]}
          />
        </Card>
      ) : null}

      {permissions.length > 0 ? (
        <Card title={`权限目录（${permissions.length}）`} flush>
          <DataTable<PermissionRow>
            rows={permissions}
            rowKey={(r) => r.key}
            columns={[
              { key: "key", header: "权限键", width: "240px", render: (r) => <span className="mono">{r.key}</span> },
              { key: "category", header: "分类", width: "160px", render: (r) => <span className="mono">{r.category}</span> },
              { key: "desc", header: "说明", render: (r) => r.description },
              { key: "risk", header: "风险等级", width: "110px", render: (r) => <StatusBadge tone={statusToneFor(r.risk_level.toUpperCase())}>{r.risk_level}</StatusBadge> },
            ]}
          />
        </Card>
      ) : null}

      <Card title="高风险操作审批">
        <Field label="审批编排" htmlFor="approval-note">
          <input id="approval-note" className="input" readOnly value="approval_requests 表已建立（0042）；审批 UI 在 S6 交付。" />
        </Field>
      </Card>
    </>
  );
}
