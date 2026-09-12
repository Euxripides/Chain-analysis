import React, { Suspense, lazy } from "react";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import {
  Activity,
  AlertTriangle,
  Building2,
  Coins,
  Database,
  FileSearch,
  FlaskConical,
  Gauge,
  HardDrive,
  KeyRound,
  LifeBuoy,
  ListChecks,
  ScrollText,
  Server,
  ShieldCheck,
  SlidersHorizontal,
  ToggleLeft,
  Users,
} from "lucide-react";
import { AppShell, EmptyState, NoPermissionState, PageHeader, SkeletonRows, useDialog, type NavGroup } from "@chain/ui";
import { AuthProvider, useAuth } from "@chain/api-client";
import { s6ProviderRoutes, s6ProviderNavGroup } from "./routes/provider.routes";
import { s6GovernanceRoutes, s6GovernanceNavGroups } from "./routes/governance.routes";
import { s6ControlRoutes, s6ControlNavGroups } from "./routes/control.routes";

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

// Sprint 6 路由由各领域文件提供（provider / governance / control）。
const s6Routes = [...s6ProviderRoutes, ...s6GovernanceRoutes, ...s6ControlRoutes];

interface S6NavEntry {
  group: string;
  items: Array<{ to: string; label: string; icon?: React.ReactNode }>;
}

// Sprint 6 导航补充：各领域文件导出 { group, items:[{to,label}] }，这里补图标并按组名合并。
const s6IconFor = (to: string): React.ReactNode => {
  if (to.startsWith("/providers/routing")) return <SlidersHorizontal size={16} />;
  if (to.startsWith("/providers")) return <Server size={16} />;
  if (to.startsWith("/runtime/performance")) return <FlaskConical size={16} />;
  if (to.startsWith("/api-keys")) return <KeyRound size={16} />;
  if (to.startsWith("/requests")) return <FileSearch size={16} />;
  if (to.startsWith("/costs")) return <Coins size={16} />;
  if (to.startsWith("/budgets")) return <Coins size={16} />;
  if (to.startsWith("/alerts")) return <AlertTriangle size={16} />;
  if (to.startsWith("/feature-flags")) return <ToggleLeft size={16} />;
  if (to.startsWith("/settings")) return <SlidersHorizontal size={16} />;
  if (to.startsWith("/control/kill-switch")) return <LifeBuoy size={16} />;
  if (to.startsWith("/control")) return <LifeBuoy size={16} />;
  if (to.startsWith("/system/backups")) return <HardDrive size={16} />;
  if (to.startsWith("/data/retention")) return <Database size={16} />;
  if (to.startsWith("/system/version")) return <ScrollText size={16} />;
  if (to.startsWith("/observability")) return <Activity size={16} />;
  return <Gauge size={16} />;
};

const s6NavEntries: S6NavEntry[] = [
  s6ProviderNavGroup,
  ...s6GovernanceNavGroups,
  ...s6ControlNavGroups,
];

/** §3.2 后台导航（Sprint 6 在原有分组上追加控制面入口）。 */
const navGroups = (): NavGroup[] => {
  const groups: NavGroup[] = [
    {
      label: "运营总览",
      items: [
        { to: "/overview", label: "运营总览", icon: <Gauge size={16} /> },
        { to: "/observability", label: "可观测性", icon: <Activity size={16} /> },
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
        { to: "/login-events", label: "登录记录", icon: <KeyRound size={16} /> },
      ],
    },
    {
      label: "任务与数据",
      items: [
        { to: "/jobs", label: "全局任务控制", icon: <ListChecks size={16} /> },
        { to: "/storage", label: "导出与对象存储", icon: <HardDrive size={16} /> },
        { to: "/data-quality", label: "Coverage 与数据质量", icon: <Database size={16} /> },
        { to: "/data/retention", label: "保留与删除", icon: <Database size={16} /> },
      ],
    },
    {
      label: "Provider 与 API",
      items: [],
    },
    {
      label: "安全与审计",
      items: [
        { to: "/audit", label: "审计日志", icon: <ShieldCheck size={16} /> },
        { to: "/alerts", label: "告警中心", icon: <AlertTriangle size={16} /> },
        { to: "/risk", label: "风险事件", icon: <AlertTriangle size={16} /> },
        { to: "/settings", label: "系统配置", icon: <SlidersHorizontal size={16} /> },
        { to: "/feature-flags", label: "功能开关", icon: <ToggleLeft size={16} /> },
      ],
    },
    {
      label: "系统治理",
      items: [
        { to: "/control", label: "实时控制面", icon: <LifeBuoy size={16} /> },
        { to: "/control/kill-switch", label: "Kill Switch", icon: <LifeBuoy size={16} /> },
        { to: "/system/backups", label: "备份与恢复", icon: <HardDrive size={16} /> },
        { to: "/system/version", label: "版本与迁移", icon: <ScrollText size={16} /> },
      ],
    },
  ];
  const known = new Set(groups.flatMap((group) => group.items.map((item) => item.to)));
  for (const entry of s6NavEntries) {
    const target = groups.find((group) => group.label === entry.group);
    if (!target) continue;
    for (const item of entry.items) {
      if (known.has(item.to)) continue;
      known.add(item.to);
      target.items.push({ to: item.to, label: item.label, icon: s6IconFor(item.to) });
    }
  }
  return groups.filter((group) => group.items.length > 0);
};

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
          {s6Routes.map((entry) => (
            <Route
              key={entry.path}
              path={entry.path}
              element={<Guard permission={entry.permission}>{entry.element}</Guard>}
            />
          ))}
          <Route path="/storage" element={<Placeholder permission="admin:storage:read" title="导出与对象存储" sprint="S7" />} />
          <Route path="/data-quality" element={<Placeholder permission="admin:coverage:read" title="Coverage 与数据质量" sprint="S7" />} />
          <Route path="/risk" element={<Placeholder permission="admin:risk:read" title="风险事件" sprint="S7" />} />
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
        title={title + " · NOT_IMPLEMENTED"}
        body={"该模块计划在 " + (sprint ?? "后续") + " Sprint 交付。当前版本不提供入口，避免以占位数据冒充真实控制能力。"}
        action={
          <button
            type="button"
            className="btn btn--sm"
            onClick={() =>
              dialog.notify("info", title + " 的能力边界", null, [
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
