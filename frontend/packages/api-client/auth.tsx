import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { ApiClient, ApiError, type AuthSession, type LoginResult } from "./client";

/**
 * 会话上下文：会话状态只有一个事实来源（服务端 /api/auth/v1/session）。
 * 前端不缓存 Token，也不从 localStorage 恢复身份（§28.4 验收"前端无 JS 可读 Token"）。
 */

export interface AuthContextValue {
  session: AuthSession | null;
  loading: boolean;
  client: ApiClient;
  login: (identifier: string, password: string, audience: "user" | "admin") => Promise<LoginResult>;
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
      // 服务端已经按 X-Auth-Audience 收窄受众；这里再做一次客户端断言，
      // 防止任何回退逻辑让用户层会话被后台应用误认为已登录（§3.3）。
      if (data && data.audience !== audience) {
        setSession(null);
        return null;
      }
      setSession(data);
      return data;
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

  const login = useCallback(
    async (identifier: string, password: string, target: "user" | "admin") => {
      const { data } = await client.post<LoginResult>("/api/auth/v1/login", {
        identifier,
        password,
        audience: target,
        // next 是应用内相对路径（§21.4），前端在 basename 之下直接使用。
        return_to: target === "admin" ? "/overview" : "/home",
      });
      const next = await reload();
      if (!next) {
        // 登录成功但会话读取失败：明确失败，绝不假装已登录。
        throw new ApiError(500, {
          code: "AUTHN_SESSION_UNAVAILABLE",
          message: "登录成功但无法建立会话，请检查 Cookie 是否被浏览器拦截",
          field_errors: [],
          retryable: true,
        });
      }
      return data;
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
    () => ({ session, loading, client, login, logout, reload, can, canAny }),
    [session, loading, client, login, logout, reload, can, canAny],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}
