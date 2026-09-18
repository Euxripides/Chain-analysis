import { useCallback, useEffect, useState } from "react";
import { ApiError, MFA_ERROR_CODES, useAuth, type MfaEnrollStart, type SessionRow } from "@chain/api-client";

/**
 * 设备行（与用户端 ProfilePage 同一形状）。
 * api-client 目前不导出该类型，且它只由 GET /api/auth/v1/devices 返回，故在此就近声明。
 */
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
import {
  Card,
  DataTable,
  EmptyState,
  Field,
  PageHeader,
  QrCode,
  RelativeTime,
  StatusBadge,
  dialogFormValues,
  useDialog,
  type DialogRequest,
} from "@chain/ui";

/**
 * 「账号安全」——后台管理员的**自助**页面（决策 D7）。
 *
 * 为什么要有这一页：MFA 的注册接口（/api/auth/v1/mfa/enroll/*）是自助接口，此前只有用户端
 * 「个人资料与安全」有 UI，后台管理台**没有入口** —— 于是平台管理员自己无处绑定验证器，
 * 一旦打开 AUTH_REQUIRE_ADMIN_MFA 就会被挡在登录第二步之外。本页补齐这个缺口。
 *
 * 覆盖：认证器（谷歌验证器等 TOTP 应用）注册/关闭 + 恢复码、修改密码、我的会话与设备。
 * 全部提示走弹窗（AGENTS.md / docs/code-standards.md）：页面正文只保留数据与操作控件。
 */

/** 把 Base32 密钥按 4 字符分组，便于人工抄录（复制的是原始值）。 */
function groupSecret(secret: string): string {
  return secret.replace(/(.{4})/g, "$1 ").trim();
}

/** 恢复码的一次性展示块：明文只在 confirm 响应里出现一次。 */
function RecoveryCodes({ codes }: { codes: string[] }) {
  const dialog = useDialog();
  const copyAll = async () => {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
    } catch {
      dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
    }
  };
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
      <ol className="mono" style={{ margin: 0, paddingLeft: "1.4em", lineHeight: 1.8 }}>
        {codes.map((code) => (
          <li key={code}>{code}</li>
        ))}
      </ol>
      <div>
        <button type="button" className="btn btn--sm" onClick={() => void copyAll()}>
          复制全部
        </button>
      </div>
    </div>
  );
}

export default function SecurityPage() {
  const { client, session, reload, mfaEnrollStart, mfaEnrollConfirm, mfaDisable } = useAuth();
  const dialog = useDialog();
  const [busy, setBusy] = useState(false);
  const [enroll, setEnroll] = useState<MfaEnrollStart | null>(null);
  const [code, setCode] = useState("");
  const [sessions, setSessions] = useState<SessionRow[]>([]);
  const [devices, setDevices] = useState<DeviceRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [s, d] = await Promise.all([
        client.get<SessionRow[]>("/api/auth/v1/sessions"),
        client.get<DeviceRow[]>("/api/auth/v1/devices"),
      ]);
      setSessions(s.data ?? []);
      setDevices(d.data ?? []);
    } catch (err) {
      setError(err instanceof ApiError ? err.userMessage : "无法加载会话与设备");
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void load();
  }, [load]);

  const copyText = async (value: string) => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
    }
  };

  /** 开始注册：拿到密钥与 otpauth URI（确认之前不改变登录要求）。 */
  const startEnroll = async () => {
    setBusy(true);
    try {
      const started = await mfaEnrollStart();
      setEnroll(started);
      setCode("");
    } catch (err) {
      if (err instanceof ApiError && err.code === MFA_ERROR_CODES.alreadyEnrolled) {
        await reload().catch(() => undefined);
        dialog.notify("warning", "该账号已启用 MFA", "重新注册需要先关闭 MFA，再重新开始。");
      } else if (err instanceof ApiError && err.code === MFA_ERROR_CODES.unavailable) {
        dialog.notify("warning", "MFA 未启用", "当前部署没有启用 MFA 服务，暂时无法注册。");
      } else {
        dialog.notify("error", "无法开始 MFA 注册", err instanceof ApiError ? err.userMessage : "请稍后重试");
      }
    } finally {
      setBusy(false);
    }
  };

  /** 用一次正确验证码确认注册，并一次性展示恢复码。 */
  const confirmEnroll = async () => {
    if (!enroll) return;
    const value = code.trim();
    if (value.length < 6) {
      dialog.notify("warning", "请输入验证码", "请输入认证器上显示的 6 位验证码。");
      return;
    }
    setBusy(true);
    try {
      const result = await mfaEnrollConfirm(value);
      setEnroll(null);
      setCode("");
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
        dialog.notify("error", "无法确认 MFA 注册", err instanceof ApiError ? err.userMessage : "请稍后重试");
      }
    } finally {
      setBusy(false);
    }
  };

  /** 关闭 MFA：二次确认；成功后刷新会话状态。 */
  const disableMfa = async () => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "关闭多因素认证？",
      body: "关闭后该账号仅凭密码即可登录后台，已生成的恢复码同时作废。",
      details: [
        { label: "账号", value: session?.email ?? "—" },
        { label: "当前状态", value: "已启用" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "关闭 MFA", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    setBusy(true);
    try {
      await mfaDisable();
      await reload().catch(() => undefined);
      dialog.notify("success", "MFA 已关闭", "该账号后续登录不再需要第二步验证码。");
    } catch (err) {
      dialog.notify("error", "关闭失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  /** 修改密码：成功后服务端吊销全部会话，需重新登录。 */
  const changePassword = async () => {
    const values = dialogFormValues(
      await dialog.open({
        kind: "info",
        title: "修改密码",
        body: "修改成功后，该账号的全部会话（含本次）都会被吊销，需要重新登录。",
        fields: [
          { name: "current", label: "当前密码（必填）", type: "password", required: true, autoComplete: "current-password" },
          { name: "next", label: "新密码（必填）", type: "password", required: true, autoComplete: "new-password", patternHint: "不限制长度（非空即可）" },
          { name: "confirm", label: "再次输入新密码（必填）", type: "password", required: true, autoComplete: "new-password" },
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "确认修改", result: "ok", variant: "primary" },
        ],
      } satisfies DialogRequest),
    );
    if (!values) return;
    if ((values.next ?? "") !== (values.confirm ?? "")) {
      dialog.notify("warning", "两次输入的密码不一致", "请重新填写。");
      return;
    }
    setBusy(true);
    try {
      await client.post("/api/auth/v1/password/change", {
        current_password: values.current ?? "",
        new_password: values.next ?? "",
      });
      dialog.notify("success", "密码已修改", "全部会话已吊销，请使用新密码重新登录。");
      window.location.href = "/admin/login";
    } catch (err) {
      dialog.notify("error", "修改失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  };

  const revokeSession = async (row: SessionRow) => {
    const self = row.id === session?.session_id;
    const answer = await dialog.confirm({
      kind: "warning",
      title: self ? "撤销当前会话？" : "撤销该会话？",
      body: self ? "这是当前正在使用的会话，撤销后你会立即被登出。" : "该会话将立即失效，对应设备需要重新登录。",
      details: [
        { label: "会话 ID", value: <span className="mono">{row.id}</span> },
        { label: "受众", value: row.audience },
        { label: "最近活动", value: new Date(row.last_seen_at).toLocaleString("zh-CN") },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "撤销", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.delete(`/api/auth/v1/sessions/${row.id}`);
      dialog.notify("success", "会话已撤销", self ? "你已被登出，请重新登录。" : "该设备需要重新登录。");
      if (self) {
        window.location.href = "/admin/login";
        return;
      }
      await load();
    } catch (err) {
      dialog.notify("error", "撤销失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const revokeAll = async () => {
    const answer = await dialog.confirm({
      kind: "warning",
      title: "撤销全部其他会话？",
      body: "除当前会话外的所有设备都会被登出。当前会话保留，避免你在操作过程中被踢出。",
      details: [{ label: "当前会话", value: <span className="mono">{session?.session_id ?? "—"}</span> }],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "全部撤销", result: "ok", variant: "danger" },
      ],
    });
    if (answer !== "ok") return;
    try {
      await client.post("/api/auth/v1/sessions/revoke-all", { keep_current: true });
      dialog.notify("success", "已撤销其他会话", "其他设备需要重新登录。");
      await load();
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const trustDevice = async (row: DeviceRow, days: number) => {
    try {
      await client.patch(`/api/auth/v1/devices/${row.id}`, { device_label: row.device_label, trust_days: days });
      dialog.notify("success", days > 0 ? `已信任该设备 ${days} 天` : "已取消信任", row.device_label || row.id);
      await load();
    } catch (err) {
      dialog.notify("error", "操作失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  const enrolled = session?.mfa_enrolled === true;

  return (
    <>
      <PageHeader
        title="账号安全"
        subtitle={`${session?.email ?? ""} · 本页只影响你自己的账号`}
        actions={
          <button
            type="button"
            className="btn btn--sm"
            onClick={() =>
              dialog.notify("info", "验证器兼容性", null, [
                { label: "算法", value: "TOTP（RFC 6238）：SHA1 / 6 位 / 30 秒" },
                { label: "可用应用", value: "Google Authenticator、Microsoft Authenticator、1Password、Bitwarden、Aegis、FreeOTP 等" },
                { label: "是否需要联网", value: "不需要：二维码由本页本地渲染，验证码由手机本地计算" },
                { label: "对时要求", value: "手机时间需自动同步；服务端允许 ±1 个 30 秒步长的漂移" },
              ])
            }
          >
            验证器口径
          </button>
        }
      />

      <Card title="多因素认证（MFA）">
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
          <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-3)" }}>
            <span>当前状态：</span>
            {enrolled ? <StatusBadge tone="ok">已启用</StatusBadge> : <StatusBadge tone="warn">未启用</StatusBadge>}
            {session?.mfa_satisfied ? <StatusBadge tone="ok">本次登录已通过第二步</StatusBadge> : null}
          </div>

          {enroll ? (
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
              <p style={{ margin: 0 }}>
                用验证器扫描下面的二维码（或手动输入密钥），然后输入它显示的 6 位验证码完成绑定。
              </p>
              <div style={{ display: "flex", gap: "var(--sp-4)", flexWrap: "wrap", alignItems: "flex-start" }}>
                <QrCode value={enroll.otpauth_uri} size={180} />
                <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)", minWidth: 260 }}>
                  <Field label="密钥（手动输入时使用）" htmlFor="mfa-secret">
                    <code id="mfa-secret" className="mono" style={{ userSelect: "all" }}>
                      {groupSecret(enroll.secret)}
                    </code>
                  </Field>
                  <button type="button" className="btn btn--sm" onClick={() => void copyText(enroll.secret)}>
                    复制密钥
                  </button>
                  <Field label="认证器显示的 6 位验证码" htmlFor="mfa-code">
                    <input
                      id="mfa-code"
                      className="input"
                      value={code}
                      onChange={(e) => setCode(e.target.value)}
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      maxLength={6}
                      placeholder="123456"
                    />
                  </Field>
                  <div className="toolbar">
                    <button type="button" className="btn btn--primary btn--sm" disabled={busy} onClick={() => void confirmEnroll()}>
                      确认绑定
                    </button>
                    <button
                      type="button"
                      className="btn btn--sm"
                      disabled={busy}
                      onClick={() => {
                        setEnroll(null);
                        setCode("");
                      }}
                    >
                      取消
                    </button>
                  </div>
                </div>
              </div>
            </div>
          ) : (
            <div className="toolbar">
              {enrolled ? (
                <>
                  <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void disableMfa()}>
                    关闭 MFA
                  </button>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-13)" }}>
                    需要更换认证器时：先关闭 MFA，再重新启用。
                  </span>
                </>
              ) : (
                <>
                  <button type="button" className="btn btn--primary btn--sm" disabled={busy} onClick={() => void startEnroll()}>
                    启用 MFA
                  </button>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-13)" }}>
                    启用后登录需要第二步验证码；确认绑定时会一次性下发恢复码。
                  </span>
                </>
              )}
            </div>
          )}
        </div>
      </Card>

      <Card title="密码">
        <div className="toolbar">
          <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void changePassword()}>
            修改密码
          </button>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-13)" }}>
            修改成功后全部会话（含当前）都会被吊销。
          </span>
        </div>
      </Card>

      <Card
        title={`我的会话（${sessions.length}）`}
        actions={
          <button type="button" className="btn btn--sm" onClick={() => void revokeAll()}>
            撤销其他会话
          </button>
        }
        flush
      >
        <DataTable<SessionRow>
          loading={loading}
          error={error}
          onRetry={load}
          rows={sessions}
          rowKey={(r) => r.id}
          columns={[
            {
              key: "device",
              header: "设备与会话",
              render: (r) => (
                <span>
                  <strong>{r.id === session?.session_id ? "当前会话" : r.audience === "admin" ? "后台会话" : "用户层会话"}</strong>
                  <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{r.user_agent || "—"}</div>
                </span>
              ),
            },
            { key: "audience", header: "受众", width: "90px", render: (r) => r.audience },
            { key: "mfa", header: "MFA", width: "90px", render: (r) => (r.mfa_satisfied ? <StatusBadge tone="ok">已通过</StatusBadge> : <StatusBadge tone="neutral">未要求</StatusBadge>) },
            { key: "ip", header: "来源 IP", width: "150px", render: (r) => <span className="mono">{r.client_ip || r.peer_ip || "—"}</span> },
            { key: "seen", header: "最近活动", width: "140px", render: (r) => <RelativeTime value={r.last_seen_at} /> },
            {
              key: "actions",
              header: "",
              width: "100px",
              align: "right",
              render: (r) => (
                <button className="btn btn--sm" onClick={() => void revokeSession(r)}>
                  撤销
                </button>
              ),
            },
          ]}
          empty={<EmptyState title="没有活跃会话" body="当前账号没有其他活跃会话。" />}
        />
      </Card>

      <Card title={`我的设备（${devices.length}）`} flush>
        <DataTable<DeviceRow>
          loading={loading}
          rows={devices}
          rowKey={(r) => r.id}
          columns={[
            {
              key: "device",
              header: "设备",
              render: (r) => (
                <span>
                  <strong>{r.device_label || "（未命名设备）"}</strong>
                  <div style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {[r.platform, r.browser].filter(Boolean).join(" · ") || "—"}
                  </div>
                </span>
              ),
            },
            { key: "ip", header: "最近 IP", width: "150px", render: (r) => <span className="mono">{r.last_ip || "—"}</span> },
            { key: "seen", header: "最近使用", width: "140px", render: (r) => <RelativeTime value={r.last_seen_at} /> },
            {
              key: "trust",
              header: "信任",
              width: "150px",
              render: (r) =>
                r.revoked_at ? (
                  <StatusBadge tone="danger">已撤销</StatusBadge>
                ) : r.trusted_until ? (
                  <StatusBadge tone="ok">信任中</StatusBadge>
                ) : (
                  <StatusBadge tone="neutral">未信任</StatusBadge>
                ),
            },
            {
              key: "actions",
              header: "",
              width: "170px",
              align: "right",
              render: (r) => (
                <span className="toolbar" style={{ justifyContent: "flex-end" }}>
                  {r.trusted_until ? (
                    <button className="btn btn--sm" onClick={() => void trustDevice(r, 0)}>
                      取消信任
                    </button>
                  ) : (
                    <button className="btn btn--sm" onClick={() => void trustDevice(r, 30)}>
                      信任 30 天
                    </button>
                  )}
                </span>
              ),
            },
          ]}
          empty={<EmptyState title="没有已记录设备" body="登录后设备会出现在这里。" />}
        />
      </Card>
    </>
  );
}
