import { lazy, type ReactNode } from "react";

/**
 * S6-FE-01 / S6-FE-02 · 请求审计、成本、预算、告警、配置与功能开关域的路由与导航注册。
 *
 * 页面只从真实 API 读取数据（/api/admin/v1/*），不使用占位数据；
 * 页面正文不出现任何提示类文案，全部提示一律走 useDialog()（AGENTS.md / docs/code-standards.md / AD-009）。
 */

const RequestsPage = lazy(() => import("../pages/RequestsPage"));
const CostsPage = lazy(() => import("../pages/CostsPage"));
const BudgetsPage = lazy(() => import("../pages/BudgetsPage"));
const AlertsPage = lazy(() => import("../pages/AlertsPage"));
const SettingsPage = lazy(() => import("../pages/SettingsPage"));
const FeatureFlagsPage = lazy(() => import("../pages/FeatureFlagsPage"));

export interface S6RouteEntry {
  path: string;
  permission: string;
  element: ReactNode;
}

export const s6GovernanceRoutes: S6RouteEntry[] = [
  { path: "/requests", permission: "admin:request:read", element: <RequestsPage /> },
  { path: "/costs", permission: "admin:cost:read", element: <CostsPage /> },
  { path: "/budgets", permission: "admin:budget:read", element: <BudgetsPage /> },
  { path: "/alerts", permission: "admin:alert:read", element: <AlertsPage /> },
  { path: "/settings", permission: "admin:config:read", element: <SettingsPage /> },
  { path: "/feature-flags", permission: "admin:flag:read", element: <FeatureFlagsPage /> },
];

export const s6GovernanceNavGroups: { group: string; items: { to: string; label: string }[] }[] = [
  {
    group: "Provider 与 API",
    items: [
      { to: "/requests", label: "请求审计" },
      { to: "/costs", label: "成本" },
      { to: "/budgets", label: "预算" },
    ],
  },
  {
    group: "安全与审计",
    items: [{ to: "/alerts", label: "告警中心" }],
  },
  {
    group: "系统治理",
    items: [
      { to: "/settings", label: "系统设置" },
      { to: "/feature-flags", label: "功能开关" },
    ],
  },
];
