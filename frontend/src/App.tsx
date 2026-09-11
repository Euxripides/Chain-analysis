import { lazy, Suspense, useState } from "react";
import { NavLink, Route, Routes, useLocation } from "react-router-dom";
import {
  Bell,
  ChevronDown,
  Download,
  Gauge,
  ListChecks,
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

const navGroups = [
  { label: "", items: [{ to: "/system", label: "总览", Icon: Gauge }] },
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
      { to: "/api-control", label: "API 控制中心", Icon: SlidersHorizontal },
    ],
  },
  {
    label: "系统管理",
    items: [{ to: "/system?view=settings", label: "设置中心", Icon: Settings }],
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
          <button className="icon-button notification-button" aria-label="通知">
            <Bell size={17} />
            <em>3</em>
          </button>
          <button className="avatar-button" aria-label="打开用户菜单">
            AD
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
                  title={collapsed ? item.label : undefined}
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
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="workspace-sidebar-foot">
          <div className="workspace-user">
            <span>AD</span>
            <div>
              <b>admin</b>
              <small>调查工作区</small>
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
        <Route path="/system" element={<SystemPage />} />
        <Route path="/api-control" element={<ApiControlPage />} />
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
    <WorkspaceShell>
      <RouteContent />
    </WorkspaceShell>
  );
}
