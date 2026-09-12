import React, { Suspense, lazy } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  Building2,
  Database,
  FileSearch,
  Gauge,
  HardDrive,
  KeyRound,
  ListChecks,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  Users,
} from "lucide-react";
import { AppShell, EmptyState, NoPermissionState, PageHeader, SkeletonRows, useDialog, type NavGroup } from "@chain/ui";
import { AuthProvider, useAuth } from "@chain/api-client";

const LoginPage = lazy(() => import("./pages/LoginPage"));
const OverviewPage = lazy(() => import("./pages/OverviewPage"));
const UsersPage = lazy(() => import("./pages/UsersPage"));
const RolesPage = lazy(() => import("./pages/RolesPage"));
const SessionsPage = lazy(() => import("./pages/SessionsPage"));
const LoginEventsPage = lazy(() => import("./pages/LoginEventsPage"));
const AuditPage = lazy(() => import("./pages/AuditPage"));
const OrganizationsPage = lazy(() => import("./pages/OrganizationsPage"));
const SystemHealthPage = lazy(() => import("./pages/SystemHealthPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));

/** §3.2 后台导航。 */
const navGroups = (): NavGroup[] => [
  {
    label: "运营总览",
    items: [
      { to: "/overview", label: "运营总览", icon: <Gauge size={16} /> },
      { to: "/system", label: "系统健康", icon: <Activity size={16} /> },
    ],
  },
  {
    label: "组织与权限",
    items: [
      { to: "/organizations", label: "组织与工作区", icon: <Building2 size={16} /> },
      { to: "/users", label: "用户与成员", icon: <Users size={16} /> },
      { to: "/roles", label: "角色与权限", icon: <ShieldCheck size={16} /> },
      { to: "/sessions", label: "登录与会话", icon: <KeyRound size={16} /> },
    ],
  },
  {
    label: "任务与数据",
    items: [
      { to: "/jobs", label: "全局任务控制", icon: <ListChecks size={16} /> },
      { to: "/storage", label: "导出与对象存储", icon: <HardDrive size={16} /> },
      { to: "/data-quality", label: "Coverage 与数据质量", icon: <Database size={16} /> },
    ],
  },
  {
    label: "Provider 与 API",
    items: [
      { to: "/provider", label: "Provider 账户", icon: <Server size={16} /> },
      { to: "/requests", label: "请求审计", icon: <FileSearch size={16} /> },
      { to: "/cost", label: "配额与成本", icon: <SlidersHorizontal size={16} /> },
    ],
  },
  {
    label: "安全与审计",
    items: [
      { to: "/audit", label: "审计日志", icon: <ShieldCheck size={16} /> },
      { to: "/risk", label: "风险事件", icon: <AlertTriangle size={16} /> },
    ],
  },
];

export function App() {
  return (
    <AuthProvider audience="admin">
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
        <PageHeader title="正在校验管理会话" subtitle="管理身份与用户身份使用不同的会话受众。" />
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
      productName="ChainSight Admin"
      productTag="后台管理系统 · admin"
      brandMark="AD"
      env={session.audience === "admin" ? "后台管理系统" : session.audience}
      navGroups={navGroups()}
      user={{ displayName: session.display_name, email: session.email, roles: session.roles }}
      onLogout={async () => {
        await logout();
        navigate("/login", { replace: true });
      }}
    >
      <Suspense fallback={<SkeletonRows rows={6} columns={4} />}>
        <Routes>
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/login" element={<Navigate to="/overview" replace />} />
          <Route path="/overview" element={<Guard permission="admin:dashboard:read"><OverviewPage /></Guard>} />
          <Route path="/system" element={<Guard permission="admin:system:read"><SystemHealthPage /></Guard>} />
          <Route path="/organizations" element={<Guard permission="admin:org:read"><OrganizationsPage /></Guard>} />
          <Route path="/users" element={<Guard permission="admin:user:read"><UsersPage /></Guard>} />
          <Route path="/roles" element={<Guard permission="admin:role:read"><RolesPage /></Guard>} />
          <Route path="/sessions" element={<Guard permission="admin:session:read"><SessionsPage /></Guard>} />
          <Route path="/login-events" element={<Guard permission="admin:login-event:read"><LoginEventsPage /></Guard>} />
          <Route path="/audit" element={<Guard permission="admin:audit:read"><AuditPage /></Guard>} />
          <Route path="/jobs" element={<Guard permission="admin:job:list"><JobsPage /></Guard>} />
          <Route path="/jobs/:jobId" element={<Guard permission="admin:job:read"><JobDetailPage /></Guard>} />
          <Route path="/storage" element={<Placeholder permission="admin:storage:read" title="导出与对象存储" sprint="S6" />} />
          <Route path="/data-quality" element={<Placeholder permission="admin:coverage:read" title="Coverage 与数据质量" sprint="S6" />} />
          <Route path="/provider" element={<Placeholder permission="admin:provider:read" title="Provider 账户" sprint="S6" />} />
          <Route path="/requests" element={<Placeholder permission="admin:request:read" title="请求审计" sprint="S6" />} />
          <Route path="/cost" element={<Placeholder permission="admin:cost:read" title="配额与成本" sprint="S6" />} />
          <Route path="/risk" element={<Placeholder permission="admin:risk:read" title="风险事件" sprint="S6" />} />
          <Route path="/alerts" element={<Placeholder permission="admin:alert:read" title="实时告警" sprint="S6" />} />
          <Route path="/backups" element={<Placeholder permission="admin:backup:read" title="备份恢复" sprint="S6" />} />
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

/** 未实现的后台能力必须显式标注，不能用假数据填充（§32 约束 10）。 */
function Placeholder({ title, permission, sprint }: { title: string; permission?: string; sprint?: string }) {
  const dialog = useDialog();
  return (
    <div className="card">
      <EmptyState
        title={`${title} · NOT_IMPLEMENTED`}
        body={<>该模块计划在 {sprint ?? "后续"} Sprint 交付。当前版本不提供入口，避免以占位数据冒充真实控制能力。</>}
        action={
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
        }
      />
    </div>
  );
}
