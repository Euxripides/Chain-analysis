import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { Button, Field, PasswordInput, StatusBadge, useDialog } from "@chain/ui";
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

/**
 * §12.1 登录 `/login`
 *
 * 结构：左侧品牌与动态链路图（桌面显示，纯 CSS/SVG，**不显示虚构金额**），右侧登录区。
 *
 * 必须保留（一条都没删）：账号密码、本地账号/OIDC、工作区选择、MFA 第二步、
 * 恢复码、超时、必须修改初始密码、return-to。
 *
 * 提示口径：说明 / 成功 / 警告 / 错误 / 确认全部走 `useDialog()`。
 * 蓝图 §12.1 明确"登录失败不抖动整个页面，只轻微强调相关字段并打开错误 Dialog"，
 * 因此本页**不再使用 TopToast**（TopToast 仍作为兼容层保留在 @chain/ui 里，
 * 其他调用方不受影响）。这是一处可见的行为变化，已在实施记录中逐项列出。
 *
 * 第二步的挑战令牌与用户输入的验证码只存在于内存与请求体里：
 * 不写入 URL、localStorage、sessionStorage 或任何日志。
 */

/** 第二步的本地状态：只保留渲染与倒计时需要的字段，验证码本身不进入这里。 */
interface MfaStepState {
  /** 一次性挑战令牌；提交给 /mfa/verify 后即作废。 */
  challengeId: string;
  /** 本地 deadline（毫秒）；由 expires_in_seconds 优先换算。 */
  deadline: number;
  /** 服务端允许的第二步方式（totp / recovery_code）。 */
  methods: string[];
  /** 成功后要跳转的应用内路径（§21.4 相对路径）。 */
  next: string;
  /** 挑战绑定的账号，用于第二步视图展示"正在为谁验证"。 */
  email: string;
}

function formatCountdown(seconds: number): string {
  const mm = String(Math.floor(seconds / 60)).padStart(2, "0");
  const ss = String(seconds % 60).padStart(2, "0");
  return mm + ":" + ss;
}

/** 6 位纯数字才走"格子"视觉；恢复码等其它形式回落成普通文本框。 */
function looksLikeTotp(value: string): boolean {
  return /^\d{0,6}$/.test(value);
}

export default function LoginPage() {
  const { login, verifyMfa } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const location = useLocation();
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);
  const [mfa, setMfa] = useState<MfaStepState | null>(null);
  const [code, setCode] = useState("");
  const [secondsLeft, setSecondsLeft] = useState(0);
  /** 登录失败后轻微强调凭据字段，而不是抖动整页（蓝图 §12.1）。 */
  const [credentialInvalid, setCredentialInvalid] = useState(false);
  const [params] = useSearchParams();

  useEffect(() => {
    let alive = true;
    void fetch("/api/auth/v1/methods", { credentials: "same-origin" })
      .then((r) => (r.ok ? r.json() : null))
      .then((body) => {
        if (alive) setOidcEnabled(Boolean(body?.data?.oidc));
      })
      .catch(() => undefined);
    const oidcError = params.get("oidc_error");
    if (oidcError) {
      dialog.notify("error", "组织账号登录未完成", null, [{ label: "原因码", value: oidcError }]);
    }
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  /**
   * 第二步的剩余有效时间倒计时。
   *
   * 服务端才是唯一裁判（过期会返回 AUTHN_MFA_CHALLENGE_INVALID），
   * 这里的倒计时只是把"还剩多久"提前告诉用户；归零即回到第一步，
   * 避免用户在已经作废的挑战上反复输入。
   */
  useEffect(() => {
    if (!mfa) return;
    const tick = () => {
      const left = mfaSecondsLeft(mfa.deadline);
      setSecondsLeft(left);
      if (left <= 0) {
        setMfa(null);
        setCode("");
        dialog.notify("warning", "本次验证已超时", "第二步验证码的有效时间已结束，请重新输入账号与密码登录。");
      }
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => window.clearInterval(timer);
    // 只依赖挑战本身：dialog 是稳定引用，tick 每次渲染重建不影响正确性。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mfa]);

  /**
   * 多工作区身份的选择器（P1-20260913）。
   *
   * 服务端在"该身份在该组织内有多个有效工作区成员资格"时返回 409
   * AUTHN_WORKSPACE_SELECTION_REQUIRED 并附上可选列表；服务端**绝不**替用户挑默认工作区，
   * 前端同样不得静默选择。因此这里把选择放进弹窗（仓库规范：一切提示只用弹窗），
   * 用户选定后带 workspace_id 重新登录，由服务端按成员资格绑定会话。
   */
  const chooseWorkspace = async (options: WorkspaceOption[]): Promise<string | null> => {
    const picked = await dialog.confirm({
      kind: "info",
      title: "请选择工作区",
      body: "该账号属于多个工作区。出于安全考虑，系统不会替你选择默认工作区：请选择本次登录要进入的工作区。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        ...options.map((option) => ({
          label: option.name ? option.name + "（" + option.slug + "）" : option.slug,
          value: option.id,
          result: option.id,
          variant: "primary" as const,
        })),
      ],
    });
    return picked && picked !== "" ? picked : null;
  };

  /** 第一步与第二步共用的收尾：第二步成功后服务端响应与普通登录完全同形。 */
  const finishLogin = (result: LoginResult) => {
    const from = (location.state as { from?: string } | null)?.from;
    if (result.must_change_password) {
      dialog.notify("warning", "需要修改初始密码", "该账号仍在使用初始密码，请尽快在「个人资料与安全」中修改。");
    }
    const target = from && from !== "/login" ? from.replace(/^\/app-v2/, "") || "/home" : result.next || "/home";
    navigate(target, { replace: true });
  };

  /** 进入第二步：此时服务端没有下发任何 Cookie，本次登录尚未完成。 */
  const enterMfaStep = (challenge: MfaChallenge) => {
    const deadline = mfaChallengeDeadline(challenge);
    setMfa({
      challengeId: challenge.challenge_id,
      deadline,
      methods: challenge.methods ?? [],
      next: challenge.next || "/home",
      email: challenge.user?.email || identifier.trim(),
    });
    setCode("");
    setCredentialInvalid(false);
    setSecondsLeft(mfaSecondsLeft(deadline));
  };

  /** 退回第一步：清空挑战与验证码，并说明原因（提示只走弹窗）。 */
  const backToPasswordStep = (title: string, body: string, details?: Array<{ label: string; value: React.ReactNode }>) => {
    setMfa(null);
    setCode("");
    dialog.notify("warning", title, body, details);
  };

  /** 登录失败：强调字段 + 错误 Dialog，不做整页抖动。 */
  const reportLoginFailure = (title: string, message: string, details?: Array<{ label: string; value: React.ReactNode }>) => {
    setCredentialInvalid(true);
    dialog.notify("error", title, message, details);
  };

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!identifier.trim() || !password) {
      dialog.notify("warning", "请填写完整", "账号与密码都不能为空。");
      return;
    }
    setSubmitting(true);
    setCredentialInvalid(false);
    try {
      let workspaceId: string | undefined;
      for (let attempt = 0; attempt < 2; attempt += 1) {
        try {
          const result = await login(identifier.trim(), password, "user", workspaceId);
          // 202 / state=mfa_required：该账号已启用 MFA，本次登录还没完成，
          // 不能跳转、也不能当成已登录（此时没有会话）。
          if (isMfaChallenge(result)) {
            enterMfaStep(result);
            return;
          }
          finishLogin(result);
          return;
        } catch (err) {
          const options = workspaceSelectionOptions(err);
          if (options === null || attempt > 0) {
            throw err;
          }
          if (options.length === 0) {
            // 服务端要求选择却没有给出任何可选项：不得自行编造工作区，明确失败。
            reportLoginFailure("无法选择工作区", "该账号在当前组织内没有可用的工作区成员资格。");
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
      if (err instanceof ApiError) {
        reportLoginFailure(
          "登录失败",
          err.userMessage,
          err.traceId ? [{ label: "Trace", value: err.traceId }] : undefined,
        );
      } else {
        reportLoginFailure("登录失败", "登录请求未能完成，请检查网络后重试。");
      }
    } finally {
      setSubmitting(false);
    }
  };

  /**
   * 第二步提交。TOTP 与恢复码共用同一个输入框：
   * 长度大于 6 时按恢复码提交，其余交给服务端判定（前端不猜格式）。
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
      // 验证码不留在组件状态里；成功后会话由 AuthProvider 写入。
      setCode("");
      setMfa(null);
      finishLogin(result);
    } catch (err) {
      setCode("");
      if (!(err instanceof ApiError)) {
        dialog.notify("error", "第二步验证未通过", "验证请求未能完成，请检查网络后重试。");
        return;
      }
      if (err.code === MFA_ERROR_CODES.invalid) {
        // 码错：留在第二步重试。
        dialog.notify(
          "error",
          "验证码不正确",
          "请确认认证器与本机时间同步；恢复码每个只能使用一次。",
          err.traceId ? [{ label: "Trace", value: err.traceId }] : undefined,
        );
      } else if (err.code === MFA_ERROR_CODES.challengeInvalid) {
        // 挑战过期/已使用/不存在：必须重新走第一步。
        backToPasswordStep("本次验证已失效", "第二步验证已过期或已被使用，请重新输入账号与密码。");
      } else if (err.code === MFA_ERROR_CODES.attemptsExhausted) {
        backToPasswordStep("尝试次数已用尽", "为避免暴力猜测，该账号短时间内不能再提交验证码。", [
          { label: "可重试时间", value: err.retryAfterSeconds === undefined ? "请稍后重试" : err.retryAfterSeconds + " 秒后" },
          { label: "下一步", value: "重新输入账号与密码" },
        ]);
      } else {
        dialog.notify("error", "第二步验证失败", err.userMessage, err.traceId ? [{ label: "Trace", value: err.traceId }] : undefined);
      }
    } finally {
      setSubmitting(false);
    }
  };

  const supportsRecoveryCode = Boolean(mfa?.methods.includes("recovery_code"));
  const totpMode = looksLikeTotp(code);

  return (
    <div className="auth-page">
      {/* 品牌面板：抽象地址节点与资金路径，纯 SVG + CSS（零外部资源、零虚构金额）。
          <980px 由 CSS 隐藏，移动端只保留小型品牌标识与表单（蓝图 §12.1）。 */}
      <aside className="auth-aside">
        <div className="auth-aside__top">
          <span className="auth-aside__mark">
            <span className="cs-brand__mark" aria-hidden="true">CS</span>
            <span>
              <span className="auth-aside__name">ChainSight</span>
              <span className="auth-aside__tagline">链上调查工作台</span>
            </span>
          </span>
        </div>
        <div className="auth-aside__body">
          <h2 className="auth-aside__title">
            把链上资金流
            <br />
            还原成可呈堂的证据链
          </h2>
          <p className="auth-aside__desc">地址画像、资金流向、交易证据与审计轨迹，收敛在同一个工作台里。</p>
          <FundPathCanvas />
        </div>
        <div className="auth-aside__foot">ChainSight · 内部部署环境</div>
      </aside>

      <div className="auth-card">
        <div className="auth-card__brand">
          <span className="cs-brand__mark" aria-hidden="true">
            CS
          </span>
          <div>
            <div className="auth-card__title">ChainSight 调查工作台</div>
            <div className="auth-card__sub">{mfa ? "两步验证 · 第二步" : "用户层系统"}</div>
          </div>
        </div>

        {mfa ? (
          <form onSubmit={submitMfa} noValidate>
            <div className="toolbar" style={{ justifyContent: "space-between" }}>
              <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
                {mfa.email}
              </span>
              <StatusBadge tone={secondsLeft <= 60 ? "warn" : "info"}>剩余 {formatCountdown(secondsLeft)}</StatusBadge>
            </div>

            <Field
              label="认证器验证码"
              htmlFor="mfa_code"
              hint={supportsRecoveryCode ? "6 位数字；认证器不可用时，也可以输入一个恢复码。" : "认证器上显示的 6 位数字验证码。"}
            >
              {/*
                6 格视觉输入，但**只有一个真实 input**（蓝图 §12.1）：
                格子只是 aria-hidden 的呈现层，值的唯一来源仍是 #mfa_code。
                输入出现非数字/超长（恢复码）时自动回落成普通文本框，不限制格式。
              */}
              <div className="cs-otp" data-mode={totpMode ? "cells" : "text"}>
                <div className="cs-otp__cells" aria-hidden="true">
                  {Array.from({ length: 6 }).map((_, index) => (
                    <span className="cs-otp__cell" key={index} data-filled={code[index] ? "true" : "false"}>
                      {code[index] ?? ""}
                    </span>
                  ))}
                </div>
                <input
                  id="mfa_code"
                  name="mfa_code"
                  className="input cs-otp__input"
                  type="text"
                  autoComplete="one-time-code"
                  autoFocus
                  spellCheck={false}
                  maxLength={64}
                  aria-label="认证器验证码"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                />
              </div>
            </Field>

            <Button type="submit" variant="primary" busy={submitting} busyLabel="正在验证…" disabled={code.trim().length < 6}>
              提交
            </Button>
            <Button
              type="button"
              disabled={submitting}
              onClick={() => {
                // 返回上一步：丢弃本地挑战（服务端的一次性令牌随后自然过期）。
                setMfa(null);
                setCode("");
              }}
            >
              返回上一步
            </Button>
          </form>
        ) : (
          <form onSubmit={submit} noValidate>
            <Field label="账号邮箱" htmlFor="identifier" error={credentialInvalid ? "账号或密码不正确。" : undefined}>
              <input
                id="identifier"
                name="identifier"
                className="input"
                type="text"
                autoComplete="username"
                inputMode="email"
                required
                aria-invalid={credentialInvalid || undefined}
                value={identifier}
                onChange={(e) => {
                  setIdentifier(e.target.value);
                  setCredentialInvalid(false);
                }}
              />
            </Field>
            <Field label="密码" htmlFor="password" error={credentialInvalid ? "账号或密码不正确。" : undefined}>
              <PasswordInput
                id="password"
                label="密码"
                value={password}
                onChange={(next) => {
                  setPassword(next);
                  setCredentialInvalid(false);
                }}
                /* Enter 提交当前步骤由表单原生行为承担：输入框在 <form> 内，
                   且存在 type="submit" 按钮，浏览器回车即走与点击完全相同的路径。 */
                required
              />
            </Field>
            <Button type="submit" variant="primary" busy={submitting} busyLabel="正在登录…">
              登录
            </Button>
            {oidcEnabled ? (
              <Button
                type="button"
                variant="primary"
                onClick={() => {
                  window.location.href = "/api/auth/v1/oidc/start?audience=user&return_to=" + encodeURIComponent("/home");
                }}
              >
                使用组织账号登录
              </Button>
            ) : null}
          </form>
        )}
      </div>
    </div>
  );
}

/**
 * 品牌区的抽象资金路径。
 *
 * 只画**结构**（地址节点、方向、路径流动），不标注任何金额或余额——
 * 蓝图 §12.1 明确"不显示虚构金额"。纯 SVG + CSS，无外部资源、无网络请求。
 * reduced-motion 下动画由 CSS 关闭。
 */
function FundPathCanvas() {
  return (
    <svg className="auth-flow" viewBox="0 0 420 200" role="img" aria-label="抽象的资金路径示意：地址节点之间的方向与汇聚关系">
      <defs>
        <linearGradient id="auth-flow-line" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0%" stopColor="#4353E6" stopOpacity="0.15" />
          <stop offset="55%" stopColor="#0EA5B7" stopOpacity="0.85" />
          <stop offset="100%" stopColor="#0EA5B7" stopOpacity="0.2" />
        </linearGradient>
      </defs>
      <path className="auth-flow__edge" d="M40 40 C 140 40, 160 100, 262 100" />
      <path className="auth-flow__edge" d="M40 160 C 140 160, 160 100, 262 100" />
      <path className="auth-flow__edge" d="M262 100 C 330 100, 340 56, 384 56" />
      <path className="auth-flow__edge" d="M262 100 C 330 100, 340 150, 384 150" />
      <g className="auth-flow__node" transform="translate(40 40)">
        <circle r="7" />
      </g>
      <g className="auth-flow__node" transform="translate(40 160)">
        <circle r="7" />
      </g>
      <g className="auth-flow__node auth-flow__node--hub" transform="translate(262 100)">
        <rect x="-8" y="-8" width="16" height="16" rx="3" />
      </g>
      <g className="auth-flow__node" transform="translate(384 56)">
        <polygon points="0,-9 9,0 0,9 -9,0" />
      </g>
      <g className="auth-flow__node" transform="translate(384 150)">
        <polygon points="0,-9 9,0 0,9 -9,0" />
      </g>
    </svg>
  );
}
