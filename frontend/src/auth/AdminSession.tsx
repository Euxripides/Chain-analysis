import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { probeAdminSession, type AdminSessionProbe } from "../api/client";

/**
 * 后台会话上下文（R-3）。
 *
 * 旧 V1 SPA 同时承载**用户层**页面（地址下载 / 任务 / 调查 / 数据浏览）与
 * **后台**页面（`/api-control` API 控制中心、`/system` 状态总览）。
 * 后台页面调用的是 `admin:*` 权限键的接口，必须运行在 admin 受众会话下；
 * 而 admin 受众会话只能从**后台入口** `/admin/login` 签发（§19/§3.3）。
 *
 * 本上下文只做两件事：
 *   1. 只读探测当前浏览器是否持有 admin 会话（`GET /api/auth/v1/session` + admin 受众）；
 *   2. 把结论暴露给路由，用于**导航**——没有后台会话时不要假装后台页面可用，
 *      而是把用户导向后台入口。
 *
 * 它**不**参与任何授权判定：真正的放行/拒绝仍由服务端 Policy 引擎给出。
 */
export type AdminSessionState = {
  status: "checking" | "authenticated" | "anonymous";
  probe: AdminSessionProbe;
  refresh: () => void;
};

const AdminSessionContext = createContext<AdminSessionState>({
  status: "checking",
  probe: { authenticated: false },
  refresh: () => {},
});

export function AdminSessionProvider({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<AdminSessionState["status"]>("checking");
  const [probe, setProbe] = useState<AdminSessionProbe>({ authenticated: false });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    let active = true;
    setStatus("checking");
    probeAdminSession(controller.signal).then((result) => {
      if (!active) return;
      setProbe(result);
      setStatus(result.authenticated ? "authenticated" : "anonymous");
    });
    return () => {
      active = false;
      controller.abort();
    };
  }, [nonce]);

  const refresh = useCallback(() => setNonce((value) => value + 1), []);
  const value = useMemo(() => ({ status, probe, refresh }), [status, probe, refresh]);
  return <AdminSessionContext.Provider value={value}>{children}</AdminSessionContext.Provider>;
}

export function useAdminSession(): AdminSessionState {
  return useContext(AdminSessionContext);
}

/** 需要后台会话的路由前缀。 */
export const BACKEND_ROUTES = ["/api-control", "/system"];

export function isBackendRoute(pathname: string): boolean {
  return BACKEND_ROUTES.some(
    (base) => pathname === base || pathname.startsWith(base + "/"),
  );
}

/** 后台入口（携带 return_to，登录后回到原页面）。 */
export function backendLoginHref(pathname: string, search: string): string {
  const returnTo = pathname + search;
  return `/admin/login?return_to=${encodeURIComponent(returnTo)}`;
}
