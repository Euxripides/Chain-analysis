import { useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { Field, useDialog } from "@chain/ui";
import { ApiError, useAuth } from "@chain/api-client";

/** §19 后台登录页。管理会话与用户会话分离（不同 Cookie、不同受众）。 */
export default function LoginPage() {
  const { login } = useAuth();
  const dialog = useDialog();
  const navigate = useNavigate();
  const location = useLocation();
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!identifier.trim() || !password) {
      dialog.notify("warning", "请填写完整", "账号与密码都不能为空。");
      return;
    }
    setSubmitting(true);
    try {
      const result = await login(identifier.trim(), password, "admin");
      const from = (location.state as { from?: string } | null)?.from;
      const target = from && from !== "/login" ? from.replace(/^\/admin/, "") || "/overview" : result.next || "/overview";
      navigate(target, { replace: true });
    } catch (err) {
      dialog.notify(
        "error",
        "登录未成功",
        err instanceof ApiError ? err.userMessage : "请稍后重试",
        err instanceof ApiError && err.traceId ? [{ label: "Trace ID", value: err.traceId }] : undefined,
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="auth-page">
      <div className="auth-card">
        <div className="auth-card__brand">
          <span className="shell__brand-mark" aria-hidden="true">
            AD
          </span>
          <div>
            <div className="auth-card__title">ChainSight 后台管理</div>
            <div className="auth-card__sub">管理员、安全管理员与审计员入口</div>
          </div>
        </div>
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
        <div className="auth-card__foot">
          <button
            type="button"
            className="btn btn--ghost btn--sm"
            onClick={() =>
              dialog.notify("info", "后台访问边界", null, [
                { label: "会话", value: "独立 admin 受众，用户层会话无法访问后台 API" },
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
