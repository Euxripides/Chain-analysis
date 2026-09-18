import React, { Suspense, lazy, useCallback, useEffect, useState } from "react";
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
import { ApiError, AuthProvider, useAuth, type WorkspaceOption } from "@chain/api-client";

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
const ExplorerPage = lazy(() => import("./pages/ExplorerPage"));
const AddressDetailPage = lazy(() => import("./pages/AddressDetailPage"));
const TransactionDetailPage = lazy(() => import("./pages/TransactionDetailPage"));
const DownloadNewPage = lazy(() => import("./pages/DownloadNewPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const ExportsPage = lazy(() => import("./pages/ExportsPage"));
const CanvasListPage = lazy(() => import("./pages/CanvasListPage"));
const CanvasPage = lazy(() => import("./pages/CanvasPage"));

/** §3.1 建议导航。未实现的路由一律显示 NOT_IMPLEMENTED 说明，不伪装成可用页面。 */
const baseNavGroups: NavGroup[] = [
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
const requiredPermission = (to: string): string | undefined => {
  if (ROUTE_PERMISSIONS[to]) return ROUTE_PERMISSIONS[to];
  const prefix = Object.keys(ROUTE_PERMISSIONS)
    .filter((key) => to.startsWith(key + "/"))
    .sort((a, b) => b.length - a.length)[0];
  return prefix ? ROUTE_PERMISSIONS[prefix] : undefined;
};

/** §3.1 / §7.5 同源规则：导航入口按当前会话权限裁剪（权限来自 ROUTE_PERMISSIONS）。 */
const navGroups = (permissions: string[]): NavGroup[] => {
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
      headerExtra={<WorkspaceSwitcher />}
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
          <Route path="/graph" element={<Guard permission="canvas:list"><CanvasListPage /></Guard>} />
          <Route path="/canvases/:canvasId" element={<Guard permission="canvas:read"><CanvasPage /></Guard>} />
          <Route path="/explorer" element={<Guard permission="explorer:read"><ExplorerPage /></Guard>} />
          <Route path="/addresses/:chain/:address" element={<Guard permission="explorer:read"><AddressDetailPage /></Guard>} />
          <Route path="/transactions/:chain/:hash" element={<Guard permission="explorer:read"><TransactionDetailPage /></Guard>} />
          <Route path="/downloads" element={<Navigate to="/downloads/new" replace />} />
          <Route path="/downloads/new" element={<Guard permission="download:create"><DownloadNewPage /></Guard>} />
          <Route path="/jobs" element={<Guard permission="job:list:self"><JobsPage /></Guard>} />
          <Route path="/jobs/:jobId" element={<Guard permission="job:read"><JobDetailPage /></Guard>} />
          <Route path="/exports" element={<Guard permission="export:list"><ExportsPage /></Guard>} />
          <Route path="*" element={<Placeholder title="页面不存在" />} />
        </Routes>
      </Suspense>
    </AppShell>
  );
}

/**
 * 登录后的工作区切换器（P1-20260913 后半段）。
 *
 * 与会话是两件事：登录页的选择器决定"用哪个工作区建立会话"，
 * 这里决定"已建立的会话切到哪个工作区"，由服务端按成员资格判定并写审计。
 * 一切提示只走弹窗（AGENTS.md）。
 */
function WorkspaceSwitcher() {
  const { session, workspaceOptions, switchWorkspace } = useAuth();
  const dialog = useDialog();
  const [options, setOptions] = useState<WorkspaceOption[]>([]);
  const [busy, setBusy] = useState(false);
  const currentId = session?.workspace_id ?? "";

  useEffect(() => {
    let alive = true;
    void workspaceOptions()
      .then((list) => {
        if (alive) setOptions(list);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [workspaceOptions]);

  const current = options.find((option) => option.id === currentId);
  const label = current ? `${current.name}（${current.slug}）` : `${currentId.slice(0, 8)}…`;

  const open = useCallback(async () => {
    setBusy(true);
    try {
      const list = await workspaceOptions();
      setOptions(list);
      if (list.length <= 1) {
        dialog.notify("info", "没有可切换的工作区", "该账号在当前组织内只有一个有效工作区成员资格，无法切换。");
        return;
      }
      const picked = await dialog.confirm({
        kind: "info",
        title: "切换工作区",
        body: "切换后当前页面的数据会按新工作区重新加载，未提交的编辑内容不会保留。",
        actions: [
          { label: "取消", result: "", variant: "default" },
          ...list.map((option) => ({
            label: option.id === currentId ? `${option.name}（当前）` : `${option.name}（${option.slug}）`,
            value: option.id,
            result: option.id,
            variant: option.id === currentId ? ("default" as const) : ("primary" as const),
          })),
        ],
      });
      if (!picked || picked === "" || picked === currentId) return;
      await switchWorkspace(picked);
      dialog.notify("success", "已切换工作区", "页面数据已按新工作区重新加载。");
    } catch (err) {
      dialog.notify("error", "切换工作区失败", err instanceof ApiError ? err.userMessage : "请稍后重试");
    } finally {
      setBusy(false);
    }
  }, [currentId, dialog, switchWorkspace, workspaceOptions]);

  return (
    <button
      type="button"
      className="btn btn--ghost btn--sm"
      data-testid="workspace-switcher"
      data-audience="user"
      aria-label="切换工作区"
      onClick={() => void open()}
      disabled={busy}
    >
      {busy ? "切换中…" : label}
    </button>
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
            旧控制台已于 2026-09-18 退出公网（局域网入口关闭），此处不再提供入口。
          </>
        }
        action={
          <span className="toolbar">
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
