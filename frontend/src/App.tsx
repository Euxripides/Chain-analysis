import { lazy, Suspense, useState } from "react";
import { NavLink, Route, Routes, useLocation } from "react-router-dom";
import { AdminSessionProvider, useAdminSession } from "./auth/AdminSession";
import { useSessionScope } from "./lib/sessionScope";
import BackendLoginDialog from "./components/BackendLoginDialog";
import {
  Bell,
  ChevronDown,
  Download,
  Gauge,
  ListChecks,
  Lock,
  LogOut,
  Menu,
  Orbit,
  Network,
  ScanSearch,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
  SlidersHorizontal,
  X,
} from "lucide-react";
const DownloadPage = lazy(() => import("./pages/DownloadPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const SystemPage = lazy(() => import("./pages/SystemPage"));
const BrowsePage = lazy(() => import("./pages/BrowsePage"));
const DataExplorerPage = lazy(() => import("./pages/DataExplorerPage"));
const AddressDetailPage = lazy(() => import("./pages/AddressDetailPage"));
const CombinedDataPage = lazy(() => import("./pages/CombinedDataPage"));
const ApiControlPage = lazy(
  () => import("./features/api-control/ApiControlPage"),
);
const InvestigationsPage = lazy(() => import("./pages/InvestigationsPage"));
const FundFlowPage = lazy(() => import("./pages/FundFlowPage"));

// requiresAdmin=true 的条目属于**后台管理面**：它们调用的接口是 `admin:*` 权限键，
// 只能在从 `/admin/login` 签发的 admin 受众会话下工作（R-3）。
type NavItem = {
  to: string;
  label: string;
  Icon: React.ComponentType<{ size?: number | string; "aria-label"?: string }>;
  requiresAdmin?: boolean;
};
type NavGroup = { label: string; items: NavItem[] };

const navGroups: NavGroup[] = [
  {
    label: "",
    items: [{ to: "/system", label: "总览", Icon: Gauge, requiresAdmin: true }],
  },
  {
    label: "数据中心",
    items: [
      { to: "/investigations", label: "智能调查", Icon: ScanSearch },
      { to: "/fund-flow", label: "资金分析图", Icon: Network },
      { to: "/data-explorer", label: "地址查询", Icon: Search },
      { to: "/", label: "地址下载", Icon: Download },
      { to: "/jobs", label: "任务管理", Icon: ListChecks },
    ],
  },
  {
    label: "API 控制",
    items: [
      {
        to: "/api-control",
        label: "API 控制中心",
        Icon: SlidersHorizontal,
        requiresAdmin: true,
      },
    ],
  },
  {
    label: "系统管理",
    items: [
      {
        to: "/system?view=settings",
        label: "设置中心",
        Icon: Settings,
        requiresAdmin: true,
      },
    ],
  },
];

const routeNames: Array<[RegExp, string]> = [
  [/^\/jobs\/[^/]+/, "任务详情"],
  [/^\/jobs/, "任务中心"],
  [/^\/system/, "状态总览"],
  [/^\/api-control/, "API 控制中心"],
  [/^\/data-explorer\/[^/]+\//, "地址详情"],
  [/^\/investigations/, "智能调查"],
  [/^\/fund-flow/, "资金分析图"],
  [/^\/data-explorer/, "地址查询"],
  [/^\/browse/, "数据浏览"],
  [/^\/$/, "地址下载"],
];

function WorkspaceShell({ children }: { children: React.ReactNode }) {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const location = useLocation();
  const adminSession = useAdminSession();
  const { scope, user, logout } = useSessionScope();
  const [loggingOut, setLoggingOut] = useState(false);
  const backendLocked = adminSession.status === "anonymous";
  const scopeLabel = !user.authenticated
    ? "未登录"
    : user.ws
      ? `${user.org.slice(0, 8)}… / ${user.ws.slice(0, 8)}…`
      : "未绑定工作区";
  const avatarText = (user.audience || "an").slice(0, 2).toUpperCase();
  const doLogout = async () => {
    setLoggingOut(true);
    try {
      await logout("user");
    } finally {
      setLoggingOut(false);
    }
  };
  const routeName =
    routeNames.find(([pattern]) => pattern.test(location.pathname))?.[1] ??
    "数据工作台";

  return (
    <div className={`workspace-shell${collapsed ? " is-collapsed" : ""}`}>
      <a className="skip-link" href="#main-content">
        跳到主要内容
      </a>
      <header className="workspace-bar">
        <button
          className="icon-button mobile-nav-trigger"
          aria-label="打开主导航"
          onClick={() => setMobileOpen(true)}
        >
          <Menu size={18} />
        </button>
        <div className="workspace-crumb">
          <span>ChainSight Investigations</span>
          <ChevronDown size={14} />
        </div>
        <label className="global-search">
          <Search size={15} />
          <input
            aria-label="全局搜索"
            name="global-search"
            autoComplete="off"
            placeholder="搜索地址 / 交易哈希 / 区块 / 任务…"
          />
          <kbd>⌘ K</kbd>
        </label>
        <div className="workspace-actions">
          <span className="global-provider">
            <i /> Ankr 正常
          </span>
          {/*
            会话作用域指示 + 退出登录（P1-20260913-CACHE）。
            作用域标签来自服务端会话（组织/工作区），不是本地猜测；退出登录会调用
            服务端注销，随后 cancelQueries + clear 丢弃旧作用域的 React Query 缓存（§30.1）。
          */}
          <span
            className="global-provider"
            data-testid="workspace-scope"
            data-org={scope.org}
            data-ws={scope.ws}
            title="当前会话绑定的组织 / 工作区"
          >
            <i /> {scopeLabel}
          </span>
          <button
            className="icon-button"
            aria-label="退出登录"
            data-testid="logout"
            onClick={() => void doLogout()}
            disabled={!user.authenticated || loggingOut}
          >
            <LogOut size={17} />
          </button>
          <button className="icon-button notification-button" aria-label="通知">
            <Bell size={17} />
            <em>3</em>
          </button>
          <button className="avatar-button" aria-label="打开用户菜单">
            {avatarText}
          </button>
        </div>
      </header>

      {mobileOpen && (
        <button
          className="nav-scrim"
          aria-label="关闭主导航"
          onClick={() => setMobileOpen(false)}
        />
      )}
      <aside
        className={`workspace-sidebar${mobileOpen ? " mobile-open" : ""}`}
        aria-label="主导航"
      >
        <div className="workspace-brand">
          <span className="workspace-brand-mark">
            <Orbit size={23} />
          </span>
          <strong>
            Ankr <b>Data</b>
          </strong>
          <button
            className="icon-button mobile-nav-close"
            aria-label="关闭主导航"
            onClick={() => setMobileOpen(false)}
          >
            <X size={18} />
          </button>
        </div>
        <nav>
          {navGroups.map((group) => (
            <div
              className="workspace-nav-group"
              key={group.label || "overview"}
            >
              {group.label && (
                <span className="workspace-nav-label">{group.label}</span>
              )}
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  title={
                    collapsed
                      ? item.label
                      : item.requiresAdmin && backendLocked
                        ? `${item.label}（需要后台会话，将从后台入口登录）`
                        : undefined
                  }
                  data-requires-admin={item.requiresAdmin ? "true" : undefined}
                  data-backend-locked={
                    item.requiresAdmin && backendLocked ? "true" : undefined
                  }
                  end={
                    item.to === "/" ||
                    item.to === "/system" ||
                    item.to === "/api-control"
                  }
                  className={({ isActive }) => {
                    const targetHasQuery = item.to.includes("?");
                    const active = targetHasQuery
                      ? `${location.pathname}${location.search}` === item.to
                      : isActive &&
                        (item.to === "/api-control" || !location.search);
                    return `workspace-nav-item${active ? " active" : ""}`;
                  }}
                  onClick={() => setMobileOpen(false)}
                >
                  <item.Icon size={17} />
                  <span>{item.label}</span>
                  {item.requiresAdmin && backendLocked && (
                    <Lock size={13} aria-label="需要后台会话" />
                  )}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="workspace-sidebar-foot">
          <div className="workspace-user" data-testid="workspace-user">
            <span>{adminSession.probe.authenticated ? "AD" : "--"}</span>
            <div>
              <b>{adminSession.probe.authenticated ? adminSession.probe.displayName || adminSession.probe.email || "admin" : "未登录后台"}</b>
              <small>
                {adminSession.status === "checking"
                  ? "正在确认后台会话…"
                  : adminSession.probe.authenticated
                    ? "后台会话 · 管理面可用"
                    : "后台页面需从 /admin/login 登录"}
              </small>
            </div>
          </div>
          <button
            className="icon-button collapse-trigger"
            aria-label={collapsed ? "展开侧栏" : "收起侧栏"}
            onClick={() => setCollapsed((value) => !value)}
          >
            {collapsed ? (
              <PanelLeftOpen size={17} />
            ) : (
              <PanelLeftClose size={17} />
            )}
          </button>
        </div>
      </aside>

      <main className="workspace-main" id="main-content" aria-label={routeName}>
        {children}
      </main>
    </div>
  );
}

/**
 * 后台路由闸门（R-3 导航修复）。
 *
 * 没有 admin 受众会话时：页面仍按原样渲染（这样"服务端是否拒绝"依然可被真实观测），
 * 但**叠加一个阻断式弹窗**并给出通往后台入口的路径——不再让用户面对一个
 * 静默报错的后台页面。授权判定完全不受影响：服务端对 admin:* 接口依旧
 * 只放行 admin 受众会话。
 */
function BackendRoute({ children }: { children: React.ReactNode }) {
  const adminSession = useAdminSession();
  const location = useLocation();
  if (adminSession.status === "checking") {
    return (
      <div className="workspace-page">
        <div className="workspace-skeleton">
          <i />
          <i />
          <i />
        </div>
      </div>
    );
  }
  if (adminSession.status === "anonymous") {
    return (
      <>
        {children}
        <BackendLoginDialog returnTo={location.pathname + location.search} />
      </>
    );
  }
  return <>{children}</>;
}

function RouteContent() {
  return (
    <Suspense
      fallback={
        <div className="workspace-page">
          <div className="workspace-skeleton">
            <i />
            <i />
            <i />
          </div>
        </div>
      }
    >
      <Routes>
        <Route path="/" element={<DownloadPage />} />
        <Route path="/jobs" element={<JobsPage />} />
        <Route path="/jobs/:id" element={<JobDetailPage />} />
        <Route
          path="/system"
          element={
            <BackendRoute>
              <SystemPage />
            </BackendRoute>
          }
        />
        <Route
          path="/api-control"
          element={
            <BackendRoute>
              <ApiControlPage />
            </BackendRoute>
          }
        />
        <Route path="/investigations" element={<InvestigationsPage />} />
        <Route path="/fund-flow" element={<FundFlowPage />} />
        <Route
          path="/browse/transactions"
          element={<BrowsePage dataset="transactions" />}
        />
        <Route
          path="/browse/token_transfers"
          element={<BrowsePage dataset="token_transfers" />}
        />
        <Route
          path="/browse/nft_transfers"
          element={<BrowsePage dataset="nft_transfers" />}
        />
        <Route path="/browse/logs" element={<BrowsePage dataset="logs" />} />
        <Route path="/data-explorer" element={<DataExplorerPage />} />
        <Route path="/data-explorer/combined" element={<CombinedDataPage />} />
        <Route
          path="/data-explorer/:chain/:address"
          element={<AddressDetailPage />}
        />
      </Routes>
    </Suspense>
  );
}

export default function App() {
  return (
    <AdminSessionProvider>
      <WorkspaceShell>
        <RouteContent />
      </WorkspaceShell>
    </AdminSessionProvider>
  );
}
