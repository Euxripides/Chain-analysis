/**
 * 后台登录 `return_to` 的本地白名单清洗（R-3）。
 *
 * 背景：旧 V1 SPA 的后台页面（`/api-control`、`/system`）与后台管理系统
 * （Admin Console，挂载在 `/admin/`）是**两个 SPA**。旧页面需要把用户送到
 * `/admin/login?return_to=<原页面>`，登录成功后再用**真实浏览器跳转**回到原页面
 * （不能走 admin 的 basename 内部 navigate，否则会得到 `/admin/api-control` 这种不存在的路由）。
 *
 * 该参数是**开放重定向**的经典入口，因此这里采用"本地白名单"而不是"黑名单过滤"：
 *   - 只接受 `/api-control`、`/system` 两个后台页面及其合法子路由（`/api-control/...`）；
 *   - query 原样保留；
 *   - 其余一律返回 `null`（调用方回退到默认落地页）。
 *
 * 与服务端 `backend/internal/api/authv1.go:sanitizeReturnToDefault` 同一安全取向
 * （只允许站内相对路径、拒绝 `//`、反斜线、`://`），但更严格——只放行命名白名单。
 */

/** 允许作为 return_to 的后台页面前缀。 */
export const RETURN_TO_ALLOWLIST = ["/api-control", "/system"] as const;

/** 被拒绝时的原因（用于诊断与验收证据，不影响返回结果）。 */
export type ReturnToRejection =
  | "empty"
  | "not-absolute"
  | "protocol-relative"
  | "backslash"
  | "scheme"
  | "control-char"
  | "encoded-trick"
  | "cross-origin"
  | "userinfo"
  | "parent-escape"
  | "not-allowlisted"
  | "unparseable";

export type ReturnToResult =
  | { ok: true; target: string }
  | { ok: false; reason: ReturnToRejection; raw: string };

function matchesAllowlist(pathname: string): boolean {
  return RETURN_TO_ALLOWLIST.some(
    (base) => pathname === base || pathname.startsWith(base + "/"),
  );
}

/** 纯函数校验：返回可安全跳转的 `pathname + search`，否则给出拒绝原因。 */
export function inspectReturnTo(raw: string | null | undefined): ReturnToResult {
  const value = (raw ?? "").trim();
  if (!value) return { ok: false, reason: "empty", raw: value };
  // 任何控制字符（含 NUL / CR / LF）都不接受。
  if (/[\u0000-\u001f\u007f]/.test(value)) return { ok: false, reason: "control-char", raw: value };
  // javascript: / data: / https: 等带 scheme 的形式。
  if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(value)) return { ok: false, reason: "scheme", raw: value };
  if (!value.startsWith("/")) return { ok: false, reason: "not-absolute", raw: value };
  if (value.startsWith("//")) return { ok: false, reason: "protocol-relative", raw: value };
  if (value.includes("\\")) return { ok: false, reason: "backslash", raw: value };
  if (value.startsWith("/\\")) return { ok: false, reason: "backslash", raw: value };
  // 解码一次后再查一次，阻断 %2f%2f、%5c、%2e%2e 之类的编码绕过。
  let decoded = value;
  try {
    decoded = decodeURIComponent(value);
  } catch {
    return { ok: false, reason: "encoded-trick", raw: value };
  }
  if (decoded !== value) {
    if (decoded.startsWith("//") || decoded.includes("\\") || !decoded.startsWith("/")) {
      return { ok: false, reason: "encoded-trick", raw: value };
    }
  }

  let url: URL;
  try {
    url = new URL(value, window.location.origin);
  } catch {
    return { ok: false, reason: "unparseable", raw: value };
  }
  // URL 解析已经折叠了 `.`/`..`；不同源、带凭据、或折叠后逃出白名单一律拒绝。
  if (url.origin !== window.location.origin) return { ok: false, reason: "cross-origin", raw: value };
  if (url.username || url.password) return { ok: false, reason: "userinfo", raw: value };
  const segments = url.pathname.split("/");
  if (segments.includes("..")) return { ok: false, reason: "parent-escape", raw: value };
  if (/\/{2,}/.test(url.pathname)) return { ok: false, reason: "parent-escape", raw: value };
  if (!matchesAllowlist(url.pathname)) return { ok: false, reason: "not-allowlisted", raw: value };
  return { ok: true, target: url.pathname + url.search };
}

/** 便捷封装：合法则返回目标路径，否则 `null`。 */
export function sanitizeReturnTo(raw: string | null | undefined): string | null {
  const result = inspectReturnTo(raw);
  return result.ok ? result.target : null;
}
