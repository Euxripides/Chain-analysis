import React, { Suspense, lazy } from "react";
import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "@chain/api-client";
import { EmptyState, ErrorState } from "@chain/ui";
import { CsSkeleton } from "@chain/user-ui";
import { UserShell } from "./app/UserShell";
import { ROUTE_PERMISSIONS } from "./app/navigation";
import { CaseTabRoute } from "./app/CaseTabRoute";
import { routeMetaFor } from "./app/route-meta";
import { RoutedContent } from "./app/route-transitions";

/**
 * 用户端路由表（蓝图 §5.4）
 *
 * 本次重构**不改变任何路由路径、参数名、权限名或重定向行为**（§19.4）。
 * 变化只有两处表现层内容：
 *   1. Shell 换成 `UserShell`（AppShell 的 user variant + 命令面板 + 通知中心）；
 *   2. 路由懒加载的回退换成与目标页面形状一致的骨架（§11.9 / §23.4），
 *      形状与过渡策略统一由 `app/route-meta.ts` 决定（§9.3 / §61）。
 */

const LoginPage = lazy(() => import("./pages/LoginPage"));
const HomePage = lazy(() => import("./pages/HomePage"));
const CasesPage = lazy(() => import("./pages/CasesPage"));
const CaseNewPage = lazy(() => import("./pages/CaseNewPage"));
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

export function App() {
  return (
    <AuthProvider audience="user">
      <Shell />
    </AuthProvider>
  );
}

function Shell() {
  const { session, loading, sessionError, retrySession } = useAuth();
  const location = useLocation();

  /*
   * 会话引导失败（网关 5xx、网络中断等）。
   *
   * 此时**不能**直接把用户丢到登录页：那样等于告诉他"你没登录"，
   * 而真实原因是拿不到会话。这里给出可重试的真实错误状态（蓝图 §11.5 / §11.8）。
   */
  if (!loading && !session && sessionError) {
    return (
      <div className="cs-shell cs-shell--boot">
        <main className="cs-shell__content" id="main-content">
          <div className="card">
            <ErrorState
              title="无法确认会话状态"
              message={sessionError}
              onRetry={() => {
                void retrySession();
              }}
            />
          </div>
        </main>
      </div>
    );
  }

  if (loading) {
    /* 会话校验期间不能提前渲染 Shell：`loading` 结束时整棵树会重新挂载一次。 */
    return (
      <div className="cs-shell cs-shell--boot">
        <main className="cs-shell__content" id="main-content">
          <CsSkeleton shape="detail" />
        </main>
      </div>
    );
  }

  if (!session) {
    return (
      <Suspense fallback={<CsSkeleton shape="form" />}>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="*" element={<Navigate to="/login" replace state={{ from: location.pathname }} />} />
        </Routes>
      </Suspense>
    );
  }

  /*
   * 页面骨架与过渡策略来自同一张路由元信息表（§9.3 / §23.4 / §61）：
   * 懒加载回退的形状必须和目标页面一致，否则会先闪一个"表格骨架"再变成图谱。
   */
  const meta = routeMetaFor(location.pathname);

  return (
    <UserShell>
      <Suspense fallback={<CsSkeleton shape={meta.skeleton} />}>
        <RoutedContent pathname={location.pathname}>
        <Routes>
          <Route path="/" element={<Navigate to="/home" replace />} />
          <Route path="/login" element={<Navigate to="/home" replace />} />
          <Route path="/home" element={<Guard permission="dashboard:read"><HomePage /></Guard>} />
          <Route path="/search" element={<Guard permission="search:read"><SearchPage /></Guard>} />
          <Route path="/cases" element={<Guard permission="case:list"><CasesPage /></Guard>} />
          <Route path="/cases/new" element={<Guard permission="case:create"><CaseNewPage /></Guard>} />
          {/*
          案件详情的 7 个子 Tab 各有自己的权限（app/case-tabs.ts）。
          这里不再统一挂 case:read —— 那正是"UI 藏起来了但直达 URL 进得去"的成因。
          守卫在 CaseDetailPage 挂载之前完成判定，未授权时不渲染页面、不发任何请求。
        */}
        <Route path="/cases/:caseId/:tab" element={<CaseTabRoute />} />
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
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
        </RoutedContent>
      </Suspense>
    </UserShell>
  );
}

function Guard({ permission, children }: { permission?: string; children: React.ReactNode }) {
  const { can } = useAuth();
  if (permission && !can(permission)) {
    return (
      <div className="card">
        <NoPermission permission={permission} />
      </div>
    );
  }
  return <>{children}</>;
}

/**
 * 无权限状态。使用 `NoPermissionState` 语义（显示缺少的权限名称），
 * **不伪装成 404**（蓝图 §11.6）。
 */
function NoPermission({ permission }: { permission: string }) {
  const { can } = useAuth();
  const home = can("dashboard:read") ? "/home" : "/login";
  return (
    <div className="state">
      <div className="state__title">没有访问该功能的权限</div>
      <div className="state__body">
        当前角色未被授予所需权限（<span className="mono">{permission}</span>）。
        如需访问，请联系组织 Owner 调整角色绑定。
      </div>
      <Link className="btn btn--sm" to={home}>
        返回工作台
      </Link>
    </div>
  );
}

/**
 * 未知路由（蓝图 §12.26）
 *
 * 只说明"这个地址没有对应页面"，并给出返回工作台的入口。
 * **不**把不存在的路由描述成"已在后续 Sprint 排期"，也不提供已下线的旧控制台链接。
 */
function NotFoundPage() {
  return (
    <div className="card">
      <EmptyState
        title="页面不存在"
        body="当前地址没有对应的页面。可能是链接已过期，或对象标识被改动过。"
        action={
          <Link className="btn btn--sm" to="/home">
            返回工作台
          </Link>
        }
      />
    </div>
  );
}

export { ROUTE_PERMISSIONS };
