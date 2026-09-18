import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  useQuery as useReactQuery,
  useQueryClient,
  type QueryClient,
  type QueryKey,
  type UseQueryOptions,
  type UseQueryResult,
} from "@tanstack/react-query";

/**
 * 旧工作台（frontend/src）的**工作区作用域**。
 *
 * 计划 §30.1 的硬要求："React Query key 包含 organization/workspace/object/filter；
 * 切换工作区必须清除不安全缓存"。
 *
 * 旧工作台此前所有 useQuery 的 key 只有对象/过滤条件（"jobs"、"job", id、
 * "ac-overview", filter ...），没有 organization/workspace：会话切到另一个工作区后，
 * React Query 会把**上一个工作区**的缓存当作本次结果直接复用（§27 RBAC-003 / UX 验收）。
 *
 * 本模块提供两件事，都是真实消费点而不是等价物：
 *   1. useScopedQuery —— 旧工作台里所有 useQuery 的统一入口，把
 *      {org, ws, aorg, aws} 追加进 queryKey（对象/过滤条件由调用方保留）；
 *   2. SessionScopeProvider —— 只读探测服务端会话（用户受众 + 后台受众），
 *      作用域发生变化（切换工作区 / 退出登录 / 会话轮换）时
 *      cancelQueries + clear，丢弃全部旧作用域缓存；在途请求由 cancel 中止。
 *
 * 作用域只是缓存正确性，**不是**授权判定：放行/拒绝仍由服务端 Policy 引擎给出。
 */

/** 单个受众的会话探测结果。未登录时 authenticated=false，org/ws 均为空串。 */
export interface SessionProbe {
  authenticated: boolean;
  audience: string;
  org: string;
  ws: string;
  csrfToken: string;
}

/** 追加到每个 queryKey 末尾的缓存作用域。 */
export interface CacheScope {
  /** 用户受众会话所属组织。 */
  org: string;
  /** 用户受众会话绑定的工作区。 */
  ws: string;
  /** 后台受众会话所属组织。 */
  aorg: string;
  /** 后台受众会话绑定的工作区。 */
  aws: string;
}

export interface SessionScopeState {
  status: "checking" | "ready";
  user: SessionProbe;
  admin: SessionProbe;
  scope: CacheScope;
  scopeKey: string;
  /** 重新探测会话（作用域变化时自动清空 React Query 缓存）。 */
  refresh: () => void;
  /** 退出登录：调用服务端注销 -> cancelQueries -> clear。 */
  logout: (audience?: "user" | "admin") => Promise<void>;
}

const ANONYMOUS: SessionProbe = {
  authenticated: false,
  audience: "",
  org: "",
  ws: "",
  csrfToken: "",
};

const EMPTY_SCOPE: CacheScope = { org: "", ws: "", aorg: "", aws: "" };

/** 探测某个受众的当前会话；任何非 2xx（含 401/403）都按"未登录"处理，不抛异常。 */
export async function probeSession(
  audience: "user" | "admin",
  signal?: AbortSignal,
): Promise<SessionProbe> {
  try {
    const res = await fetch("/api/auth/v1/session", {
      headers: { Accept: "application/json", "X-Auth-Audience": audience },
      credentials: "same-origin",
      signal,
    });
    if (!res.ok) return { ...ANONYMOUS, audience };
    const body = (await res.json()) as Record<string, unknown>;
    const data = (body && typeof body === "object" && "data" in body
      ? (body as { data?: unknown }).data
      : body) as Record<string, unknown> | null;
    if (!data || typeof data !== "object") return { ...ANONYMOUS, audience };
    return {
      authenticated: true,
      audience: String(data.audience ?? audience),
      org: String(data.organization_id ?? ""),
      ws: String(data.workspace_id ?? ""),
      csrfToken: String(data.csrf_token ?? ""),
    };
  } catch {
    return { ...ANONYMOUS, audience };
  }
}

/** 作用域标识：任一受众的组织/工作区变化都会让缓存键整体失效。 */
export function scopeKeyOf(user: SessionProbe, admin: SessionProbe): string {
  return [
    user.audience || "user",
    user.org,
    user.ws,
    admin.audience || "admin",
    admin.org,
    admin.ws,
  ].join("|");
}

const SessionScopeContext = createContext<SessionScopeState>({
  status: "checking",
  user: ANONYMOUS,
  admin: ANONYMOUS,
  scope: EMPTY_SCOPE,
  scopeKey: scopeKeyOf(ANONYMOUS, ANONYMOUS),
  refresh: () => {},
  logout: async () => {},
});

/**
 * 会话作用域提供者。必须放在 <QueryClientProvider> 之内（需要 queryClient）。
 *
 * 探测只在挂载时、窗口重新获得焦点时、以及每 30s 做一次：足以覆盖"用户在另一个
 * 标签页/设备切换了工作区"，又不会给每次都按键增加请求。
 */
export function SessionScopeProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient();
  const [status, setStatus] = useState<SessionScopeState["status"]>("checking");
  const [user, setUser] = useState<SessionProbe>(ANONYMOUS);
  const [admin, setAdmin] = useState<SessionProbe>(ANONYMOUS);
  const [nonce, setNonce] = useState(0);
  const scopeRef = useRef<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    Promise.all([
      probeSession("user", controller.signal),
      probeSession("admin", controller.signal),
    ]).then(([u, a]) => {
      if (!active) return;
      setUser(u);
      setAdmin(a);
      setStatus("ready");
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [nonce]);

  const scopeKey = scopeKeyOf(user, admin);

  useEffect(() => {
    if (status !== "ready") return;
    const previous = scopeRef.current;
    scopeRef.current = scopeKey;
    if (previous === null || previous === scopeKey) return;
    // 作用域真的变了：先中止在途请求，再清空全部旧作用域缓存。
    // 顺序不能颠倒 —— 先 clear 再 cancel 会让已发出的响应重新写回缓存。
    void queryClient.cancelQueries().then(() => queryClient.clear());
  }, [queryClient, scopeKey, status]);

  useEffect(() => {
    const onFocus = () => setNonce((value) => value + 1);
    window.addEventListener("focus", onFocus);
    const timer = window.setInterval(onFocus, 30_000);
    return () => {
      window.removeEventListener("focus", onFocus);
      window.clearInterval(timer);
    };
  }, []);

  const refresh = useCallback(() => setNonce((value) => value + 1), []);

  const logout = useCallback(
    async (audience: "user" | "admin" = "user") => {
      const probe = audience === "admin" ? admin : user;
      try {
        await fetch("/api/auth/v1/logout", {
          method: "POST",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/json",
            "X-Auth-Audience": audience,
            ...(probe.csrfToken ? { "X-CSRF-Token": probe.csrfToken } : {}),
          },
          credentials: "same-origin",
          body: "{}",
        });
      } finally {
        // 无论服务端返回什么，本地都不得继续持有旧作用域数据。
        await queryClient.cancelQueries();
        queryClient.clear();
        setNonce((value) => value + 1);
      }
    },
    [admin, queryClient, user],
  );

  const scope = useMemo<CacheScope>(
    () => ({ org: user.org, ws: user.ws, aorg: admin.org, aws: admin.ws }),
    [admin.org, admin.ws, user.org, user.ws],
  );

  const value = useMemo<SessionScopeState>(
    () => ({ status, user, admin, scope, scopeKey, refresh, logout }),
    [admin, logout, refresh, scope, scopeKey, status, user],
  );

  return <SessionScopeContext.Provider value={value}>{children}</SessionScopeContext.Provider>;
}

export function useSessionScope(): SessionScopeState {
  return useContext(SessionScopeContext);
}

/**
 * 作用域化的 useQuery：行为与 @tanstack/react-query 的 useQuery 完全一致，
 * 只把 {org, ws, aorg, aws} 追加到 queryKey 末尾。
 *
 * 追加（而不是前缀）是刻意的：既有的 queryClient.invalidateQueries({queryKey:["jobs"]})
 * 依赖**前缀**匹配，追加作用域后这些失效调用依然命中；换成前缀会让所有失效静默失效。
 */
export function useScopedQuery<
  TQueryFnData = unknown,
  TError = Error,
  TData = TQueryFnData,
>(
  options: UseQueryOptions<TQueryFnData, TError, TData, QueryKey>,
  queryClient?: QueryClient,
): UseQueryResult<TData, TError> {
  const { scope } = useSessionScope();
  const base = Array.isArray(options.queryKey) ? options.queryKey : [];
  const scopedKey = useMemo<QueryKey>(() => [...base, scope], [base, scope]);
  return useReactQuery(
    { ...options, queryKey: scopedKey } as UseQueryOptions<TQueryFnData, TError, TData, QueryKey>,
    queryClient,
  );
}
