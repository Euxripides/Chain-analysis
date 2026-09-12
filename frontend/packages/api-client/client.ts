/**
 * 类型化 API SDK 的迁移起点（§6.1 / §28.2）。
 *
 * 它替代 frontend/src/api/client.ts 中"裸 fetch + 各页自己拼 URL"的做法：
 *   - 统一信封解包（success/data/meta/error），错误转成 ApiError 抛出；
 *   - 自动携带 CSRF 头（值来自可读的 CSRF Cookie，服务端做 double-submit 校验）；
 *   - 会话只存在于 HttpOnly Cookie，前端永远拿不到也不存储 Token；
 *   - 401 统一触发一次会话重取，失败后广播 onUnauthenticated。
 *
 * 旧 client 继续作为 compatibility adapter 存在，逐端点替换（§28.2）。
 */

export interface ApiMeta {
  request_id: string;
  trace_id: string;
  server_time: string;
  data_version?: string;
  next_cursor: string | null;
  total?: number;
}

export interface ApiErrorBody {
  code: string;
  message: string;
  field_errors: Array<{ field: string; code: string; message: string }>;
  retryable: boolean;
  details?: Record<string, unknown>;
}

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly fieldErrors: ApiErrorBody["field_errors"];
  readonly retryable: boolean;
  readonly details?: Record<string, unknown>;
  readonly traceId?: string;

  constructor(status: number, body: ApiErrorBody, traceId?: string) {
    super(body.message || `HTTP ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.code = body.code;
    this.fieldErrors = body.field_errors ?? [];
    this.retryable = Boolean(body.retryable);
    this.details = body.details;
    this.traceId = traceId;
  }

  /** 面向用户的中文提示（不包含后端实现细节）。 */
  get userMessage(): string {
    if (this.fieldErrors.length > 0) {
      return `${this.message}：${this.fieldErrors.map((f) => `${f.field} ${f.message}`).join("；")}`;
    }
    return this.message;
  }
}

export interface ApiClientOptions {
  baseUrl?: string;
  /** 用户层会话与后台会话使用不同的 Cookie，因此需要声明受众。 */
  audience: "user" | "admin";
  csrfCookieName?: string;
  onUnauthenticated?: () => void;
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  query?: Record<string, string | number | boolean | undefined | null>;
  body?: unknown;
  /** 幂等键（§21.2：创建/命令请求携带 Idempotency-Key）。 */
  idempotencyKey?: string;
  /** 版本化更新的 If-Match（§21.2）。 */
  ifMatch?: string;
  signal?: AbortSignal;
  /** 内部使用：401 后重试过一次，避免无限循环。 */
  retried?: boolean;
}

export class ApiClient {
  private readonly baseUrl: string;
  private readonly audience: "user" | "admin";
  private readonly csrfCookieName: string;
  private readonly onUnauthenticated?: () => void;

  constructor(options: ApiClientOptions) {
    this.baseUrl = (options.baseUrl ?? "").replace(/\/$/, "");
    this.audience = options.audience;
    this.csrfCookieName = options.csrfCookieName ?? `cs_${options.audience}_session_csrf`;
    this.onUnauthenticated = options.onUnauthenticated;
  }

  /** 读取可被 JS 访问的 CSRF Cookie（会话 Cookie 本身是 HttpOnly，读不到）。 */
  csrfToken(): string {
    const match = document.cookie.match(new RegExp(`(?:^|; )${escapeRegExp(this.csrfCookieName)}=([^;]*)`));
    return match ? decodeURIComponent(match[1]) : "";
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<{ data: T; meta: ApiMeta }> {
    const url = new URL(`${this.baseUrl}${path}`, window.location.origin);
    if (options.query) {
      for (const [key, value] of Object.entries(options.query)) {
        if (value === undefined || value === null || value === "") continue;
        url.searchParams.set(key, String(value));
      }
    }
    const headers: Record<string, string> = { Accept: "application/json" };
    if (options.body !== undefined) headers["Content-Type"] = "application/json; charset=utf-8";
    if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
    if (options.ifMatch) headers["If-Match"] = `"${options.ifMatch}"`;
    const method = options.method ?? "GET";
    if (method !== "GET") {
      const csrf = this.csrfToken();
      if (csrf) headers["X-CSRF-Token"] = csrf;
    }
    headers["X-Auth-Audience"] = this.audience;

    const response = await fetch(url.toString(), {
      method,
      headers,
      credentials: "same-origin",
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });

    const traceId = response.headers.get("X-Trace-Id") ?? undefined;

    if (response.status === 204) {
      return {
        data: undefined as T,
        meta: { request_id: "", trace_id: traceId ?? "", server_time: new Date().toISOString(), next_cursor: null },
      };
    }

    const text = await response.text();
    let payload: { success?: boolean; data?: T; meta?: ApiMeta; error?: ApiErrorBody } | null = null;
    try {
      payload = text ? JSON.parse(text) : null;
    } catch {
      // 网关/代理返回的非 JSON 响应：不把原始内容回显给用户，只保留状态码。
      throw new ApiError(response.status, {
        code: "INTERNAL_UNEXPECTED_RESPONSE",
        message: "服务返回了无法解析的响应",
        field_errors: [],
        retryable: response.status >= 500,
      }, traceId);
    }

    if (!response.ok || payload?.success === false) {
      const body: ApiErrorBody = payload?.error ?? {
        code: `HTTP_${response.status}`,
        message: defaultMessageFor(response.status),
        field_errors: [],
        retryable: response.status >= 500,
      };
      // 会话过期：一次静默重取，仍然失败则交给上层跳转登录。
      if (response.status === 401 && !options.retried && !path.startsWith("/api/auth/v1/")) {
        const refreshed = await fetch(`${this.baseUrl}/api/auth/v1/refresh`, {
          method: "POST",
          credentials: "same-origin",
          headers: { "X-CSRF-Token": this.csrfToken(), "X-Auth-Audience": this.audience },
        });
        if (refreshed.ok) {
          return this.request<T>(path, { ...options, retried: true });
        }
        this.onUnauthenticated?.();
      }
      throw new ApiError(response.status, body, traceId);
    }

    const meta: ApiMeta =
      payload?.meta ?? { request_id: "", trace_id: traceId ?? "", server_time: new Date().toISOString(), next_cursor: null };
    return { data: payload?.data as T, meta };
  }

  get<T>(path: string, options: Omit<RequestOptions, "method" | "body"> = {}) {
    return this.request<T>(path, { ...options, method: "GET" });
  }
  post<T>(path: string, body?: unknown, options: Omit<RequestOptions, "method" | "body"> = {}) {
    return this.request<T>(path, { ...options, method: "POST", body });
  }
  patch<T>(path: string, body?: unknown, options: Omit<RequestOptions, "method" | "body"> = {}) {
    return this.request<T>(path, { ...options, method: "PATCH", body });
  }
  delete<T>(path: string, options: Omit<RequestOptions, "method"> = {}) {
    return this.request<T>(path, { ...options, method: "DELETE" });
  }
}

function defaultMessageFor(status: number): string {
  switch (status) {
    case 400: return "请求参数不合法";
    case 401: return "登录状态已失效，请重新登录";
    case 403: return "当前账号无权执行此操作";
    case 404: return "对象不存在或无权查看";
    case 409: return "当前状态与该操作冲突";
    case 412: return "数据版本已变化，请刷新后重试";
    case 429: return "请求过于频繁，请稍后再试";
    case 503: return "依赖服务暂时不可用";
    default: return status >= 500 ? "服务内部错误" : "请求失败";
  }
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/** 生成幂等键，避免同一表单被重复提交（§21.2）。 */
export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) return crypto.randomUUID();
  return `idem-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}

// ---------- 领域类型（auth + 首页/后台最小集） ----------

export interface SessionUser {
  id: string;
  display_name: string;
  email: string;
}

export interface AuthSession {
  user: SessionUser;
  email: string;
  display_name: string;
  organization_id: string;
  workspace_id?: string;
  audience: "user" | "admin";
  session_id: string;
  roles: string[];
  permissions: string[];
  expires_at: string;
  absolute_expires_at: string;
  must_change_password: boolean;
  mfa_satisfied: boolean;
  csrf_token: string;
}

export interface LoginResult {
  state: string;
  user: SessionUser;
  roles: string[];
  audience: "user" | "admin";
  next: string;
  csrf_token: string;
  session_id: string;
  must_change_password: boolean;
}

export interface SessionRow {
  id: string;
  identity_id: string;
  organization_id: string;
  audience: string;
  auth_method: string;
  mfa_satisfied: boolean;
  device_id?: string;
  device?: Record<string, unknown>;
  user_agent: string;
  peer_ip?: string;
  client_ip?: string;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
  absolute_expires_at: string;
  revoked_at?: string;
  revoked_reason?: string;
  identity: { id: string; email: string; display_name: string; status: string };
  roles?: string[];
}

export interface LoginEventRow {
  id: string;
  seq: number;
  occurred_at: string;
  event_type: string;
  result: string;
  reason_code: string;
  risk_level: string;
  identity_id?: string;
  subject_masked: string;
  audience: string;
  auth_method: string;
  session_id?: string;
  device_id?: string;
  is_new_device: boolean;
  device_label: string;
  device_type: string;
  os_name: string;
  browser_name: string;
  peer_ip: string;
  client_ip: string;
  entry_host: string;
  request_id: string;
  detail: Record<string, unknown>;
  event_hash: string;
  prev_hash: string;
}

export interface AuditEventRow {
  id: string;
  seq: number;
  occurred_at: string;
  actor_id?: string;
  actor_email: string;
  actor_type: string;
  actor_roles: string[];
  organization_id?: string;
  workspace_id?: string;
  session_id?: string;
  action: string;
  category: string;
  resource_type: string;
  resource_id: string;
  result: string;
  http_status: number;
  http_method: string;
  route_template: string;
  request_id: string;
  trace_id: string;
  permission_key?: string;
  policy_decision: string;
  deny_reason: string;
  risk_level: string;
  sensitive: boolean;
  client_ip: string;
  peer_ip: string;
  user_agent: string;
  detail: Record<string, unknown>;
  changes?: Record<string, unknown>;
  prev_hash: string;
  event_hash: string;
}

export interface AdminIdentity {
  id: string;
  organization_id: string;
  email: string;
  display_name: string;
  status: string;
  is_platform_admin: boolean;
  mfa_required: boolean;
  must_change_password: boolean;
  failed_login_count: number;
  locked_until?: string;
  last_login_at?: string;
  created_at: string;
  updated_at: string;
  has_password: boolean;
  roles?: string[];
}

export interface RoleRow {
  id: string;
  key: string;
  name: string;
  description: string;
  is_system: boolean;
  organization_id: string | null;
  permission_count: number;
}

export interface PermissionRow {
  key: string;
  category: string;
  description: string;
  risk_level: string;
  sensitive: boolean;
}

export interface DashboardCard {
  key: string;
  label: string;
  value: number;
  unit: string;
}

export interface UserDashboard {
  as_of: string;
  scope: { organization_id: string; workspace_id: string };
  cards: DashboardCard[];
  quick_actions: Array<{ key: string; label: string; href: string; permission: string }>;
  data_source: string;
}

export interface AdminOverview {
  as_of: string;
  status: string;
  cards: DashboardCard[];
  my_roles: string[];
  data_source: Record<string, string>;
}

export interface ChainStatus {
  chain: string;
  last_seq: number;
  last_hash: string;
}

/** 已封存的完整性边界（Sprint 6 审计修复）。 */
export interface SealedBoundaryRef {
  incident_ref: string;
  chain: string;
  gap_from_seq: number;
  gap_to_seq: number;
  gap_event_count: number;
  trusted_from_seq: number;
  sealed_tail_hash: string;
  evidence_hash: string;
  sealed_at: string;
  statement: string;
}

/**
 * valid 只描述「实际被校验到的区间」是否自洽；
 * integrity_status / prefix_status 才表达链路是否完整，二者不可互相替代。
 */
export interface VerifyChainResult {
  chain: string;
  from_seq: number;
  to_seq: number;
  checked: number;
  valid: boolean;
  broken_at_seq: number | null;
  reason: string;
  head_hash: string;
  errors: string[];
  integrity_status: "intact" | "sealed_incident" | "undeclared_gap" | "boundary_conflict";
  prefix_status: "intact" | "sealed_unrecoverable" | "unsealed_gap";
  verified_from_seq: number;
  sealed_boundary?: SealedBoundaryRef;
  integrity_warnings: string[];
}
