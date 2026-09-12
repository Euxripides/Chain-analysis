import React, { Suspense, lazy } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
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
import { AppShell, EmptyState, NoPermissionState, PageHeader, SkeletonRows, useDialog, type NavGroup } from "@chain/ui";
import { AuthProvider, useAuth } from "@chain/api-client";

const LoginPage = lazy(() => import("./pages/LoginPage"));
const HomePage = lazy(() => import("./pages/HomePage"));
const CasesPage = lazy(() => import("./pages/CasesPage"));
const CaseNewPage = lazy(() => import("./pages/CaseNewPage"));
const CaseDetailPage = lazy(() => import("./pages/CaseDetailPage"));
const InvestigationsPage = lazy(() => import("./pages/InvestigationsPage"));
const InvestigationNewPage = lazy(() => import("./pages/InvestigationNewPage"));
const InvestigationWorkbenchPage = lazy(() => import("./pages/InvestigationWorkbenchPage"));
const FindingsPage = lazy(() => import("./pages/FindingsPage"));
const EvidenceDetailPage = lazy(() => import("./pages/EvidenceDetailPage"));
const ReportsPage = lazy(() => import("./pages/ReportsPage"));
const ReportDetailPage = lazy(() => import("./pages/ReportDetailPage"));
const InboxPage = lazy(() => import("./pages/InboxPage"));
const NotificationsPage = lazy(() => import("./pages/NotificationsPage"));
const SearchPage = lazy(() => import("./pages/SearchPage"));
const ProfilePage = lazy(() => import("./pages/ProfilePage"));

/** §3.1 建议导航。未实现的路由一律显示 NOT_IMPLEMENTED 说明，不伪装成可用页面。 */
const navGroups: (permissions: string[]) => NavGroup[] = () => [
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
      { to: "/downloads", label: "数据下载", icon: <Download size={16} /> },
      { to: "/jobs", label: "任务中心", icon: <ListChecks size={16} /> },
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

const ROUTE_PERMISSIONS: Record<string, string> = {
  "/home": "dashboard:read",
  "/search": "search:read",
  "/cases": "case:list",
  "/cases/new": "case:create",
  "/investigations": "investigation:list",
  "/investigations/new": "investigation:create",
  "/graph": "canvas:list",
  "/explorer": "explorer:read",
  "/downloads": "download:create",
  "/jobs": "job:list:self",
  "/inbox": "inbox:read",
  "/notifications": "notification:list",
  "/reports": "report:list",
  "/profile": "profile:read",
};

export function App() {
  return (
    <AuthProvider audience="user">
      <Shell />
    </AuthProvider>
  );
}

function Shell() {
  const { session, loading, logout } = useAuth();
  const location = useLocation();
  const navigate = useNavigate();

  if (loading) {
    return (
      <div className="shell__content">
        <PageHeader title="正在校验会话" subtitle="会话状态以服务端返回为准，不使用本地缓存。" />
        <SkeletonRows rows={4} columns={3} />
      </div>
    );
  }

  if (!session) {
    return (
      <Suspense fallback={null}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="*" element={<Navigate to="/login" replace state={{ from: location.pathname }} />} />
        </Routes>
      </Suspense>
    );
  }

  return (
    <AppShell
      productName="ChainSight"
      productTag="调查工作台 · user"
      brandMark="CS"
      env={session.audience === "user" ? "用户层系统" : session.audience}
      navGroups={navGroups(session.permissions)}
      user={{ displayName: session.display_name, email: session.email, roles: session.roles }}
      headerExtra={null}
      onLogout={async () => {
        await logout();
        navigate("/login", { replace: true });
      }}
    >
      <Suspense fallback={<SkeletonRows rows={6} columns={4} />}>
        <Routes>
          <Route path="/" element={<Navigate to="/home" replace />} />
          <Route path="/login" element={<Navigate to="/home" replace />} />
          <Route path="/home" element={<Guard permission="dashboard:read"><HomePage /></Guard>} />
          <Route path="/search" element={<Guard permission="search:read"><SearchPage /></Guard>} />
          <Route path="/cases" element={<Guard permission="case:list"><CasesPage /></Guard>} />
          <Route path="/cases/new" element={<Guard permission="case:create"><CaseNewPage /></Guard>} />
          <Route path="/cases/:caseId/:tab" element={<Guard permission="case:read"><CaseDetailPage /></Guard>} />
          <Route path="/investigations" element={<Guard permission="investigation:list"><InvestigationsPage /></Guard>} />
          <Route path="/investigations/new" element={<Guard permission="investigation:create"><InvestigationNewPage /></Guard>} />
          <Route path="/investigations/:id/workbench" element={<Guard permission="investigation:read"><InvestigationWorkbenchPage /></Guard>} />
          <Route path="/investigations/:id/findings" element={<Guard permission="finding:list"><FindingsPage /></Guard>} />
          <Route path="/investigations/:id/evidence/:evidenceId" element={<Guard permission="evidence:read"><EvidenceDetailPage /></Guard>} />
          <Route path="/reports" element={<Guard permission="report:list"><ReportsPage /></Guard>} />
          <Route path="/reports/:id" element={<Guard permission="report:read"><ReportDetailPage /></Guard>} />
          <Route path="/inbox" element={<Guard permission="inbox:read"><InboxPage /></Guard>} />
          <Route path="/notifications" element={<Guard permission="notification:list"><NotificationsPage /></Guard>} />
          <Route path="/profile" element={<Guard permission="profile:read"><ProfilePage /></Guard>} />
          <Route path="/graph" element={<Placeholder permission="canvas:list" title="资金图谱" sprint="S5" />} />
          <Route path="/explorer" element={<Placeholder permission="explorer:read" title="地址与交易查询" sprint="S5" />} />
          <Route path="/downloads" element={<Placeholder permission="download:create" title="数据下载" sprint="S5" />} />
          <Route path="/jobs" element={<Placeholder permission="job:list:self" title="任务中心" sprint="S5" />} />
          <Route path="*" element={<Placeholder title="页面不存在" />} />
        </Routes>
      </Suspense>
    </AppShell>
  );
}

function Guard({ permission, children }: { permission?: string; children: React.ReactNode }) {
  const { can } = useAuth();
  if (permission && !can(permission)) {
    return (
      <div className="card">
        <NoPermissionState permission={permission} />
      </div>
    );
  }
  return <>{children}</>;
}

/**
 * 未实现页面：显式说明能力边界，并保留旧工作台的入口，避免用户以为功能损坏。
 * 这里不使用页面内提示横幅，说明信息通过弹窗提供（AGENTS.md 约束）。
 */
function Placeholder({ title, permission, sprint }: { title: string; permission?: string; sprint?: string }) {
  const dialog = useDialog();
  return (
    <div className="card">
      <EmptyState
        title={`${title} · 尚未在此版本开放`}
        body={
          <>
            该模块记录在 dual-system-development-plan 的 {sprint ?? "后续"} Sprint 中，当前状态为 NOT_IMPLEMENTED。
            旧的工程工作台仍然可用。
          </>
        }
        action={
          <span className="toolbar">
            <a className="btn btn--sm" href="/" target="_self">
              打开旧工作台
            </a>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() =>
                dialog.notify("info", `${title} 的能力边界`, null, [
                  { label: "目标 Sprint", value: sprint ?? "未排期" },
                  { label: "所需权限", value: permission ?? "—" },
                  { label: "当前状态", value: "NOT_IMPLEMENTED" },
                ])
              }
            >
              查看说明
            </button>
          </span>
        }
      />
    </div>
  );
}

export { ROUTE_PERMISSIONS };
