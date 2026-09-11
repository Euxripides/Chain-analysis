import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type SessionRow } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  EvidenceRail,
  Field,
  PageHeader,
  RelativeTime,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";

interface Profile {
  id: string;
  email: string;
  display_name: string;
  status: string;
  roles: string[];
  organization_id: string;
  must_change_password: boolean;
  mfa_required: boolean;
  last_login_at?: string;
  created_at: string;
}

interface DeviceRow {
  id: string;
  device_label: string;
  platform: string;
  browser: string;
  first_seen_at: string;
  last_seen_at: string;
  trusted_until?: string;
  revoked_at?: string;
  created_ip: string;
  last_ip: string;
}

/** §18 个人资料 + §21.4 会话与设备安全页。 */
export default function ProfilePage() {
  const { client, session, reload } = useAuth();
  const dialog = useDialog();
  const [profile, setProfile] = useState<Profile | null>(null);
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [devices, setDevices] = useState<DeviceRow[]>([]);
  const [displayName, setDisplayName] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [p, s, d] = await Promise.all([
        client.get<Profile>("/api/v2/me/profile"),
        client.get<SessionRow[]>("/api/auth/v1/sessions"),
        client.get<DeviceRow[]>("/api/auth/v1/devices"),
      ]);
      setProfile(p.data);
      setDisplayName(p.data.display_name);
      setSessions(s.data ?? []);
      setDevices(d.data ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载个人资料");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const saveName = async () => {
    if (!displayName.trim()) {
      dialog.notify("warning", "名称不能为空");
      return;
    }
    setBusy(true);
    try {
      await client.patch("/api/v2/me/profile", { display_name: displayName.trim() });
      dialog.notify("success", "资料已更新");
      await reload();
      await load();
    } catch (err) {
      dialog.notify("error", "更新失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const changePassword = async () => {
    if (currentPassword.length === 0 || newPassword.length < 12) {
      dialog.notify("warning", "密码不满足要求", "新密码至少 12 个字符；当前密码不能为空。");
      return;
    }
    const answer = await dialog.confirm({
      kind: "warning",
      title: "修改密码并下线全部设备？",
      body: "修改成功后，包括当前设备在内的全部会话都会被撤销，需要重新登录。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "修改并下线", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    setBusy(true);
    try {
      await client.post("/api/auth/v1/password/change", { current_password: currentPassword, new_password: newPassword });
      setCurrentPassword("");
      setNewPassword("");
      dialog.notify("success", "密码已修改", "全部会话已撤销，请使用新密码重新登录。");
      window.location.href = "/app-v2/login";
    } catch (err) {
      dialog.notify("error", "修改失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const revokeSession = async (row: SessionRow) => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "撤销该会话？",
      body: "被撤销的设备会立即退出登录。",
      details: [
        { label: "设备", value: row.device?.label ? String(row.device.label) : row.user_agent.slice(0, 60) || "未知" },
        { label: "客户端声明 IP", value: row.client_ip || "—" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "撤销", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.delete(`/api/auth/v1/sessions/${row.id}`);
      dialog.notify("success", "会话已撤销");
      await load();
    } catch (err) {
      dialog.notify("error", "撤销失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const revokeAll = async () => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "退出全部设备？",
      body: "除当前浏览器外，其它设备的会话都会立即失效。",
      confirmWord: "REVOKE-ALL",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "全部下线", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.post("/api/auth/v1/sessions/revoke-all", { keep_current: true });
      dialog.notify("success", "其它设备已下线");
      await load();
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  if (loading) {
    return <Card title="个人资料与安全">正在加载…</Card>;
  }
  if (error || !profile) {
    return (
      <Card>
        <EmptyState title="无法加载个人资料" body={error ?? "未知错误"} action={<button className="btn btn--sm" onClick={load}>重试</button>} />
      </Card>
    );
  }

  return (
    <div className="workbench">
      <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
        <PageHeader title="个人资料与安全" subtitle={<span className="mono">{profile.email}</span>} />

        <Card title="基本资料">
          <Field label="显示名称" htmlFor="display_name" hint="用于任务、审计和协作记录中的展示。">
            <input id="display_name" className="input" value={displayName} onChange={(e) => setDisplayName(e.target.value)} maxLength={80} />
          </Field>
          <div className="toolbar" style={{ marginTop: "var(--sp-4)" }}>
            <button className="btn btn--primary btn--sm" onClick={saveName} disabled={busy || displayName === profile.display_name}>
              保存
            </button>
            <StatusBadge tone={profile.status === "active" ? "ok" : "warn"}>{profile.status}</StatusBadge>
            {profile.must_change_password ? <StatusBadge tone="warn">需要修改初始密码</StatusBadge> : null}
          </div>
        </Card>

        <Card title="修改密码">
          <div style={{ display: "grid", gap: "var(--sp-4)", maxWidth: 420 }}>
            <Field label="当前密码" htmlFor="current_password">
              <input
                id="current_password"
                className="input"
                type="password"
                autoComplete="current-password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
              />
            </Field>
            <Field label="新密码" htmlFor="new_password" hint="至少 12 个字符。系统只保存 Argon2id 哈希。">
              <input
                id="new_password"
                className="input"
                type="password"
                autoComplete="new-password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
              />
            </Field>
            <button className="btn btn--primary" onClick={changePassword} disabled={busy}>
              修改密码
            </button>
          </div>
        </Card>

        <Card
          title={`我的会话（${sessions.length}）`}
          actions={
            <button className="btn btn--sm" onClick={revokeAll}>
              退出其它设备
            </button>
          }
          flush
        >
          <DataTable<SessionRow>
            rows={sessions}
            rowKey={(r) => r.id}
            empty={<EmptyState title="没有会话记录" />}
            columns={[
              {
                key: "device",
                header: "设备（客户端声明）",
                render: (r) => (
                  <span>
                    <strong>{r.device?.label ? String(r.device.label) : "未知设备"}</strong>
                    <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)", maxWidth: 320, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {r.user_agent || "—"}
                    </div>
                  </span>
                ),
              },
              { key: "audience", header: "受众", width: "90px", render: (r) => <StatusBadge tone="info">{r.audience}</StatusBadge> },
              { key: "ip", header: "客户端 IP", width: "150px", render: (r) => <span className="mono">{r.client_ip || "—"}</span> },
              { key: "seen", header: "最近活动", width: "130px", render: (r) => <RelativeTime value={r.last_seen_at} /> },
              {
                key: "status",
                header: "状态",
                width: "110px",
                render: (r) =>
                  r.revoked_at ? (
                    <StatusBadge tone="neutral">已撤销</StatusBadge>
                  ) : (
                    <StatusBadge tone={statusToneFor("ACTIVE")}>
                      {r.id === session?.session_id ? "当前会话" : "有效"}
                    </StatusBadge>
                  ),
              },
              {
                key: "actions",
                header: "",
                width: "90px",
                align: "right",
                render: (r) =>
                  r.id === session?.session_id || r.revoked_at ? null : (
                    <button className="btn btn--sm" onClick={() => revokeSession(r)}>
                      撤销
                    </button>
                  ),
              },
            ]}
          />
        </Card>

        <Card title={`设备（${devices.length}）`} flush>
          <DataTable<DeviceRow>
            rows={devices}
            rowKey={(r) => r.id}
            empty={<EmptyState title="还没有设备记录" body="登录成功后系统会按平台与浏览器登记设备，不采集硬件指纹。" />}
            columns={[
              { key: "label", header: "设备", render: (r) => r.device_label || "未命名设备" },
              { key: "platform", header: "平台", width: "120px", render: (r) => r.platform || "—" },
              { key: "browser", header: "浏览器", width: "120px", render: (r) => r.browser || "—" },
              { key: "ip", header: "最近 IP", width: "150px", render: (r) => <span className="mono">{r.last_ip || "—"}</span> },
              { key: "seen", header: "最近出现", width: "130px", render: (r) => <RelativeTime value={r.last_seen_at} /> },
            ]}
          />
        </Card>
      </div>

      <EvidenceRail
        title="会话证据"
        items={[
          { label: "会话 ID", value: <EntityLink value={session?.session_id ?? ""} /> },
          { label: "受众", value: session?.audience ?? "—" },
          { label: "角色", value: session?.roles.join(", ") || "—" },
          { label: "权限数", value: String(session?.permissions.length ?? 0) },
          { label: "空闲到期", value: session ? new Date(session.expires_at).toLocaleString("zh-CN") : "—" },
          { label: "绝对到期", value: session ? new Date(session.absolute_expires_at).toLocaleString("zh-CN") : "—" },
          { label: "MFA", value: session?.mfa_satisfied ? "已验证" : "未启用" },
          { label: "组织", value: profile?.organization_id ?? "—" },
        ]}
      />
    </div>
  );
}
