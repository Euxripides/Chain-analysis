import React from "react";
import {
  Download,
  FileText,
  FolderOpen,
  Gauge,
  Inbox,
  ListChecks,
  Network,
  Search,
  SlidersHorizontal,
  User,
} from "lucide-react";
import type { NavGroup } from "@chain/ui";

/**
 * 导航、权限与页面标题（蓝图 §5 / §5.4 / §19.4）
 *
 * 从 App.tsx 抽出，**路由、权限名与裁剪规则完全不变**：
 * ROUTE_PERMISSIONS 仍是导航裁剪的唯一依据，查不到权限声明的入口继续 fail-closed。
 */

/** 蓝图 §5 的分组导航。未实现的路由不在此出现，也不伪装成可用页面。 */
export const baseNavGroups: NavGroup[] = [
  {
    label: "工作台",
    items: [
      { to: "/home", label: "我的概览", icon: <Gauge size={16} /> },
      { to: "/search", label: "全局搜索", icon: <Search size={16} /> },
    ],
  },
  {
    label: "案件与调查",
    items: [
      { to: "/cases", label: "案件中心", icon: <FolderOpen size={16} /> },
      { to: "/investigations", label: "调查任务", icon: <SlidersHorizontal size={16} /> },
      { to: "/graph", label: "资金图谱", icon: <Network size={16} /> },
    ],
  },
  {
    label: "数据工具",
    items: [
      { to: "/explorer", label: "地址与交易查询", icon: <Search size={16} /> },
      { to: "/downloads/new", label: "数据下载", icon: <Download size={16} /> },
      { to: "/jobs", label: "任务中心", icon: <ListChecks size={16} /> },
      { to: "/exports", label: "导出中心", icon: <FileText size={16} /> },
    ],
  },
  {
    label: "协作",
    items: [
      { to: "/inbox", label: "待我处理", icon: <Inbox size={16} /> },
      { to: "/reports", label: "报告中心", icon: <FileText size={16} /> },
      { to: "/notifications", label: "通知", icon: <FileText size={16} /> },
    ],
  },
  {
    label: "个人",
    items: [{ to: "/profile", label: "个人资料与安全", icon: <User size={16} /> }],
  },
];

/** 路由 → 所需权限。**不得修改任何键或值**：服务端 Guard 与导航裁剪共用这张表。 */
export const ROUTE_PERMISSIONS: Record<string, string> = {
  "/home": "dashboard:read",
  "/search": "search:read",
  "/cases": "case:list",
  "/cases/new": "case:create",
  "/investigations": "investigation:list",
  "/investigations/new": "investigation:create",
  "/graph": "canvas:list",
  "/explorer": "explorer:read",
  "/downloads": "download:create",
  "/downloads/new": "download:create",
  "/jobs": "job:list:self",
  "/exports": "export:list",
  "/canvases": "canvas:read",
  "/addresses": "explorer:read",
  "/transactions": "explorer:read",
  "/inbox": "inbox:read",
  "/notifications": "notification:list",
  "/reports": "report:list",
  "/profile": "profile:read",
};

/**
 * 取某个导航路径所需的权限：先精确匹配，再按最长前缀匹配（例如 /canvases/xxx 归到 /canvases）。
 *
 * 这张表此前只被定义、从未参与渲染（导航对所有人一视同仁）。现在它是导航裁剪的唯一依据：
 * 会话缺少权限的入口不渲染；**查不到权限声明的入口也不渲染**（fail-closed，宁可少显示，
 * 也不给出走不通的入口）。服务端 <Guard> 与逐端点鉴权仍然是权威判据。
 */
export const requiredPermission = (to: string): string | undefined => {
  if (ROUTE_PERMISSIONS[to]) return ROUTE_PERMISSIONS[to];
  const prefix = Object.keys(ROUTE_PERMISSIONS)
    .filter((key) => to.startsWith(key + "/"))
    .sort((a, b) => b.length - a.length)[0];
  return prefix ? ROUTE_PERMISSIONS[prefix] : undefined;
};

/** §3.1 / §7.5 同源规则：导航入口按当前会话权限裁剪（权限来自 ROUTE_PERMISSIONS）。 */
export const navGroups = (permissions: string[]): NavGroup[] => {
  const allowed = new Set(permissions);
  return baseNavGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        const permission = requiredPermission(item.to);
        return Boolean(permission) && allowed.has(permission as string);
      }),
    }))
    .filter((group) => group.items.length > 0);
};

/** 详情页的短标题（移动端顶栏用），按最长前缀匹配到所属模块。 */
export function pageTitleFor(pathname: string): string {
  const flattened = baseNavGroups.flatMap((group) => group.items);
  const exact = flattened.filter((item) => pathname === item.to || pathname.startsWith(item.to + "/"));
  if (exact.length > 0) return [...exact].sort((a, b) => b.to.length - a.to.length)[0].label;
  if (pathname.startsWith("/canvases")) return "资金图谱";
  if (pathname.startsWith("/addresses")) return "地址详情";
  if (pathname.startsWith("/transactions")) return "交易详情";
  if (pathname.startsWith("/jobs/")) return "任务详情";
  if (pathname.startsWith("/reports/")) return "报告详情";
  if (pathname.startsWith("/investigations/")) return "调查";
  if (pathname.startsWith("/cases/")) return "案件详情";
  return "ChainSight";
}
