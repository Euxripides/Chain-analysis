import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Field, StatusBadge, useDialog } from "@chain/ui";
import {
  ApiError,
  MFA_ERROR_CODES,
  isMfaChallenge,
  mfaChallengeDeadline,
  mfaSecondsLeft,
  useAuth,
  workspaceSelectionOptions,
  type LoginResult,
  type MfaChallenge,
  type WorkspaceOption,
} from "@chain/api-client";
import { sanitizeReturnTo } from "../lib/returnTo";

/**
 * §19 后台登录页（含 §19.9 MFA 第二步）。管理会话与用户会话分离（不同 Cookie、不同受众）。
 *
 * 第二步与用户层行为一致：202/state=mfa_required 时不出会话，前端渲染验证码表单；
 * 挑战令牌与验证码只存在于内存与请求体，绝不写入 URL（包括 return_to 重定向）或日志。
 */

/** 第二步的本地状态：只保留渲染与倒计时需要的字段。 */
interface MfaStepState {
  challengeId: string;
  deadline: number;
  methods: string[];
  next: string;
  email: string;
}

function formatCountdown(seconds: number): string {
  const mm = String(Math.floor(seconds / 60)).padStart(2, "0");
  const ss = String(seconds % 60).padStart(2, "0");
  return `${mm}:${ss}`;
}

export default function LoginPage() {
  const { login, verifyMfa } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const location = useLocation();
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [mfa, setMfa] = useState<MfaStepState | null>(null);
  const [code, setCode] = useState("");
  const [secondsLeft, setSecondsLeft] = useState(0);
  // R-3：旧 V1 后台页面（/api-control、/system）通过 ?return_to= 把用户送进来。
  // 只接受本地白名单内的站内路径；其余一律忽略（防开放重定向）。
  const returnTo = sanitizeReturnTo(new URLSearchParams(location.search).get("return_to"));

  /**
   * 第二步的剩余有效时间倒计时：归零即回到第一步。
   * 服务端仍是唯一裁判（过期返回 AUTHN_MFA_CHALLENGE_INVALID）。
   */
  useEffect(() => {
    if (!mfa) return;
    const tick = () => {
      const left = mfaSecondsLeft(mfa.deadline);
      setSecondsLeft(left);
      if (left <= 0) {
        setMfa(null);
        setCode("");
        dialog.notify("warning", "本次验证已超时", "第二步验证码的有效时间已结束，请重新输入管理员邮箱与密码。");
      }
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mfa]);

  /**
   * 多工作区身份的选择器（P1-20260913，后台侧对齐用户层）。
   *
   * gap found by the C-round browser matrix: a multi-workspace admin identity could NOT
   * sign in at all through /admin/login. The server answered 409
   * AUTHN_WORKSPACE_SELECTION_REQUIRED and the page rendered a generic
   * "该账号属于多个工作区，请先选择要进入的工作区" dialog with no way to choose.
   * The user console had the selector; the admin console did not. Behaviour must not
   * differ by audience: the server never picks a default workspace, and the client must
   * ask. Selection happens in a dialog (repository rule: notices are dialogs only).
   */
  const chooseWorkspace = async (options: WorkspaceOption[]): Promise<string | null> => {
    const picked = await dialog.confirm({
      kind: "info",
      title: "请选择工作区",
      body: "该管理员账号属于多个工作区。出于安全考虑，系统不会替你选择默认工作区：请选择本次管理会话要进入的工作区。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        ...options.map((option) => ({
          label: option.name ? `${option.name}（${option.slug}）` : option.slug,
          value: option.id,
          result: option.id,
          variant: "primary" as const,
        })),
      ],
    });
    return picked && picked !== "" ? picked : null;
  };

  /** 进入第二步：服务端尚未下发 Cookie，本次登录还未完成。 */
  const enterMfaStep = (challenge: MfaChallenge) => {
    const deadline = mfaChallengeDeadline(challenge);
    setMfa({
      challengeId: challenge.challenge_id,
      deadline,
      methods: challenge.methods ?? [],
      next: challenge.next || "/overview",
      email: challenge.user?.email || identifier.trim(),
    });
    setCode("");
    setSecondsLeft(mfaSecondsLeft(deadline));
  };

  /** 退回第一步：清空挑战与验证码，并说明原因（提示只走弹窗）。 */
  const backToPasswordStep = (title: string, body: string, details?: Array<{ label: string; value: React.ReactNode }>) => {
    setMfa(null);
    setCode("");
    dialog.notify("warning", title, body, details);
  };

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!identifier.trim() || !password) {
      dialog.notify("warning", "请填写完整", "账号与密码都不能为空。");
      return;
    }
    setSubmitting(true);
    try {
      let workspaceId: string | undefined;
      for (let attempt = 0; attempt < 2; attempt += 1) {
        try {
          const result = await login(identifier.trim(), password, "admin", workspaceId);
          // 202 / state=mfa_required：需要第二步，不能跳转（此时没有会话）。
          if (isMfaChallenge(result)) {
            enterMfaStep(result);
            return;
          }
          return await finishLogin(result);
        } catch (err) {
          const options = workspaceSelectionOptions(err);
          if (options === null || attempt > 0) {
            throw err;
          }
          if (options.length === 0) {
            // The server asked for a choice but offered none: never invent one.
            dialog.notify("error", "无法选择工作区", "该管理员账号在当前组织内没有可用的工作区成员资格。");
            return;
          }
          const picked = await chooseWorkspace(options);
          if (!picked) {
            return;
          }
          workspaceId = picked;
        }
      }
    } catch (err) {
      dialog.notify("error", "登录失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setSubmitting(false);
    }
  };

  /**
   * 第二步提交。TOTP 与恢复码共用同一个输入框：
   * 长度大于 6 时按恢复码提交，其余交给服务端判定。
   */
  const submitMfa = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!mfa || submitting) return;
    const value = code.trim();
    if (value.length < 6) {
      dialog.notify("warning", "请输入验证码", "请输入认证器上的 6 位验证码；认证器不可用时，也可以输入一个恢复码。");
      return;
    }
    setSubmitting(true);
    try {
      const result = await verifyMfa(mfa.challengeId, value);
      setCode("");
      setMfa(null);
      await finishLogin(result);
    } catch (err) {
      setCode("");
      if (!(err instanceof ApiError)) {
        dialog.notify("error", "第二步验证未通过", "请稍后重试");
        return;
      }
      if (err.code === MFA_ERROR_CODES.invalid) {
        dialog.notify("error", "验证码不正确", "请确认认证器与本机时间同步；恢复码只能用一次。");
      } else if (err.code === MFA_ERROR_CODES.challengeInvalid) {
        backToPasswordStep("本次验证已失效", "第二步验证已过期或已被使用，请重新输入管理员邮箱与密码。");
      } else if (err.code === MFA_ERROR_CODES.attemptsExhausted) {
        backToPasswordStep("尝试次数已用尽", "为避免暴力猜测，该账号短时间内不能再提交验证码。", [
          { label: "可重试时间", value: err.retryAfterSeconds === undefined ? "请稍后重试" : `${err.retryAfterSeconds} 秒后` },
          { label: "下一步", value: "重新输入管理员邮箱与密码" },
        ]);
      } else {
        dialog.notify("error", "第二步验证未通过", err.userMessage);
      }
    } finally {
      setSubmitting(false);
    }
  };

  const finishLogin = async (result: LoginResult) => {
      // 跨 SPA 返回必须用**真实浏览器跳转**：目标在 /admin/ 之外（旧 V1 SPA），
      // 用本路由的 navigate 会被 basename 拼成 /admin/api-control 这样的死路由。
      if (returnTo) {
        window.location.assign(returnTo);
        return;
      }
    const from = (location.state as { from?: string } | null)?.from;
    const target = from && from !== "/login" ? from.replace(/^\/admin/, "") || "/overview" : result.next || "/overview";
    navigate(target, { replace: true });
  };

  const supportsRecoveryCode = Boolean(mfa?.methods.includes("recovery_code"));

  return (
    <div className="auth-page">
      {/* 品牌面板：与用户端同款左右分栏（同一套 CSS，两个控制台视觉一致）。 */}
      <aside className="auth-aside">
        <div className="auth-aside__top">
          <span className="auth-aside__mark">
            <span className="shell__brand-mark" aria-hidden="true">AD</span>
            <span>
              <span className="auth-aside__name">ChainSight Admin</span>
              <span className="auth-aside__tagline">后台管理系统</span>
            </span>
          </span>
        </div>
        <div className="auth-aside__body">
          <h2 className="auth-aside__title">
            账号、权限与审计
            <br />
            统一在这里管理
          </h2>
        </div>
      </aside>
      <div className="auth-card">
        <div className="auth-card__brand">
          <span className="shell__brand-mark" aria-hidden="true">
            AD
          </span>
          <div>
            <div className="auth-card__title">ChainSight 后台管理</div>
            {mfa ? <div className="auth-card__sub">两步验证 · 第二步</div> : null}
          </div>
        </div>
        {mfa ? (
          <form onSubmit={submitMfa} noValidate>
            <div className="toolbar" style={{ justifyContent: "space-between" }}>
              <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
                {mfa.email}
              </span>
              <StatusBadge tone={secondsLeft <= 60 ? "warn" : "info"}>
                剩余 {formatCountdown(secondsLeft)}
              </StatusBadge>
            </div>
            <Field
              label="认证器验证码"
              htmlFor="mfa_code"
              hint={supportsRecoveryCode ? "6 位数字；认证器不可用时，也可以输入一个恢复码。" : "认证器上显示的 6 位数字验证码。"}
            >
              <input
                id="mfa_code"
                name="mfa_code"
                className="input"
                type="text"
                autoComplete="one-time-code"
                autoFocus
                spellCheck={false}
                maxLength={64}
                value={code}
                onChange={(e) => setCode(e.target.value)}
              />
            </Field>
            <button className="btn btn--primary" type="submit" disabled={submitting || code.trim().length < 6}>
              {submitting ? "正在验证…" : "提交"}
            </button>
            <button
              className="btn"
              type="button"
              disabled={submitting}
              onClick={() => {
                setMfa(null);
                setCode("");
              }}
            >
              返回上一步
            </button>
          </form>
        ) : (
          <form onSubmit={submit} noValidate>
            <Field label="管理员邮箱" htmlFor="identifier">
              <input
                id="identifier"
                name="identifier"
                className="input"
                type="text"
                autoComplete="username"
                required
                value={identifier}
                onChange={(e) => setIdentifier(e.target.value)}
              />
            </Field>
            <Field label="密码" htmlFor="password">
              <input
                id="password"
                name="password"
                className="input"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </Field>
            <button className="btn btn--primary" type="submit" disabled={submitting}>
              {submitting ? "正在登录…" : "登录后台"}
            </button>
          </form>
        )}
        <div className="auth-card__foot">
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() =>
              dialog.notify("info", "后台访问边界", null, [
                { label: "会话", value: "独立 admin 受众，用户层会话无法访问后台 API" },
                { label: "两步验证", value: "启用 MFA 的后台账号登录时必须通过认证器验证码或恢复码" },
                { label: "案件正文", value: "后台默认不可读取；需要 evidence:sensitive:read 并留下敏感读取审计" },
                { label: "审计员", value: "只读审计、配置历史与导出校验" },
                { label: "用户入口", value: "/app-v2/login" },
              ])
            }
          >
            访问边界说明
          </button>
          <a href="/app-v2/">返回用户层系统</a>
        </div>
      </div>
    </div>
  );
}
