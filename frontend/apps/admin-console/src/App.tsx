import React, { Suspense, lazy, useCallback, useEffect, useState } from "react";
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
import { ApiError, AuthProvider, useAuth, type WorkspaceOption } from "@chain/api-client";
import { s6ProviderRoutes, s6ProviderNavGroup } from "./routes/provider.routes";
import { s6GovernanceRoutes, s6GovernanceNavGroups } from "./routes/governance.routes";
import { s6ControlRoutes, s6ControlNavGroups } from "./routes/control.routes";
import { sanitizeReturnTo } from "./lib/returnTo";

const LoginPage = lazy(() => import("./pages/LoginPage"));
const OverviewPage = lazy(() => import("./pages/OverviewPage"));
const UsersPage = lazy(() => import("./pages/UsersPage"));
const RolesPage = lazy(() => import("./pages/RolesPage"));
const SessionsPage = lazy(() => import("./pages/SessionsPage"));
const LoginEventsPage = lazy(() => import("./pages/LoginEventsPage"));
const AuditPage = lazy(() => import("./pages/AuditPage"));
const AuditIntegrityPage = lazy(() => import("./pages/AuditIntegrityPage"));
const OrganizationsPage = lazy(() => import("./pages/OrganizationsPage"));
const SystemHealthPage = lazy(() => import("./pages/SystemHealthPage"));
const JobsPage = lazy(() => import("./pages/JobsPage"));
const RiskPage = lazy(() => import("./pages/RiskPage"));
const StoragePage = lazy(() => import("./pages/StoragePage"));
const DataQualityPage = lazy(() => import("./pages/DataQualityPage"));
const JobDetailPage = lazy(() => import("./pages/JobDetailPage"));
const SecurityPage = lazy(() => import("./pages/SecurityPage"));

interface AdminRouteEntry {
  path: string;
  permission: string;
  element: React.ReactNode;
}

/**
 * 非 Sprint 6 的既有后台路由。
 *
 * 为什么要有这张表（§7.5「每个角色只看到允许的导航和按钮」）：
 * 导航项的权限不再各自手写一份，而是**从路由表反查**——路由是唯一事实来源，
 * 因此「页面能被打开」与「入口会出现」不可能各自漂移。
 */
const coreRoutes: AdminRouteEntry[] = [
  { path: "/overview", permission: "admin:dashboard:read", element: <OverviewPage /> },
  { path: "/system", permission: "admin:system:read", element: <SystemHealthPage /> },
  { path: "/organizations", permission: "admin:org:read", element: <OrganizationsPage /> },
  { path: "/users", permission: "admin:user:read", element: <UsersPage /> },
  { path: "/roles", permission: "admin:role:read", element: <RolesPage /> },
  { path: "/sessions", permission: "admin:session:read", element: <SessionsPage /> },
  { path: "/login-events", permission: "admin:login-event:read", element: <LoginEventsPage /> },
  { path: "/audit", permission: "admin:audit:read", element: <AuditPage /> },
  { path: "/audit/integrity", permission: "admin:audit:verify", element: <AuditIntegrityPage /> },
  { path: "/jobs", permission: "admin:job:list", element: <JobsPage /> },
  { path: "/jobs/:jobId", permission: "admin:job:read", element: <JobDetailPage /> },
  { path: "/storage", permission: "admin:storage:read", element: <StoragePage /> },
  { path: "/data-quality", permission: "admin:coverage:read", element: <DataQualityPage /> },
  { path: "/risk", permission: "admin:risk:read", element: <RiskPage /> },
  // 自助页面（决策 D7）：入口权限取 self 类的 security:update。
  // 为什么不是 admin:*：这一页只操作**调用者本人**的凭据与会话，管理员与普通用户同样适用；
  // 迁移 0071 已把该键授予全部系统角色（此前 platform_admin/security_admin/auditor 都没有任何
  // self 类权限，会连自己的 MFA 都绑不了）。
  { path: "/account/security", permission: "security:update", element: <SecurityPage /> },
];

// Sprint 6 路由由各领域文件提供（provider / governance / control），与既有路由合成同一张表。
const s6Routes = [...s6ProviderRoutes, ...s6GovernanceRoutes, ...s6ControlRoutes];
const adminRoutes: AdminRouteEntry[] = [...coreRoutes, ...s6Routes];

/** nav 项的权限：按路径从路由表反查（nav 与 Guard 同源）。 */
const permissionForNavPath = (to: string): string | undefined =>
  adminRoutes.find((route) => route.path === to)?.permission;

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

/**
 * §3.2 / §7.5 后台导航：Sprint 6 在原有分组上追加控制面入口，并按**当前会话的权限**裁剪。
 *
 * 规则（不依赖任何前端猜测）：
 *   - 每个入口的权限来自路由表（permissionForNavPath），与页面上的 <Guard> 同源；
 *   - 会话没有该权限 -> 不渲染该入口（服务端仍然是权威判据，这里只是不给出走不通的入口）；
 *   - 反查不到权限的入口一律**不显示**（fail-closed）：一个没有权限声明的入口在
 *     §7.5 下就是缺陷，宁可少显示，也不能让角色看到自己打不开的入口。
 */
const navGroups = (can: (permission: string) => boolean): NavGroup[] => {
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
        { to: "/audit/integrity", label: "完整性边界", icon: <ShieldCheck size={16} /> },
        { to: "/alerts", label: "告警中心", icon: <AlertTriangle size={16} /> },
        { to: "/risk", label: "风险事件", icon: <AlertTriangle size={16} /> },
        { to: "/settings", label: "系统配置", icon: <SlidersHorizontal size={16} /> },
        { to: "/feature-flags", label: "功能开关", icon: <ToggleLeft size={16} /> },
      ],
    },
    {
      label: "我的账号",
      items: [{ to: "/account/security", label: "账号安全", icon: <ShieldCheck size={16} /> }],
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
  // §7.5：按会话权限裁剪入口（权限来自路由表；查不到声明的入口不显示）。
  for (const group of groups) {
    group.items = group.items.filter((item) => {
      const permission = permissionForNavPath(item.to);
      return Boolean(permission) && can(permission as string);
    });
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
  const { session, loading, logout, can } = useAuth();
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
          <Route
            path="*"
            element={
              <Navigate
                to={{ pathname: "/login", search: location.search }}
                replace
                state={{ from: location.pathname + location.search }}
              />
            }
          />
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
      navGroups={navGroups(can)}
      user={{ displayName: session.display_name, email: session.email, roles: session.roles }}
      headerExtra={<WorkspaceSwitcher />}
      onLogout={async () => {
        await logout();
        navigate("/login", { replace: true });
      }}
    >
      <Suspense fallback={<SkeletonRows rows={6} columns={4} />}>
        <Routes>
          <Route path="/" element={<Navigate to="/overview" replace />} />
          <Route path="/login" element={<LoginLanding />} />
          {/* 路由与导航同源（adminRoutes / coreRoutes），避免"页面能开但入口没有"或反过来。 */}
          {adminRoutes.map((entry) => (
            <Route
              key={entry.path}
              path={entry.path}
              element={<Guard permission={entry.permission}>{entry.element}</Guard>}
            />
          ))}
          <Route path="*" element={<Placeholder title="页面不存在" />} />
        </Routes>
      </Suspense>
    </AppShell>
  );
}

/**
 * 登录后的工作区切换器（后台受众；与用户层 `user-console/src/App.tsx` 的
 * WorkspaceSwitcher 行为一致，data-testid 相同，供同一套浏览器闸门覆盖）。
 *
 * 后台会话同样绑定组织/工作区（§6.3）：运维在同一组织内切换工作区时，
 * 必须走**服务端成员资格判定**并留下审计，而不是由前端改一个本地状态。
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
  const label = current ? `${current.name}（${current.slug}）` : currentId === "" ? "无绑定工作区" : `${currentId.slice(0, 8)}…`;

  const open = useCallback(async () => {
    setBusy(true);
    try {
      const list = await workspaceOptions();
      setOptions(list);
      if (list.length <= 1) {
        dialog.notify("info", "没有可切换的工作区", "当前后台账号在当前组织内只有一个有效工作区成员资格，无法切换。");
        return;
      }
      const picked = await dialog.confirm({
        kind: "info",
        title: "切换工作区",
        body: "切换后后台页面会按新工作区重新加载。该操作由服务端按成员资格判定并写入审计。",
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
      dialog.notify("success", "已切换工作区", "后台页面数据已按新工作区重新加载。");
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
      data-audience="admin"
      aria-label="切换工作区"
      onClick={() => void open()}
      disabled={busy}
    >
      {busy ? "切换中…" : label}
    </button>
  );
}

/**
 * 已持有后台会话时访问 /login 的落地逻辑（R-3）。
 *
 * 旧 V1 后台页面把用户送到 `/admin/login?return_to=<旧页面>`；如果用户其实**已经**登录，
 * 这里不能再无条件跳 `/overview`，否则"回到原页面"会失败。合法 return_to 一律用
 * **真实浏览器跳转**（`window.location.assign`）回到旧 SPA 的绝对路径。
 */
function LoginLanding() {
  const location = useLocation();
  const returnTo = sanitizeReturnTo(new URLSearchParams(location.search).get("return_to"));

  React.useEffect(() => {
    if (returnTo) window.location.assign(returnTo);
  }, [returnTo]);

  if (returnTo) {
    return (
      <div className="shell__content">
        <PageHeader title="正在返回原页面" subtitle={`即将跳转到 ${returnTo}`} />
        <SkeletonRows rows={3} columns={2} />
      </div>
    );
  }
  return <Navigate to="/overview" replace />;
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
