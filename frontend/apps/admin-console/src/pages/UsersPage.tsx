import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, newIdempotencyKey, useAuth, type AdminIdentity } from "@chain/api-client";
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
  dialogFormValues,
  useDialog,
  type DialogRequest,
} from "@chain/ui";
import type { StatusTone } from "@chain/ui";

/**
 * MFA 单元格取值口径。
 *
 * 优先展示注册状态（mfa_enrolled，当前用户接口尚未返回该字段）；
 * 只有策略要求字段（mfa_required）可用时，展示"要求/未要求"而不是猜测"已启用"。
 */
function MfaBadge({ row }: { row: AdminIdentity }) {
  if (row.mfa_enrolled === true) {
    return <StatusBadge tone="ok">已启用</StatusBadge>;
  }
  if (row.mfa_enrolled === false) {
    return <StatusBadge tone="neutral">未启用</StatusBadge>;
  }
  return (
    <StatusBadge tone={row.mfa_required ? "warn" : "neutral"}>
      {row.mfa_required ? "要求 MFA" : "未要求"}
    </StatusBadge>
  );
}

/** 详情弹窗里的注册状态描述：接口没返回就直说，不用策略字段冒充注册状态。 */
function describeMfaEnrollment(row: AdminIdentity): string {
  if (row.mfa_enrolled === true) return "已启用（已注册认证器）";
  if (row.mfa_enrolled === false) return "未启用";
  return "接口未返回（users 接口只提供 MFA 要求字段）";
}

/** 重置 MFA 的权限键（与后端 Guard 使用的键一致）。 */
const RESET_MFA_PERMISSION = "admin:user:reset_mfa";

interface UserDetail {
  identity: AdminIdentity;
  roles: string[] | null;
  active_sessions: unknown[] | null;
}

/** POST /commands/reset-mfa 的 data（破坏性命令）。 */
interface ResetMfaResult {
  reset: boolean;
  mfa_enrolled: boolean;
  /** true 表示被重置的就是当前登录账号本人：提交成功后本次会话已被吊销。 */
  self: boolean;
  removed?: { secret?: boolean; recovery_codes?: number } | null;
  sessions_revoked?: number;
}

/**
 * 邮箱脱敏：预览弹窗只展示脱敏账号，完整邮箱留在详情弹窗里按需查看。
 * 无 @ 或空值时退化为"首字符 + *"，绝不原样回显。
 */
function maskEmail(email: string): string {
  const raw = (email ?? "").trim();
  if (raw === "") return "—";
  const at = raw.lastIndexOf("@");
  if (at <= 0) return raw.slice(0, 1) + "***";
  const local = raw.slice(0, at);
  return local.slice(0, 1) + "*".repeat(Math.max(3, local.length - 1)) + raw.slice(at);
}

/**
 * 目标账号是否就是当前登录账号本人。
 *
 * 这是"你将被立即登出"的唯一判据：重置会吊销目标账号的全部会话，其中就包括
 * 当前这条管理会话。id 缺失时退回邮箱比对（同一组织内邮箱唯一）。
 */
function isSelfIdentity(identity: AdminIdentity, session: { user?: { id?: string }; email?: string } | null): boolean {
  if (!session) return false;
  const sameId = Boolean(session.user?.id) && session.user?.id === identity.id;
  const sameEmail = Boolean(session.email) && session.email === identity.email;
  return sameId || sameEmail;
}

interface ResetMfaFailure {
  title: string;
  message: string;
  /** true 表示"回到执行弹窗重新输入密码"，而不是结束流程。 */
  retryWithPassword: boolean;
}

/**
 * 重置 MFA 的失败分支。
 *
 * 错误码判定只按服务端契约里的稳定字符串；提示文案全部走弹窗（AGENTS.md）。
 */
function classifyResetMfaFailure(err: unknown): ResetMfaFailure {
  if (!(err instanceof ApiError)) {
    return { title: "重置 MFA 失败", message: "请稍后重试。", retryWithPassword: false };
  }
  switch (err.code) {
    case "STEP_UP_REQUIRED":
      return {
        title: "需要二次身份确认",
        message: "服务端要求提供当前账号密码后才能执行该破坏性操作，请重新输入密码。",
        retryWithPassword: true,
      };
    case "STEP_UP_INVALID":
      return {
        title: "当前账号密码不正确",
        message: "二次身份确认未通过：密码不正确。请重新输入当前账号密码。",
        retryWithPassword: true,
      };
    case "STEP_UP_UNAVAILABLE":
      return {
        title: "二次身份确认暂不可用",
        message: "服务端的 step-up 校验暂时不可用，本次未执行任何变更。可稍后重新提交，或取消本次操作。",
        retryWithPassword: true,
      };
    case "CONFLICT_MFA_NOT_ENROLLED":
      return {
        title: "该账号未启用 MFA",
        message: "目标账号当前没有启用 MFA，不存在可删除的认证器密钥与恢复码，服务端未做任何变更。",
        retryWithPassword: false,
      };
    case "AUTHZ_FORBIDDEN":
      return {
        title: "没有重置 MFA 的权限",
        message: `当前账号未被授予 ${RESET_MFA_PERMISSION}，无法重置 MFA。请联系组织 Owner 调整角色绑定。`,
        retryWithPassword: false,
      };
    case "RESOURCE_NOT_FOUND":
      return {
        title: "目标账号不存在或不可见",
        message: "该账号不存在，或不在当前组织与工作区的可见范围内，请刷新列表后重试。",
        retryWithPassword: false,
      };
    case "CONFLICT_CONFIRMATION_REQUIRED":
      return {
        title: "缺少影响确认",
        message: "服务端要求显式确认影响（confirm=true）后才能执行，请重新走一次确认流程。",
        retryWithPassword: false,
      };
    default:
      return { title: "重置 MFA 失败", message: err.userMessage, retryWithPassword: false };
  }
}

/** 新建用户 / 删除（离职）的权限键（与后端 Guard 使用的键一致）。 */
const CREATE_PERMISSION = "admin:user:create";
const DELETE_PERMISSION = "admin:user:delete";

/** POST /api/admin/v1/users 的 data：一次性初始密码只在本次响应里出现。 */
interface CreateUserResult {
  id: string;
  email: string;
  display_name: string;
  role_key: string;
  organization_id: string;
  /** 管理员显式指定共享工作区时才有值。 */
  workspace_id?: string;
  /** 建号时自动创建的个人空间（迁移 0066）：新用户的数据默认归属地。 */
  personal_workspace_id?: string;
  /** 个人空间里的默认角色（当前为 basic_user）。 */
  default_role_key?: string;
  initial_password: string;
  plaintext_returned_once: boolean;
  must_change_password: boolean;
}

/** DELETE /api/admin/v1/users/{id} 的 data（软删 + 匿名化 + 归档个人空间）。 */
interface DeleteUserResult {
  deleted: boolean;
  identity_id: string;
  status: string;
  anonymous_email: string;
  archived_personal_workspaces?: string[];
  note?: string;
}

/** 复制到剪贴板：不可用时返回 false，由调用方以弹窗说明（不静默失败）。 */
async function copyToClipboard(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return false;
  }
}

/**
 * 新建用户的失败分支。错误码只按服务端契约里的稳定字符串匹配；
 * 提示文案一律走弹窗（AGENTS.md：正文不放提示条）。
 */
function classifyCreateUserFailure(err: unknown): { title: string; message: string } {
  if (!(err instanceof ApiError)) {
    return { title: "创建用户失败", message: "请稍后重试。" };
  }
  switch (err.code) {
    case "CONFLICT_DUPLICATE":
      return { title: "该邮箱已存在", message: "同一组织内邮箱唯一，请改用其他邮箱或先检索该账号。" };
    case "VALIDATION_UNKNOWN_ROLE":
      return {
        title: "角色键不存在",
        message: "填写的角色键不在系统角色目录中。留空即为基础用户（basic_user），或填写 viewer / analyst / investigator / reviewer / auditor / owner / security_admin / platform_admin。",
      };
    case "VALIDATION_INVALID_ARGUMENT":
      return { title: "参数不合法", message: err.userMessage || "请检查邮箱格式与必填项。" };
    case "CONFLICT_CONFIRMATION_REQUIRED":
      return { title: "缺少确认", message: "服务端要求显式确认后才能执行，请重新走一次流程。" };
    case "permission_denied":
    case "AUTHZ_FORBIDDEN":
      return {
        title: "没有创建用户的权限",
        message: `当前账号未被授予 ${CREATE_PERMISSION}，无法创建用户。请联系组织 Owner 调整角色绑定。`,
      };
    default:
      return { title: "创建用户失败", message: err.userMessage };
  }
}

/** 删除（离职）的失败分支。 */
function classifyDeleteUserFailure(err: unknown): { title: string; message: string } {
  if (!(err instanceof ApiError)) {
    return { title: "删除用户失败", message: "请稍后重试。" };
  }
  switch (err.code) {
    case "CONFLICT_ALREADY_DELETED":
      return { title: "该用户已删除", message: "该账号此前已被删除（身份行保留用于审计），无需重复操作。" };
    case "CONFLICT_LAST_OWNER":
      return {
        title: "不能删除最后一名 Owner",
        message: "该账号是组织内唯一可用的 Owner。请先为组织补充另一名 Owner，再执行删除。",
      };
    case "VALIDATION_SELF_LOCKOUT":
      return { title: "不能删除当前登录账号", message: "删除会立即吊销本会话。请改用其他管理员账号执行该操作。" };
    case "CONFLICT_CONFIRMATION_REQUIRED":
      return { title: "缺少确认", message: "服务端要求显式确认（confirm=true）后才能执行，请重新走一次确认流程。" };
    case "permission_denied":
    case "AUTHZ_FORBIDDEN":
      return {
        title: "没有删除用户的权限",
        message: `当前账号未被授予 ${DELETE_PERMISSION}，无法删除用户。`,
      };
    case "RESOURCE_NOT_FOUND":
    case "NOT_FOUND":
      return { title: "目标账号不存在或不可见", message: "该账号不存在，或不在当前组织的可见范围内，请刷新列表后重试。" };
    default:
      return { title: "删除用户失败", message: err.userMessage };
  }
}

/** §19.3 用户与成员管理（A-USER-01/02）。高风险操作带二次确认与原因。 */
export default function UsersPage() {
  const { client, can, session } = useAuth();
  const dialog = useDialog();
  /**
   * 重置 MFA 是"预览 → 执行 → 结果"的多步流程，任何时刻只允许一条在途流程：
   * 重复点击不得提交第二次（破坏性操作不接受并发重放）。
   */
  const resetFlowRef = useRef(false);
  const [resetting, setResetting] = useState(false);
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
      const res = await client.get<UserDetail>(`/api/admin/v1/users/${row.id}`);
      const detail = res.data;
      const identity = detail.identity;
      /**
       * 只有服务端明确回报 mfa_enrolled=false 才禁用入口。
       * 字段缺失（旧部署的 users 接口不返回注册状态）时不能当成"未启用"——
       * 那会让本可执行的运维动作被前端凭空挡住，此时保持可用，由服务端以
       * 409 CONFLICT_MFA_NOT_ENROLLED 给出权威结论。
       */
      const knownNotEnrolled = identity.mfa_enrolled === false;
      const allowed = can(RESET_MFA_PERMISSION);
      const resetDisabled = !allowed || knownNotEnrolled;
      const resetDisabledReason = !allowed
        ? `当前账号未被授予 ${RESET_MFA_PERMISSION}，无法重置 MFA`
        : "该账号未启用 MFA，没有可删除的认证器密钥与恢复码";
      const activeSessions = Array.isArray(detail.active_sessions) ? detail.active_sessions.length : 0;
      /**
       * 删除（离职）的可用性三分支：无权限 / 目标是本人 / 目标已删除。
       * 与重置 MFA 同一约定——不可用时必须把**原因写进弹窗**，而不是只靠 disabled 按钮的 tooltip。
       */
      const deleteAllowed = can(DELETE_PERMISSION);
      const deleteSelf = isSelfIdentity(identity, session);
      const deleteAlready = identity.status === "deleted";
      const deleteDisabled = !deleteAllowed || deleteSelf || deleteAlready;
      const deleteDisabledReason = !deleteAllowed
        ? `当前账号未被授予 ${DELETE_PERMISSION}，无法删除用户`
        : deleteSelf
          ? "不能删除当前登录账号（删除会立即吊销本会话）"
          : "该账号已删除（身份行保留用于审计，无需重复操作）";

      const answer = await dialog.open({
        kind: "info",
        title: identity.display_name || identity.email,
        body: null,
        details: [
          { label: "用户 ID", value: <span className="mono">{identity.id}</span> },
          { label: "邮箱", value: identity.email },
          { label: "状态", value: <StatusBadge tone={statusToneFor(identity.status) as StatusTone}>{identity.status}</StatusBadge> },
          // roles 可能为 JSON null（该身份没有任何角色绑定）：归一化成空数组再展示。
          { label: "角色", value: (detail.roles ?? []).join(", ") || "—" },
          { label: "组织", value: <span className="mono">{identity.organization_id}</span> },
          { label: "已有密码凭据", value: identity.has_password ? "是" : "否" },
          { label: "MFA 要求（策略）", value: identity.mfa_required ? "是" : "否" },
          { label: "MFA 注册状态", value: describeMfaEnrollment(identity) },
          { label: "需要修改密码", value: identity.must_change_password ? "是" : "否" },
          { label: "失败登录次数", value: String(identity.failed_login_count) },
          { label: "最近登录", value: identity.last_login_at ? new Date(identity.last_login_at).toLocaleString("zh-CN") : "—" },
          { label: "活跃会话", value: String(activeSessions) },
          // 禁用原因同时写在这里：部分浏览器不会为 disabled 按钮显示 tooltip，
          // 能力边界必须让用户看得见（而不是靠悬停才发现）。
          ...(resetDisabled ? [{ label: "重置 MFA", value: `不可用：${resetDisabledReason}` }] : []),
          ...(deleteDisabled ? [{ label: "删除（离职）", value: `不可用：${deleteDisabledReason}` }] : []),
        ],
        actions: [
          { label: "关闭", variant: "default", result: "close" },
          {
            label: "重置 MFA",
            variant: "danger",
            result: "reset-mfa",
            disabled: resetDisabled || resetting,
            title: resetDisabled ? resetDisabledReason : undefined,
          },
          {
            label: "删除（离职）",
            variant: "danger",
            result: "delete-user",
            disabled: deleteDisabled || deleting,
            title: deleteDisabled ? deleteDisabledReason : undefined,
          },
        ],
      } satisfies DialogRequest);
      if (answer === "reset-mfa") await runResetMfa(detail);
      if (answer === "delete-user") await runDeleteUser(detail);
    } catch (err) {
      dialog.notify("error", "读取用户详情失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    }
  };

  /**
   * 重置 MFA（破坏性命令）的三段式编排：影响预览 → 执行（原因 + step-up 密码 + 影响确认）→ 结果。
   *
   * 所有提示都走弹窗（AGENTS.md）：页面正文只保留业务数据与操作控件。
   * step-up 失败时回到执行弹窗重新输入密码，而不是把整条流程推倒重来。
   */
  const runResetMfa = async (detail: UserDetail) => {
    if (resetFlowRef.current) return;
    resetFlowRef.current = true;
    setResetting(true);
    const identity = detail.identity;
    const masked = maskEmail(identity.email);
    const activeSessions = Array.isArray(detail.active_sessions) ? detail.active_sessions.length : 0;
    const selfReset = isSelfIdentity(identity, session);
    try {
      // ① 影响预览：破坏性操作的后果必须在执行前逐项列出。
      const preview = await dialog.confirm({
        kind: "warning",
        title: "重置 MFA · 影响预览",
        body: (
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
            <p style={{ margin: 0 }}>
              这是破坏性且不可撤销的操作：删除该账号的认证器 TOTP 密钥与全部恢复码，并吊销其全部会话。
            </p>
            <p style={{ margin: 0 }}>后果：此后该账号登录不再需要第二步验证码。</p>
            {selfReset ? (
              <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>
                目标账号是当前登录账号本人：提交成功后你将被立即登出，需要重新登录。
              </p>
            ) : null}
          </div>
        ),
        details: [
          { label: "目标账号", value: <span className="mono">{masked}</span> },
          { label: "当前 MFA 状态", value: describeMfaEnrollment(identity) },
          { label: "将删除", value: "认证器 TOTP 密钥 + 全部未使用的恢复码" },
          { label: "将吊销的活跃会话", value: `${activeSessions} 个` },
          { label: "操作类型", value: "破坏性 · 写入审计日志" },
          ...(selfReset ? [{ label: "注意", value: "目标是当前登录账号：你将被立即登出" }] : []),
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "继续", result: "continue", variant: "danger" },
        ],
      });
      if (preview !== "continue") return;

      // ② 执行：原因（必填）+ 当前账号密码（step-up，必填）+ 影响确认（必勾选）。
      // 三项齐全前提交按钮保持 disabled；step-up 失败时带着失败原因回到本弹窗。
      let reason = "";
      let acked = false;
      let notice: string | null = null;
      for (;;) {
        const values = dialogFormValues(
          await dialog.open({
            kind: "warning",
            title: "重置 MFA · 执行",
            body: (
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
                {notice ? <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>{notice}</p> : null}
                <p style={{ margin: 0 }}>
                  删除该账号的认证器 TOTP 密钥与全部恢复码，并吊销其全部会话；该操作不可撤销。
                </p>
                {selfReset ? (
                  <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>
                    目标账号是当前登录账号本人：提交成功后你将被立即登出，需要重新登录。
                  </p>
                ) : null}
              </div>
            ),
            details: [
              { label: "目标账号", value: <span className="mono">{masked}</span> },
              { label: "当前 MFA 状态", value: describeMfaEnrollment(identity) },
              { label: "将删除", value: "认证器 TOTP 密钥 + 全部未使用的恢复码" },
              { label: "将吊销的活跃会话", value: `${activeSessions} 个` },
            ],
            fields: [
              {
                name: "reason",
                label: "操作原因（必填，写入审计日志）",
                placeholder: "例如：员工更换手机且未保存恢复码",
                required: true,
                pattern: /^.{1,500}$/,
                patternHint: "1-500 个字符",
                defaultValue: reason,
              },
              {
                // 密码永不预填：每次重试都必须重新输入。
                name: "password",
                label: "当前账号密码（二次身份确认，必填）",
                type: "password",
                placeholder: "请输入当前登录账号的密码",
                required: true,
                patternHint: "请输入当前账号密码",
                autoComplete: "current-password",
              },
              {
                name: "ack",
                label: "我已确认上述影响",
                type: "checkbox",
                required: true,
                patternHint: "请先勾选确认后再提交",
                defaultValue: acked ? "true" : "",
              },
            ],
            actions: [
              { label: "取消", result: "", variant: "default" },
              { label: "确认重置", result: "reset", variant: "danger" },
            ],
          } satisfies DialogRequest),
        );
        if (!values) return; // 用户取消：本次不提交任何请求。
        reason = (values.reason ?? "").trim();
        acked = values.ack === "true";
        const password = values.password ?? "";

        try {
          const res = await client.post<ResetMfaResult>(
            `/api/admin/v1/users/${identity.id}/commands/reset-mfa`,
            { confirm: true, reason, step_up_password: password },
            { idempotencyKey: newIdempotencyKey() },
          );
          const data = res.data;
          // ③ 结果：以服务端回报为准（self 表示被重置的是当前登录账号本人）。
          const selfNow = data?.self ?? selfReset;
          const removed = data?.removed ?? null;
          const revoked = typeof data?.sessions_revoked === "number" ? data.sessions_revoked : 0;
          dialog.notify(
            "success",
            "MFA 已重置",
            selfNow ? (
              <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>你已被登出，请重新登录。</p>
            ) : (
              "该账号的认证器密钥与恢复码已删除，全部会话已吊销；如需重新启用 MFA，请由该账号本人在「个人资料与安全」中自助注册。"
            ),
            [
              { label: "目标账号", value: <span className="mono">{masked}</span> },
              {
                label: "已删除 TOTP 密钥",
                value: removed && typeof removed.secret === "boolean" ? (removed.secret ? "是" : "否") : "接口未返回",
              },
              {
                label: "已删除未使用恢复码",
                value:
                  removed && typeof removed.recovery_codes === "number"
                    ? `${removed.recovery_codes} 枚`
                    : "接口未返回数量",
              },
              { label: "已吊销会话", value: `${revoked} 个` },
              { label: "当前 MFA 状态", value: data?.mfa_enrolled ? "已启用" : "未启用" },
            ],
          );
          // ④ 刷新列表；被重置的不是本人时再打开一次详情（MFA 注册状态已变为未启用）。
          await load();
          if (!selfNow) await openUser(identity);
          return;
        } catch (err) {
          const failure = classifyResetMfaFailure(err);
          if (failure.retryWithPassword) {
            // 停在执行弹窗：把失败原因带回去，密码需要重新输入。
            notice = `${failure.title}：${failure.message}`;
            continue;
          }
          dialog.notify(
            "error",
            failure.title,
            failure.message,
            err instanceof ApiError
              ? [
                  { label: "错误码", value: <span className="mono">{err.code}</span> },
                  ...(err.traceId ? [{ label: "Trace ID", value: err.traceId }] : []),
                ]
              : undefined,
          );
          return;
        }
      }
    } finally {
      resetFlowRef.current = false;
      setResetting(false);
    }
  };

  /**
   * 新建用户（决策 D5：账号与初始密码由管理员在后台生成，系统无自助注册）。
   *
   * 三段式：填写 → 提交 → 结果。结果弹窗里的**一次性初始密码只显示这一次**，
   * 服务端响应也带 plaintext_returned_once=true，关闭后无法再次查看。
   * 建号同时创建该用户的个人空间（迁移 0066 / 决策 D1）：新用户的数据默认归属自己的空间，
   * 不会被塞进共享工作区。
   */
  const createFlowRef = useRef(false);
  const [creating, setCreating] = useState(false);

  const copyInitialPassword = async (value: string) => {
    const ok = await copyToClipboard(value);
    if (!ok) {
      dialog.notify("warning", "复制失败", "浏览器未授予剪贴板权限，请手动选中密码后复制。");
    }
  };

  const runCreateUser = async () => {
    if (createFlowRef.current) return;
    createFlowRef.current = true;
    setCreating(true);
    try {
      let email = "";
      let displayName = "";
      let roleKey = "";
      let reason = "";
      let notice: string | null = null;
      for (;;) {
        const values = dialogFormValues(
          await dialog.open({
            kind: "info",
            title: "新建用户",
            body: (
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
                {notice ? <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>{notice}</p> : null}
                <p style={{ margin: 0 }}>
                  系统由管理员建号：提交后生成一次性初始密码，该用户首次登录必须修改密码，且在此之前无法访问业务接口。
                </p>
                <p style={{ margin: 0 }}>
                  建号会同时为该用户创建<strong>个人空间</strong>，其案件与调查默认只属于他自己。
                </p>
              </div>
            ),
            fields: [
              {
                name: "email",
                label: "登录邮箱（必填）",
                placeholder: "例如：zhangsan@example.com",
                required: true,
                pattern: /^[^\s@]+@[^\s@]+\.[^\s@]+$/,
                patternHint: "请输入合法邮箱地址",
                defaultValue: email,
              },
              {
                // 注意：Dialog 的字段**默认必填**（Dialog.tsx: required !== false 即视为必填），
                // 所以"选填"必须显式写 required: false，否则留空时提交按钮永远 disabled。
                name: "display_name",
                label: "显示名称（选填）",
                placeholder: "留空则使用邮箱",
                required: false,
                pattern: /^.{0,64}$/,
                patternHint: "最多 64 个字符",
                defaultValue: displayName,
              },
              {
                name: "role_key",
                label: "附加角色键（选填，按组织范围绑定）",
                placeholder: "留空 = 仅基础用户 basic_user",
                required: false,
                pattern: /^[a-z_]{0,32}$/,
                patternHint: "仅小写字母与下划线；留空即为基础用户",
                defaultValue: roleKey,
              },
              {
                name: "reason",
                label: "创建原因（必填，写入审计日志）",
                placeholder: "例如：新同事入职，需要调查工作台账号",
                required: true,
                pattern: /^.{1,500}$/,
                patternHint: "1-500 个字符",
                defaultValue: reason,
              },
            ],
            actions: [
              { label: "取消", result: "", variant: "default" },
              { label: "创建账号", result: "create", variant: "primary" },
            ],
          } satisfies DialogRequest),
        );
        if (!values) return; // 用户取消：不提交任何请求。
        email = (values.email ?? "").trim();
        displayName = (values.display_name ?? "").trim();
        roleKey = (values.role_key ?? "").trim();
        reason = (values.reason ?? "").trim();

        try {
          const res = await client.post<CreateUserResult>(
            "/api/admin/v1/users",
            { email, display_name: displayName, role_key: roleKey, reason },
            { idempotencyKey: newIdempotencyKey() },
          );
          const data = res.data;
          dialog.notify(
            "success",
            "用户已创建",
            <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
              <p style={{ margin: 0 }}>
                初始密码只在本弹窗显示一次，关闭后无法再次查看，请立即转交本人；该用户首次登录会被强制修改密码。
              </p>
              <div style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)" }}>
                <code className="mono" style={{ fontSize: "var(--fs-14)" }}>
                  {data?.initial_password ?? "（接口未返回）"}
                </code>
                {data?.initial_password ? (
                  <button type="button" className="btn btn--sm" onClick={() => void copyInitialPassword(data.initial_password)}>
                    复制密码
                  </button>
                ) : null}
              </div>
            </div>,
            [
              { label: "邮箱", value: data?.email ?? email },
              { label: "用户 ID", value: <span className="mono">{data?.id ?? "—"}</span> },
              {
                label: "个人空间",
                value: data?.personal_workspace_id ? (
                  <span className="mono">{data.personal_workspace_id}</span>
                ) : (
                  "接口未返回"
                ),
              },
              { label: "个人空间默认角色", value: data?.default_role_key ?? "basic_user" },
              { label: "共享工作区", value: data?.workspace_id ? <span className="mono">{data.workspace_id}</span> : "未加入（仅个人空间）" },
              { label: "附加角色", value: roleKey || "无（仅基础用户）" },
              { label: "强制首次改密", value: data?.must_change_password ? "是" : "否" },
              { label: "密码可见性", value: "仅此一次（服务端标记 plaintext_returned_once）" },
            ],
          );
          await load();
          return;
        } catch (err) {
          const failure = classifyCreateUserFailure(err);
          const terminal =
            err instanceof ApiError && (err.code === "permission_denied" || err.code === "AUTHZ_FORBIDDEN");
          if (terminal) {
            dialog.notify(
              "error",
              failure.title,
              failure.message,
              err instanceof ApiError
                ? [
                    { label: "错误码", value: <span className="mono">{err.code}</span> },
                    ...(err.traceId ? [{ label: "Trace ID", value: err.traceId }] : []),
                  ]
                : undefined,
            );
            return;
          }
          notice = `${failure.title}：${failure.message}`;
          continue;
        }
      }
    } finally {
      createFlowRef.current = false;
      setCreating(false);
    }
  };

  /**
   * 删除用户（离职）（决策 D6）：软删 + 匿名化 + 归档个人空间 + 撤权 + 强制下线。
   *
   * 身份行**不会**被物理删除（审计与登录事件通过外键引用它），因此这里向操作者说明的是
   * "归档为只读 + 匿名化"，而不是"数据消失"。
   */
  const deleteFlowRef = useRef(false);
  const [deleting, setDeleting] = useState(false);

  const runDeleteUser = async (detail: UserDetail) => {
    if (deleteFlowRef.current) return;
    deleteFlowRef.current = true;
    setDeleting(true);
    const identity = detail.identity;
    const masked = maskEmail(identity.email);
    const activeSessions = Array.isArray(detail.active_sessions) ? detail.active_sessions.length : 0;
    try {
      // ① 影响预览：破坏性且不可撤销的后果必须逐项列出。
      const preview = await dialog.confirm({
        kind: "warning",
        title: "删除用户（离职）· 影响预览",
        body: (
          <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
            <p style={{ margin: 0 }}>
              这是破坏性且不可撤销的操作：该账号立即失效，其个人空间归档为只读，邮箱与显示名被匿名化。
            </p>
            <p style={{ margin: 0 }}>
              为保持审计可回溯，身份行本身**保留**（不物理删除），历史登录与操作记录仍可查。
            </p>
          </div>
        ),
        details: [
          { label: "目标账号", value: <span className="mono">{masked}</span> },
          { label: "用户 ID", value: <span className="mono">{identity.id}</span> },
          { label: "当前状态", value: <StatusBadge tone={statusToneFor(identity.status) as StatusTone}>{identity.status}</StatusBadge> },
          { label: "角色", value: (detail.roles ?? []).join(", ") || "—" },
          { label: "将吊销的活跃会话", value: `${activeSessions} 个` },
          { label: "将撤销", value: "全部角色绑定、组织与工作区成员关系" },
          { label: "将归档", value: "该用户的个人空间（其下对象转为只读）" },
          { label: "将匿名化", value: "登录邮箱与显示名称" },
          { label: "保留", value: "身份行（审计与登录事件的外键引用）" },
          { label: "操作类型", value: "破坏性 · 不可撤销 · 写入审计日志" },
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "继续", result: "continue", variant: "danger" },
        ],
      });
      if (preview !== "continue") return;

      // ② 执行：原因（必填）+ 影响确认（必勾选）。
      let reason = "";
      let acked = false;
      let notice: string | null = null;
      for (;;) {
        const values = dialogFormValues(
          await dialog.open({
            kind: "warning",
            title: "删除用户（离职）· 执行",
            body: (
              <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}>
                {notice ? <p style={{ margin: 0, color: "var(--c-danger)", fontWeight: 600 }}>{notice}</p> : null}
                <p style={{ margin: 0 }}>
                  确认后该账号立即失效且不可恢复；其个人空间归档为只读，身份行保留用于审计。
                </p>
              </div>
            ),
            details: [
              { label: "目标账号", value: <span className="mono">{masked}</span> },
              { label: "将吊销的活跃会话", value: `${activeSessions} 个` },
            ],
            fields: [
              {
                name: "reason",
                label: "删除原因（必填，写入审计日志）",
                placeholder: "例如：员工离职，账号需回收",
                required: true,
                pattern: /^.{1,500}$/,
                patternHint: "1-500 个字符",
                defaultValue: reason,
              },
              {
                name: "ack",
                label: "我已确认上述影响（不可撤销）",
                type: "checkbox",
                required: true,
                patternHint: "请先勾选确认后再提交",
                defaultValue: acked ? "true" : "",
              },
            ],
            actions: [
              { label: "取消", result: "", variant: "default" },
              { label: "确认删除", result: "delete", variant: "danger" },
            ],
          } satisfies DialogRequest),
        );
        if (!values) return; // 用户取消：不提交任何请求。
        reason = (values.reason ?? "").trim();
        acked = values.ack === "true";

        try {
          const res = await client.delete<DeleteUserResult>(`/api/admin/v1/users/${identity.id}`, {
            body: { confirm: true, reason },
            idempotencyKey: newIdempotencyKey(),
          });
          const data = res.data;
          const archived = data?.archived_personal_workspaces ?? [];
          dialog.notify(
            "success",
            "用户已删除（离职）",
            data?.note ?? "该账号已失效；个人空间已归档为只读，身份行保留用于审计。",
            [
              { label: "目标账号", value: <span className="mono">{masked}</span> },
              { label: "匿名化邮箱", value: <span className="mono">{data?.anonymous_email ?? "—"}</span> },
              {
                label: "已归档个人空间",
                value: archived.length ? archived.map((id) => <div key={id} className="mono">{id}</div>) : "无（该账号没有个人空间）",
              },
              { label: "身份状态", value: data?.status ?? "deleted" },
              { label: "登录状态", value: "该账号登录已被拒绝；此前下发的会话已失效" },
            ],
          );
          await load();
          return;
        } catch (err) {
          const failure = classifyDeleteUserFailure(err);
          const retryable = err instanceof ApiError && err.code === "CONFLICT_CONFIRMATION_REQUIRED";
          if (retryable) {
            notice = `${failure.title}：${failure.message}`;
            continue;
          }
          dialog.notify(
            "error",
            failure.title,
            failure.message,
            err instanceof ApiError
              ? [
                  { label: "错误码", value: <span className="mono">{err.code}</span> },
                  ...(err.traceId ? [{ label: "Trace ID", value: err.traceId }] : []),
                ]
              : undefined,
          );
          await load();
          return;
        }
      }
    } finally {
      deleteFlowRef.current = false;
      setDeleting(false);
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
          <>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() =>
                dialog.notify("info", "MFA 列的取值口径", null, [
                  { label: "接口", value: "GET /api/admin/v1/users" },
                  { label: "MFA 要求", value: "identities.mfa_required：策略要求该身份登录时完成 MFA" },
                  { label: "MFA 注册状态", value: "该接口目前不返回 mfa_enrolled；返回后本列会直接显示已启用/未启用" },
                  { label: "本人查看", value: "身份本人在用户端「个人资料与安全」中自助启用/关闭 MFA" },
                  { label: "启用方式", value: "认证器 TOTP（6 位/30 秒）+ 10 枚一次性恢复码" },
                ])
              }
            >
              MFA 口径
            </button>
            <button
              type="button"
              className="btn btn--primary btn--sm"
              onClick={() => void runCreateUser()}
              disabled={!can(CREATE_PERMISSION) || creating}
              title={can(CREATE_PERMISSION) ? "生成账号与一次性初始密码，并创建其个人空间" : `当前账号未被授予 ${CREATE_PERMISSION}`}
            >
              {creating ? "创建中…" : "新建用户"}
            </button>
          </>
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
            { key: "mfa", header: "MFA", width: "130px", render: (r) => <MfaBadge row={r} /> },
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
