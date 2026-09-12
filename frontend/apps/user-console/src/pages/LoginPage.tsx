import { useEffect, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { Field, useDialog } from "@chain/ui";
import { ApiError, useAuth } from "@chain/api-client";

/** §18.2 U-AUTH-01 登录页。错误与安全说明全部通过 Dialog 呈现。 */
export default function LoginPage() {
  const { login } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const location = useLocation();
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);
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

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!identifier.trim() || !password) {
      dialog.notify("warning", "请填写完整", "账号与密码都不能为空。");
      return;
    }
    setSubmitting(true);
    try {
      const result = await login(identifier.trim(), password, "user");
      const from = (location.state as { from?: string } | null)?.from;
      if (result.must_change_password) {
        dialog.notify("warning", "需要修改初始密码", "该账号仍在使用初始化密码，请尽快在「个人资料与安全」中修改。");
      }
      const target = from && from !== "/login" ? from.replace(/^\/app-v2/, "") || "/home" : result.next || "/home";
      navigate(target, { replace: true });
    } catch (err) {
      const message = err instanceof ApiError ? err.userMessage : "登录失败，请稍后重试";
      const details = err instanceof ApiError && err.traceId ? [{ label: "Trace ID", value: err.traceId }] : undefined;
      dialog.notify("error", "登录未成功", message, details);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="auth-card">
        <div className="auth-card__brand">
          <span className="shell__brand-mark" aria-hidden="true">
            CS
          </span>
          <div>
            <div className="auth-card__title">ChainSight 调查工作台</div>
            <div className="auth-card__sub">用户层系统 · 双重系统第 2 版</div>
          </div>
        </div>
        <form onSubmit={submit} noValidate>
          <Field label="账号邮箱" htmlFor="identifier">
            <input
              id="identifier"
              name="identifier"
              className="input"
              type="text"
              autoComplete="username"
              inputMode="email"
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
            {submitting ? "正在登录…" : "登录"}
          </button>
          {oidcEnabled ? (
            <button
              className="btn btn--primary"
              type="button"
              onClick={() => {
                window.location.href = `/api/auth/v1/oidc/start?audience=user&return_to=${encodeURIComponent("/home")}`;
              }}
            >
              使用组织账号登录
            </button>
          ) : null}
        </form>
        <div className="auth-card__foot">
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() =>
              dialog.notify("info", "登录说明", null, [
                { label: "认证方式", value: "本地账户（密码使用 Argon2id 哈希）" },
                { label: "会话", value: "HttpOnly Cookie，前端不可读取 Token" },
                { label: "审计", value: "每次登录尝试（含失败）都会记录" },
                { label: "后台入口", value: "/admin/overview（需要管理会话）" },
              ])
            }
          >
            登录说明
          </button>
          <a href="/">返回旧工作台</a>
        </div>
      </div>
    </div>
  );
}
