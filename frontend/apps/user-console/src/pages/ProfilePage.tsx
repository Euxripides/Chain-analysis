import { useCallback, useEffect, useState } from "react";
import {
  ApiError,
  MFA_ERROR_CODES,
  useAuth,
  type MfaEnrollStart,
  type SessionRow,
} from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  EntityLink,
  EvidenceRail,
  Field,
  Metric,
  PageHeader,
  QrCode,
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

/** 把 Base32 密钥按 4 字符分组，便于人工抄录（分组只影响展示，复制的是原始值）。 */
function groupSecret(secret: string): string {
  return secret.replace(/(.{4})/g, "$1 ").trim();
}

/**
 * 恢复码的一次性展示块。
 *
 * 明文只在 enroll/confirm 的响应里出现一次（服务端只存摘要、不回显），
 * 因此这里既提供"复制全部"，也让调用方通过 Dialog 的 acknowledge 门控
 * 强制用户先勾选"我已保存"再关闭。
 */
function RecoveryCodes({ codes }: { codes: string[] }) {
  const dialog = useDialog();
  const [copied, setCopied] = useState(false);
  return (
    <div>
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))", gap: "var(--sp-2)" }}>
        {codes.map((item) => (
          <span
            key={item}
            className="mono"
            style={{
              padding: "var(--sp-2) var(--sp-3)",
              border: "1px solid var(--c-border)",
              borderRadius: "var(--radius-md)",
              background: "var(--c-surface)",
              textAlign: "center",
            }}
          >
            {item}
          </span>
        ))}
      </div>
      <div className="toolbar" style={{ marginTop: "var(--sp-4)" }}>
        <button
          type="button"
          className="btn btn--sm"
          onClick={async () => {
            try {
              await navigator.clipboard.writeText(codes.join("\n"));
              setCopied(true);
            } catch {
              dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
            }
          }}
        >
          {copied ? "已复制到剪贴板" : `复制全部（${codes.length}）`}
        </button>
      </div>
    </div>
  );
}

/** §18 个人资料 + §21.4 会话与设备安全页 + §19.9 MFA 自助管理。 */
export default function ProfilePage() {
  const { client, session, reload, mfaEnrollStart, mfaEnrollConfirm, mfaDisable } = useAuth();
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
  /**
   * MFA 状态与注册流程。
   *
   * mfa_enrolled 是本轮新增的会话字段：以服务端为准；旧部署尚未返回该字段时
   * （值为 undefined）用本地动作结果兜底，而不是把"字段缺失"当成"未启用"。
   */
  const [localEnrolled, setLocalEnrolled] = useState<boolean | null>(null);
  const [enroll, setEnroll] = useState<MfaEnrollStart | null>(null);
  const [enrollCode, setEnrollCode] = useState("");
  const [mfaBusy, setMfaBusy] = useState(false);
  const mfaEnrolled = session?.mfa_enrolled ?? localEnrolled ?? false;

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
    // 密码不设长度/复杂度限制（需求方 2026-09-18）：只要求非空——空密码会让"密码"这一概念失效。
    if (currentPassword.length === 0 || newPassword.length === 0) {
      dialog.notify("warning", "请填写密码", "当前密码与新密码都不能为空（新密码不限制长度）。");
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

  /** 复制到剪贴板：成功不打扰用户（与 EntityLink 一致），失败才弹窗说明。 */
  const copyText = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
    }
  };

  /** 开始注册：拿密钥与 otpauth URI（确认之前不改变登录要求）。 */
  const startEnroll = async () => {
    setMfaBusy(true);
    try {
      const started = await mfaEnrollStart();
      setEnroll(started);
      setEnrollCode("");
    } catch (err) {
      if (err instanceof ApiError && err.code === MFA_ERROR_CODES.alreadyEnrolled) {
        // 服务端是权威：该账号其实已经启用，纠正本地状态并提示先关闭再重注册。
        setLocalEnrolled(true);
        await reload().catch(() => undefined);
        dialog.notify("warning", "该账号已启用 MFA", "重新注册需要先关闭 MFA，再重新开始。");
      } else if (err instanceof ApiError && err.code === MFA_ERROR_CODES.unavailable) {
        dialog.notify("warning", "MFA 未启用", "当前部署没有启用 MFA 服务，暂时无法注册。");
      } else {
        dialog.notify(
          "error",
          "无法开始 MFA 注册",
          err instanceof ApiError ? err.userMessage : "请稍后重试",
          err instanceof ApiError && err.traceId ? [{ label: "Trace ID", value: err.traceId }] : undefined,
        );
      }
    } finally {
      setMfaBusy(false);
    }
  };

  /** 用一次正确验证码确认注册，并一次性展示恢复码。 */
  const confirmEnroll = async () => {
    if (!enroll) return;
    const value = enrollCode.trim();
    if (value.length < 6) {
      dialog.notify("warning", "请输入验证码", "请输入认证器上显示的 6 位验证码。");
      return;
    }
    setMfaBusy(true);
    try {
      const result = await mfaEnrollConfirm(value);
      setEnroll(null);
      setEnrollCode("");
      setLocalEnrolled(true);
      // 先让服务端会话追上新状态，再展示恢复码：否则弹窗期间状态可能显示回退。
      await reload().catch(() => undefined);
      await dialog.open({
        kind: "warning",
        title: "请立即保存恢复码",
        body: (
          <div>
            <p style={{ marginBottom: "var(--sp-3)" }}>
              以下 {result.recovery_codes.length} 个恢复码用于认证器不可用时登录，每个只能使用一次。
              <strong>关闭本窗口后，此后再也无法查看</strong>，请现在保存到安全的地方。
            </p>
            <RecoveryCodes codes={result.recovery_codes} />
          </div>
        ),
        acknowledge: { label: `我已保存这 ${result.recovery_codes.length} 个恢复码` },
        actions: [{ label: "我已保存，关闭", variant: "primary", result: "ok" }],
      });
    } catch (err) {
      if (err instanceof ApiError && err.code === MFA_ERROR_CODES.invalid) {
        dialog.notify("error", "验证码不正确", "请确认认证器与本机时间同步后重新输入。");
      } else if (err instanceof ApiError && err.code === MFA_ERROR_CODES.notEnrolled) {
        setEnroll(null);
        dialog.notify("warning", "注册已失效", "本次注册没有先完成第一步（密钥签发），请重新点击「启用 MFA」。");
      } else {
        dialog.notify(
          "error",
          "无法确认 MFA 注册",
          err instanceof ApiError ? err.userMessage : "请稍后重试",
          err instanceof ApiError && err.traceId ? [{ label: "Trace ID", value: err.traceId }] : undefined,
        );
      }
    } finally {
      setMfaBusy(false);
    }
  };

  /** 关闭 MFA：需要二次确认；成功后刷新状态。 */
  const disableMfa = async () => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "关闭多因素认证？",
      body: "关闭后该账号仅凭密码即可登录，已生成的恢复码同时作废。",
      details: [
        { label: "账号", value: profile?.email ?? session?.email ?? "—" },
        { label: "当前状态", value: "已启用" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "关闭 MFA", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    setMfaBusy(true);
    try {
      await mfaDisable();
      setLocalEnrolled(false);
      await reload().catch(() => undefined);
      dialog.notify("success", "MFA 已关闭", "该账号现在仅凭密码即可登录。");
    } catch (err) {
      if (err instanceof ApiError && err.code === MFA_ERROR_CODES.notEnrolled) {
        setLocalEnrolled(false);
        await reload().catch(() => undefined);
        dialog.notify("info", "该账号未启用 MFA", "服务端记录显示该账号当前没有启用 MFA。");
      } else {
        dialog.notify(
          "error",
          "关闭 MFA 失败",
          err instanceof ApiError ? err.userMessage : "请稍后重试",
          err instanceof ApiError && err.traceId ? [{ label: "Trace ID", value: err.traceId }] : undefined,
        );
      }
    } finally {
      setMfaBusy(false);
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
            <Field label="新密码" htmlFor="new_password" hint="不限制长度（非空即可）。系统只保存 Argon2id 哈希。">
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
          title={
            <span className="toolbar" style={{ gap: "var(--sp-2)" }}>
              多因素认证（MFA）
              <StatusBadge tone={mfaEnrolled ? "ok" : "neutral"}>{mfaEnrolled ? "已启用" : "未启用"}</StatusBadge>
            </span>
          }
          actions={
            enroll ? null : mfaEnrolled ? (
              <button className="btn btn--danger btn--sm" type="button" onClick={() => void disableMfa()} disabled={mfaBusy}>
                关闭 MFA
              </button>
            ) : (
              <button className="btn btn--primary btn--sm" type="button" onClick={() => void startEnroll()} disabled={mfaBusy}>
                {mfaBusy ? "正在准备…" : "启用 MFA"}
              </button>
            )
          }
        >
          {enroll ? (
            <div style={{ display: "grid", gap: "var(--sp-5)" }}>
              <div
                style={{
                  display: "grid",
                  gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))",
                  gap: "var(--sp-5)",
                  alignItems: "center",
                }}
              >
                <div
                  style={{
                    width: "fit-content",
                    padding: "var(--sp-3)",
                    background: "var(--c-surface)",
                    border: "1px solid var(--c-border)",
                    borderRadius: "var(--radius-md)",
                  }}
                >
                  <QrCode value={enroll.otpauth_uri} size={180} />
                </div>
                <div style={{ display: "grid", gap: "var(--sp-4)", minWidth: 0 }}>
                  <Field label="密钥（Base32）" hint="认证器无法扫码时手工输入；每 4 位一组只是便于抄录，复制的是原始值。">
                    <div className="toolbar">
                      <span className="mono" style={{ letterSpacing: "0.08em", overflowWrap: "anywhere" }}>
                        {groupSecret(enroll.secret)}
                      </span>
                      <button className="btn btn--sm" type="button" onClick={() => void copyText(enroll.secret)}>
                        复制密钥
                      </button>
                    </div>
                  </Field>
                  <Field label="otpauth URI" hint="服务端签发的 provisioning URI，二维码内容与它完全一致。">
                    <div className="toolbar" style={{ alignItems: "flex-start" }}>
                      <span
                        className="mono"
                        style={{ fontSize: "var(--fs-11)", maxWidth: 420, overflowWrap: "anywhere" }}
                      >
                        {enroll.otpauth_uri}
                      </span>
                      <button className="btn btn--sm" type="button" onClick={() => void copyText(enroll.otpauth_uri)}>
                        复制 URI
                      </button>
                    </div>
                  </Field>
                </div>
              </div>
              <div style={{ display: "grid", gridTemplateColumns: "minmax(200px, 280px)", gap: "var(--sp-4)" }}>
                <Field
                  label="认证器验证码"
                  htmlFor="mfa_enroll_code"
                  hint={`${enroll.digits} 位数字，每 ${enroll.period_seconds} 秒更新一次。`}
                >
                  <input
                    id="mfa_enroll_code"
                    name="mfa_enroll_code"
                    className="input"
                    type="text"
                    autoComplete="one-time-code"
                    spellCheck={false}
                    maxLength={Math.max(6, enroll.digits)}
                    value={enrollCode}
                    onChange={(e) => setEnrollCode(e.target.value)}
                  />
                </Field>
              </div>
              <div className="toolbar">
                <button
                  className="btn btn--primary"
                  type="button"
                  onClick={() => void confirmEnroll()}
                  disabled={mfaBusy || enrollCode.trim().length < 6}
                >
                  {mfaBusy ? "正在确认…" : "确认启用"}
                </button>
                <button
                  className="btn"
                  type="button"
                  disabled={mfaBusy}
                  onClick={() => {
                    // 取消注册：密钥在服务端仍是"未确认"状态，不改变登录要求。
                    setEnroll(null);
                    setEnrollCode("");
                  }}
                >
                  取消
                </button>
              </div>
            </div>
          ) : (
            <div className="metric-grid">
              <Metric label="认证方式" value="TOTP" hint="6 位验证码 / 30 秒，兼容常见认证器应用。" />
              <Metric
                label="恢复码"
                value={mfaEnrolled ? "10 枚" : "—"}
                hint={mfaEnrolled ? "仅在注册确认时展示一次，服务端只保存摘要。" : "启用 MFA 时一次性生成 10 枚。"}
              />
              <Metric
                label="本次会话"
                value={mfaEnrolled ? (session?.mfa_satisfied ? "已通过" : "未通过") : "—"}
                tone={mfaEnrolled ? (session?.mfa_satisfied ? "ok" : "warn") : "neutral"}
                hint="本次登录是否已完成第二步验证。"
              />
              <Metric
                label="组织策略"
                value={profile.mfa_required ? "要求 MFA" : "未强制"}
                tone={profile.mfa_required ? "warn" : "neutral"}
                hint="来自该身份的 MFA 要求字段。"
              />
            </div>
          )}
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
          { label: "角色", value: (session?.roles ?? []).join(", ") || "—" },
          { label: "权限数", value: String((session?.permissions ?? []).length) },
          { label: "空闲到期", value: session ? new Date(session.expires_at).toLocaleString("zh-CN") : "—" },
          { label: "绝对到期", value: session ? new Date(session.absolute_expires_at).toLocaleString("zh-CN") : "—" },
          {
            label: "MFA",
            value: mfaEnrolled
              ? session?.mfa_satisfied
                ? "已启用 · 本次已验证"
                : "已启用 · 本次未验证"
              : "未启用",
          },
          { label: "组织", value: profile?.organization_id ?? "—" },
        ]}
      />
    </div>
  );
}
