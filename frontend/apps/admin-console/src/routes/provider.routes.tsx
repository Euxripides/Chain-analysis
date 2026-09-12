import { lazy, type ReactNode } from "react";
import { Gauge, KeyRound, Route, Server } from "lucide-react";

/**
 * S6-FE-01 · Provider / 路由 / 性能 / API Key 域的路由与导航注册。
 *
 * 页面只从真实 API 读取数据（/api/admin/v1/*），不带任何占位数据；
 * 页面正文不出现提示类文案，所有提示一律走 useDialog()（AGENTS.md / AD-009）。
 */

const ProvidersPage = lazy(() => import("../pages/ProvidersPage"));
const ProviderDetailPage = lazy(() => import("../pages/ProviderDetailPage"));
const RoutingPage = lazy(() => import("../pages/RoutingPage"));
const PerformancePage = lazy(() => import("../pages/PerformancePage"));
const ApiKeysPage = lazy(() => import("../pages/ApiKeysPage"));

export interface S6RouteEntry {
  path: string;
  permission: string;
  element: ReactNode;
}

/** 具体路径排在动态段之前，便于阅读；React Router 也按静态优先级匹配。 */
export const s6ProviderRoutes: S6RouteEntry[] = [
  { path: "/providers", permission: "admin:provider:read", element: <ProvidersPage /> },
  { path: "/providers/routing", permission: "admin:routing:read", element: <RoutingPage /> },
  { path: "/providers/:provider", permission: "admin:provider:read", element: <ProviderDetailPage /> },
  { path: "/runtime/performance", permission: "admin:performance:read", element: <PerformancePage /> },
  { path: "/api-keys", permission: "admin:apikey:read", element: <ApiKeysPage /> },
];

export const s6ProviderNavGroup = {
  group: "Provider 与 API",
  items: [
    { to: "/providers", label: "Provider 账户", icon: <Server size={16} /> },
    { to: "/providers/routing", label: "路由策略", icon: <Route size={16} /> },
    { to: "/runtime/performance", label: "性能控制", icon: <Gauge size={16} /> },
    { to: "/api-keys", label: "API Key", icon: <KeyRound size={16} /> },
  ],
};
