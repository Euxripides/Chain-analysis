import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import {
  ApiClient,
  ApiError,
  isMfaChallenge,
  type AuthSession,
  type LoginResponse,
  type LoginResult,
  type MfaChallenge,
  type MfaDisableResult,
  type MfaEnrollConfirm,
  type MfaEnrollStart,
  type MfaStepUpResult,
} from "./client";

/**
 * 会话上下文：会话状态只有一个事实来源（服务端 /api/auth/v1/session）。
 * 前端不缓存 Token，也不从 localStorage 恢复身份（§28.4 验收"前端无 JS 可读 Token"）。
 */

/** 服务端在"多工作区身份未显式选择"时返回的错误码（§21.3 契约）。 */
export const WORKSPACE_SELECTION_CODE = "AUTHN_WORKSPACE_SELECTION_REQUIRED";

/** iam.WorkspaceOption 的前端形态：该身份在该组织内**有效成员资格**对应的工作区。 */
export interface WorkspaceOption {
  id: string;
  slug: string;
  name: string;
}

/**
 * 从登录失败的 ApiError 中取出可选工作区列表（P1-20260913 多成员选择器契约）。
 *
 * 返回 null 表示"这不是工作区选择错误"，调用方按普通登录失败处理；
 * 返回数组（可能为空）表示服务端确实要求显式选择，数组为空时不得静默替用户挑一个。
 */
export function workspaceSelectionOptions(err: unknown): WorkspaceOption[] | null {
  if (!(err instanceof ApiError) || err.code !== WORKSPACE_SELECTION_CODE) {
    return null;
  }
  const raw = err.details?.workspaces;
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw
    .filter((item): item is Record<string, unknown> => Boolean(item) && typeof item === "object")
    .map((item) => ({
      id: String(item.id ?? ""),
      slug: String(item.slug ?? ""),
      name: String(item.name ?? ""),
    }))
    .filter((option) => option.id !== "");
}

/**
 * 把挑战的剩余有效时间换算成本地 deadline（毫秒时间戳）。
 *
 * 优先使用 expires_in_seconds（相对时长，不受本机与服务器的时钟偏差影响），
 * 缺失或非法时才退回 expires_at；两者都拿不到就返回 0，
 * 调用方据此立即判定"已过期"，绝不放行一个无法证明有效的挑战。
 */
export function mfaChallengeDeadline(challenge: MfaChallenge): number {
  const seconds = Number(challenge.expires_in_seconds);
  if (Number.isFinite(seconds) && seconds > 0) return Date.now() + seconds * 1000;
  const at = Date.parse(challenge.expires_at ?? "");
  if (!Number.isNaN(at)) return at;
  return 0;
}

/** 挑战剩余秒数（向下取整，最小 0）。 */
export function mfaSecondsLeft(deadline: number): number {
  return Math.max(0, Math.ceil((deadline - Date.now()) / 1000));
}

/**
 * 把服务端返回的字符串集合归一化为数组。
 *
 * 服务端在该身份**没有任何角色绑定**时会把 roles 序列化成 JSON null（permissions 同理）。
 * null 一旦进入渲染层，任何 roles.join(...) / permissions.includes(...) 都会在渲染期抛
 * TypeError: Cannot read properties of null，React 卸载整棵树，页面只剩空白的 #root。
 * 这里同时丢掉非字符串项，避免 [null] 之类的脏数据继续下沉到渲染层。
 */
export function normalizeStringList(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string");
}

/**
 * 会话归一化：**所有**后端响应写进会话状态前都必须经过这里。
 *
 * reload() 是会话落地的唯一入口（服务端 /session 是唯一事实来源），登录第一步、MFA 第二步
 * 之后的重载、工作区切换、窗口重新聚焦时的轮询全部复用同一条路径，因此 roles/permissions
 * 不可能再以 null 形态进入 AuthContext。
 */
export function normalizeSession(session: AuthSession): AuthSession {
  return {
    ...session,
    roles: normalizeStringList(session?.roles),
    permissions: normalizeStringList(session?.permissions),
  };
}

/** 登录/第二步响应里的 roles 同样是后端集合字段，交给调用方之前先归一化。 */
export function normalizeLoginResult(result: LoginResult): LoginResult {
  return { ...result, roles: normalizeStringList(result?.roles) };
}

/** 登录/第二步成功但会话读不回来时的统一错误：明确失败，绝不假装已登录。 */
function sessionUnavailableError(): ApiError {
  return new ApiError(500, {
    code: "AUTHN_SESSION_UNAVAILABLE",
    message: "登录成功但无法建立会话，请检查 Cookie 是否被浏览器拦截",
    field_errors: [],
    retryable: true,
  });
}

export interface AuthContextValue {
  session: AuthSession | null;
  loading: boolean;
  client: ApiClient;
  /**
   * 第一步：账号密码。
   *
   * 返回值有两种形态——已建立会话的 LoginResult，或要求第二步的 MfaChallenge
   * （HTTP 202，此时**没有任何 Cookie**）。调用方必须用 isMfaChallenge 分支，
   * 不能直接跳转。
   */
  login: (
    identifier: string,
    password: string,
    audience: "user" | "admin",
    workspaceId?: string,
  ) => Promise<LoginResponse>;
  /**
   * 第二步：提交 TOTP 或恢复码。成功后与普通登录完全同形（会话已建立）。
   * 验证码只作为请求体提交，不写入 URL、日志或任何持久化状态。
   */
  verifyMfa: (challengeId: string, code: string) => Promise<LoginResult>;
  /** 已登录会话的 step-up：POST /mfa/verify（带会话、不带 challenge_id）。 */
  mfaStepUp: (code: string) => Promise<MfaStepUpResult>;
  /** 开始 MFA 自助注册：返回密钥与 otpauth URI（确认前不改变登录要求）。 */
  mfaEnrollStart: () => Promise<MfaEnrollStart>;
  /** 用一次正确验证码确认注册；恢复码明文只在此响应里出现一次。 */
  mfaEnrollConfirm: (code: string) => Promise<MfaEnrollConfirm>;
  /** 关闭 MFA（删除密钥与全部恢复码）。 */
  mfaDisable: () => Promise<MfaDisableResult>;
  /** 登录后切换会话绑定的工作区：服务端按成员资格判定，且绝不回退组织默认工作区。 */
  workspaceOptions: () => Promise<WorkspaceOption[]>;
  switchWorkspace: (workspaceId: string) => Promise<AuthSession>;
  logout: () => Promise<void>;
  reload: () => Promise<AuthSession | null>;
  can: (permission: string) => boolean;
  canAny: (...permissions: string[]) => boolean;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth 必须在 <AuthProvider> 内使用");
  return ctx;
}

export interface AuthProviderProps {
  audience: "user" | "admin";
  /** 未登录时的落地页（通常为 /login）。 */
  loginPath?: string;
  onSessionExpired?: () => void;
  children: React.ReactNode;
}

export function AuthProvider({ audience, onSessionExpired, children }: AuthProviderProps) {
  const [session, setSession] = useState<AuthSession | null>(null);
  const [loading, setLoading] = useState(true);

  const client = useMemo(
    () =>
      new ApiClient({
        audience,
        onUnauthenticated: () => {
          setSession(null);
          onSessionExpired?.();
        },
      }),
    [audience, onSessionExpired],
  );

  const reload = useCallback(async (): Promise<AuthSession | null> => {
    try {
      const { data } = await client.get<AuthSession>("/api/auth/v1/session");
      // 会话落地的唯一写入点：roles/permissions 可能是 JSON null（无角色绑定），
      // 必须先归一化成数组再进状态，否则渲染期 roles.join(...) 会把整棵树打崩。
      if (!data) {
        setSession(null);
        return null;
      }
      const normalized = normalizeSession(data);
      // 服务端已经按 X-Auth-Audience 收窄受众；这里再做一次客户端断言，
      // 防止任何回退逻辑让用户层会话被后台应用误认为已登录（§3.3）。
      if (normalized.audience !== audience) {
        setSession(null);
        return null;
      }
      setSession(normalized);
      return normalized;
    } catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
        setSession(null);
        return null;
      }
      throw err;
    } finally {
      setLoading(false);
    }
  }, [client]);

  useEffect(() => {
    void reload();
  }, [reload]);

  /**
   * 多标签页 / 多设备收敛（P1-20260913-MULTITAB）。
   *
   * 工作区切换改变的是**服务端会话**，同一浏览器的其它标签页不会自动知道；
   * 只靠挂载时读一次会话，第二个标签页会一直显示旧工作区的数据。
   * 这里在窗口重新获得焦点时（以及每 30s）重新读取 /session：
   * workspace_id 一旦变化，scopeKey 随之变化 -> 整棵子树重建 + 缓存清空，
   * 因此第二个标签页不会同时呈现两个工作区的数据。
   * 会话未变化时不触发任何重建，正常操作不受影响。
   */
  useEffect(() => {
    const onFocus = () => {
      void reload();
    };
    window.addEventListener("focus", onFocus);
    const timer = window.setInterval(onFocus, 30_000);
    return () => {
      window.removeEventListener("focus", onFocus);
      window.clearInterval(timer);
    };
  }, [reload]);

  const login = useCallback(
    async (identifier: string, password: string, target: "user" | "admin", workspaceId?: string) => {
      // workspace_id 只在调用方显式选择时携带：服务端绝不替用户任取默认工作区，
      // 前端也不得自行挑一个（P1-20260913）。
      const chosen = workspaceId?.trim();
      const { data } = await client.post<LoginResponse>("/api/auth/v1/login", {
        identifier,
        password,
        audience: target,
        ...(chosen ? { workspace_id: chosen } : {}),
        // next 是应用内相对路径（§21.4），前端在 basename 之下直接使用。
        return_to: target === "admin" ? "/overview" : "/home",
      });
      /**
       * 第二步场景（HTTP 202 / state=mfa_required）：服务端**没有**下发任何 Cookie，
       * 此时读 /session 只会拿到 401，然后被误判成"登录成功但会话不可用"。
       * 因此这里必须直接把挑战交给调用方，由登录页渲染第二步表单。
       */
      if (isMfaChallenge(data)) {
        return data;
      }
      const next = await reload();
      if (!next) {
        // 登录成功但会话读取失败：明确失败，绝不假装已登录。
        throw sessionUnavailableError();
      }
      // roles 可能为 null：调用方拿到的登录结果同样必须已是数组。
      return normalizeLoginResult(data);
    },
    [client, reload],
  );

  /**
   * 登录第二步：提交 TOTP 或恢复码。
   *
   * 成功后服务端已建立会话（与第一步成功同形），因此这里复用同一套
   * "写状态 = reload() 读服务端会话"的路径；失败原样抛出 ApiError，
   * 由调用方按 error.code 决定是留在第二步还是退回第一步。
   */
  const verifyMfa = useCallback(
    async (challengeId: string, code: string): Promise<LoginResult> => {
      const { data } = await client.post<LoginResult>("/api/auth/v1/mfa/verify", {
        challenge_id: challengeId,
        code,
      });
      const next = await reload();
      if (!next) {
        throw sessionUnavailableError();
      }
      // 第二步成功后的会话已由 reload() 归一化；响应体同样归一化后再返回。
      return normalizeLoginResult(data);
    },
    [client, reload],
  );

  /** step-up：在当前会话上校验一次验证码（不建立新会话、不改 challenge 状态）。 */
  const mfaStepUp = useCallback(
    async (code: string): Promise<MfaStepUpResult> => {
      const { data } = await client.post<MfaStepUpResult>("/api/auth/v1/mfa/verify", { code });
      return data;
    },
    [client],
  );

  const mfaEnrollStart = useCallback(async (): Promise<MfaEnrollStart> => {
    const { data } = await client.post<MfaEnrollStart>("/api/auth/v1/mfa/enroll/start", {});
    return data;
  }, [client]);

  const mfaEnrollConfirm = useCallback(
    async (code: string): Promise<MfaEnrollConfirm> => {
      const { data } = await client.post<MfaEnrollConfirm>("/api/auth/v1/mfa/enroll/confirm", { code });
      return data;
    },
    [client],
  );

  const mfaDisable = useCallback(async (): Promise<MfaDisableResult> => {
    const { data } = await client.post<MfaDisableResult>("/api/auth/v1/mfa/disable", {});
    return data;
  }, [client]);

  /** 列出当前身份在当前组织内的有效成员资格工作区（与登录选择器同源）。 */
  const workspaceOptions = useCallback(async (): Promise<WorkspaceOption[]> => {
    const { data } = await client.get<{ workspaces?: WorkspaceOption[] }>("/api/auth/v1/session/workspaces");
    const raw = Array.isArray(data?.workspaces) ? data.workspaces : [];
    return raw
      .filter((item): item is WorkspaceOption => Boolean(item) && typeof item === "object")
      .map((item) => ({ id: String(item.id ?? ""), slug: String(item.slug ?? ""), name: String(item.name ?? "") }))
      .filter((option) => option.id !== "");
  }, [client]);

  /**
   * 切换工作区。
   *
   * 成功判定以服务端会话为唯一事实来源：切换后重新读取 /session，
   * workspace_id 必须真的等于目标值，否则明确失败，绝不让前端假装已切换。
   */
  const switchWorkspace = useCallback(
    async (workspaceId: string): Promise<AuthSession> => {
      const target = workspaceId.trim();
      if (!target) {
        throw new ApiError(400, {
          code: "AUTHN_WORKSPACE_REQUIRED",
          message: "必须显式指定目标工作区",
          field_errors: [],
          retryable: false,
        });
      }
      await client.post("/api/auth/v1/session/workspace", { workspace_id: target });
      const next = await reload();
      if (!next || next.workspace_id !== target) {
        throw new ApiError(500, {
          code: "AUTHN_WORKSPACE_SWITCH_NOT_APPLIED",
          message: "工作区切换未生效，请重新登录后再试",
          field_errors: [],
          retryable: true,
        });
      }
      return next;
    },
    [client, reload],
  );

  const logout = useCallback(async () => {
    try {
      await client.post("/api/auth/v1/logout", {});
    } finally {
      setSession(null);
    }
  }, [client]);

  const can = useCallback((permission: string) => Boolean(session?.permissions?.includes(permission)), [session]);
  const canAny = useCallback(
    (...permissions: string[]) => permissions.some((p) => Boolean(session?.permissions?.includes(p))),
    [session],
  );

  const value = useMemo<AuthContextValue>(
    () => ({
      session,
      loading,
      client,
      login,
      logout,
      reload,
      can,
      canAny,
      workspaceOptions,
      switchWorkspace,
      verifyMfa,
      mfaStepUp,
      mfaEnrollStart,
      mfaEnrollConfirm,
      mfaDisable,
    }),
    [
      session,
      loading,
      client,
      login,
      logout,
      reload,
      can,
      canAny,
      workspaceOptions,
      switchWorkspace,
      verifyMfa,
      mfaStepUp,
      mfaEnrollStart,
      mfaEnrollConfirm,
      mfaDisable,
    ],
  );

  /**
   * 工作区作用域键。切换工作区（或退出登录）时用它强制重建整棵子树：
   * 所有页面组件卸载重挂，页面内的私有缓存随之丢弃、每个 useEffect 重新取数，
   * 在途旧请求由各自的 cleanup 中止。
   *
   * 这一层是兜底：页面里既有 useApi（自带作用域键 + AbortController），
   * 也有自己调 client.get 的页面，靠逐页修改无法保证"无闪现旧工作区数据"。
   */
  const scopeKey = session
    ? `${session.audience}:${session.organization_id}:${session.workspace_id ?? ""}`
    : "anonymous";

  return (
    <AuthContext.Provider value={value}>
      <React.Fragment key={scopeKey}>{children}</React.Fragment>
    </AuthContext.Provider>
  );
}
