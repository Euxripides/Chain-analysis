// Sprint 6 · 控制面 / Kill Switch / 备份恢复 / 保留策略 / 版本 / 可观测性 的路由与导航注册表。
//
// 本文件只导出注册表，不修改 App.tsx：由 App 层消费 s6ControlRoutes / s6ControlNavGroups，
// 统一套用 <Guard permission={...}> 与懒加载 Suspense。

import { lazy, type ReactNode } from "react";

const ControlPage = lazy(() => import("../pages/ControlPage"));
const KillSwitchPage = lazy(() => import("../pages/KillSwitchPage"));
const BackupsPage = lazy(() => import("../pages/BackupsPage"));
const RetentionPage = lazy(() => import("../pages/RetentionPage"));
const SystemVersionPage = lazy(() => import("../pages/SystemVersionPage"));
const ObservabilityPage = lazy(() => import("../pages/ObservabilityPage"));

export interface S6RouteEntry {
  path: string;
  permission: string;
  element: ReactNode;
}

/** S6-FE-02 / S6-OBS-01 / S6-BE-04 的 UI 路由表。 */
export const s6ControlRoutes: S6RouteEntry[] = [
  { path: "/control", permission: "admin:killswitch:read", element: <ControlPage /> },
  { path: "/control/kill-switch", permission: "admin:killswitch:read", element: <KillSwitchPage /> },
  { path: "/system/backups", permission: "admin:backup:read", element: <BackupsPage /> },
  { path: "/data/retention", permission: "admin:retention:read", element: <RetentionPage /> },
  { path: "/system/version", permission: "admin:version:read", element: <SystemVersionPage /> },
  { path: "/observability", permission: "admin:metrics:read", element: <ObservabilityPage /> },
];

export const s6ControlNavGroups: { group: string; items: { to: string; label: string }[] }[] = [
  {
    group: "运营总览",
    items: [
      { to: "/observability", label: "可观测性" },
    ],
  },
  {
    group: "系统治理",
    items: [
      { to: "/control", label: "实时控制面" },
      { to: "/control/kill-switch", label: "Kill Switch" },
      { to: "/system/backups", label: "备份与恢复" },
      { to: "/data/retention", label: "保留与删除" },
      { to: "/system/version", label: "版本与迁移" },
    ],
  },
];
