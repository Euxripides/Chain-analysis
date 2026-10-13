import React, { useCallback, useEffect, useRef, useState } from "react";
import {
  Bell,
  ChevronLeft,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  ShieldCheck,
} from "lucide-react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import { useDialog } from "./Dialog";
import { cx } from "./layout";

/**
 * §17.3 AppShell：左侧模块导航 + 顶部环境/组织上下文 + 主工作区。
 *
 * 2026-09 用户端重构（蓝图 §5.1 / §5.2 / §17）：
 *   新增可选 `variant="user"`，走 `.cs-shell__*` 类与用户端样式表；
 *   **不传 variant 时行为与类名与重构前完全一致**（后台端不受影响）。
 *
 * 语义保持不变：`navGroups`、`user`、`onLogout`、`headerExtra` 的含义与调用方式
 * 都不变，只是 user variant 多渲染了几个可选插槽（通知、命令面板入口、用户菜单）。
 */

export interface NavItem {
  to: string;
  label: string;
  icon?: React.ReactNode;
  badge?: number;
  activePrefixes?: string[];
}

export interface NavGroup {
  label: string;
  items: NavItem[];
  collapsible?: boolean;
}

export interface ShellUser {
  displayName: string;
  email: string;
  roles: string[];
}

export interface AppShellProps {
  productName: string;
  productTag: string;
  brandMark: string;
  env: string;
  navGroups: NavGroup[];
  user: ShellUser | null;
  headerExtra?: React.ReactNode;
  onLogout?: () => Promise<void> | void;
  children: React.ReactNode;
  /** "admin"（默认，保持原样）/ "user"（用户端工作台）。 */
  variant?: "admin" | "user";
  /** user variant 专用：顶栏通知入口。 */
  notifications?: React.ReactNode;
  /** user variant 专用：命令面板触发入口（替代 GlobalSearchBox 的视觉位置）。 */
  commandTrigger?: React.ReactNode;
  /** user variant 专用：移动端顶栏显示的当前页面短标题。 */
  mobileTitle?: string;
  /** user variant 专用：导航底部附加内容（例如权限范围说明）。 */
  railFooter?: React.ReactNode;
  /** 用户菜单里的额外条目（退出登录之前渲染）。 */
  userMenuExtra?: React.ReactNode;
}

export function AppShell({
  productName,
  productTag,
  brandMark,
  env,
  navGroups,
  user,
  headerExtra,
  onLogout,
  children,
  variant = "admin",
  notifications,
  commandTrigger,
  mobileTitle,
  railFooter,
  userMenuExtra,
}: AppShellProps) {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const dialog = useDialog();
  const navigate = useNavigate();
  const isUser = variant === "user";

  const confirmLogout = useCallback(async () => {
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "退出登录？",
      body: "当前会话将立即失效，未保存的筛选条件会丢失。",
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "退出登录", result: "logout", variant: "danger" },
      ],
    });
    if (answer !== "logout") return;
    try {
      await onLogout?.();
      navigate("/login", { replace: true });
    } catch (err) {
      dialog.notify("error", "退出失败", err instanceof Error ? err.message : "请稍后重试");
    }
  }, [dialog, navigate, onLogout]);

  if (isUser) {
    return (
      <UserShellLayout
        productName={productName}
        productTag={productTag}
        brandMark={brandMark}
        navGroups={navGroups}
        user={user}
        headerExtra={headerExtra}
        notifications={notifications}
        commandTrigger={commandTrigger}
        mobileTitle={mobileTitle}
        railFooter={railFooter}
        userMenuExtra={userMenuExtra}
        collapsed={collapsed}
        setCollapsed={setCollapsed}
        mobileOpen={mobileOpen}
        setMobileOpen={setMobileOpen}
        onLogout={confirmLogout}
      >
        {children}
      </UserShellLayout>
    );
  }

  return (
    <div className="shell" data-collapsed={collapsed}>
      {mobileOpen ? <div className="shell__scrim" onClick={() => setMobileOpen(false)} role="presentation" /> : null}
      <nav className="shell__nav" data-open={mobileOpen} aria-label="主导航">
        <div className="shell__brand">
          <span className="shell__brand-mark" aria-hidden="true">
            {brandMark}
          </span>
          {!collapsed ? (
            <span>
              <span className="shell__brand-name" style={{ display: "block" }}>
                {productName}
              </span>
              <span className="shell__brand-tag">{productTag}</span>
            </span>
          ) : null}
        </div>
        {navGroups.map((group, index) => (
          <div className="shell__group" key={group.label || index}>
            {group.label && !collapsed ? <span className="shell__group-label">{group.label}</span> : null}
            {group.items.map((item) => (
              /* 必须用 NavLink 的 className 函数形式。曾经的写法是 {...markActive}，
                 而 markActive 是一个函数——展开函数对象得不到任何属性，className
                 实际为空字符串，导航链接退化成全局 a{color:cobalt}，在深色侧栏上
                 对比度只有 2.96:1，且丢失 hover / 选中态样式。
                 NavLink 会自动为当前路由设置 aria-current="page"，无需手写。 */
              <NavLink
                key={item.to}
                to={item.to}
                end={item.to === "/"}
                title={item.label}
                className={({ isActive }) => `shell__link${isActive ? " is-active" : ""}`}
              >
                {item.icon}
                {!collapsed ? <span>{item.label}</span> : null}
                {!collapsed && item.badge ? <span className="badge badge--info" style={{ marginLeft: "auto" }}>{item.badge}</span> : null}
              </NavLink>
            ))}
          </div>
        ))}
        <div style={{ marginTop: "auto" }} />
        <button
          type="button"
          className="shell__link"
          style={{ border: 0, background: "transparent", cursor: "pointer", width: "100%" }}
          onClick={() => setCollapsed((v) => !v)}
        >
          {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
          {!collapsed ? <span>收起导航</span> : null}
        </button>
      </nav>

      <div className="shell__main">
        <header className="shell__top">
          <button
            type="button"
            className="btn btn--ghost btn--sm shell__mobile-toggle"
            aria-label="打开主导航"
            onClick={() => setMobileOpen(true)}
          >
            <Menu size={18} />
          </button>
          <span className="shell__env">{env}</span>
          {headerExtra}
          <span className="shell__spacer" />
          <span className="shell__user">
            <Bell size={16} aria-hidden="true" />
            {user ? (
              <>
                <span className="shell__avatar" aria-hidden="true">
                  {user.displayName.slice(0, 1).toUpperCase()}
                </span>
                <span>
                  <span style={{ display: "block", color: "var(--c-graphite)", fontWeight: 600 }}>{user.displayName}</span>
                  <span style={{ fontSize: "var(--fs-11)" }}>{describeRoles(user.roles)}</span>
                </span>
              </>
            ) : (
              <span>未登录</span>
            )}
          </span>
          {user ? (
            <button type="button" className="btn btn--ghost btn--sm" onClick={() => void confirmLogout()}>
              <LogOut size={15} />
              退出
            </button>
          ) : (
            <NavLink to="/login" className="btn btn--sm">
              登录
            </NavLink>
          )}
        </header>
        <main className="shell__content" id="main-content">
          {children}
        </main>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------------- */
/* 用户端 shell（蓝图 §5.1 桌面 / §5.2 移动）                                  */
/* ------------------------------------------------------------------------- */

function UserShellLayout({
  productName,
  productTag,
  brandMark,
  navGroups,
  user,
  headerExtra,
  notifications,
  commandTrigger,
  mobileTitle,
  railFooter,
  userMenuExtra,
  collapsed,
  setCollapsed,
  mobileOpen,
  setMobileOpen,
  onLogout,
  children,
}: {
  productName: string;
  productTag: string;
  brandMark: string;
  navGroups: NavGroup[];
  user: ShellUser | null;
  headerExtra?: React.ReactNode;
  notifications?: React.ReactNode;
  commandTrigger?: React.ReactNode;
  mobileTitle?: string;
  railFooter?: React.ReactNode;
  userMenuExtra?: React.ReactNode;
  collapsed: boolean;
  setCollapsed: React.Dispatch<React.SetStateAction<boolean>>;
  mobileOpen: boolean;
  setMobileOpen: React.Dispatch<React.SetStateAction<boolean>>;
  onLogout: () => Promise<void>;
  children: React.ReactNode;
}) {
  const location = useLocation();
  const railRef = useRef<HTMLElement>(null);
  const menuButtonRef = useRef<HTMLButtonElement>(null);
  const [scrolled, setScrolled] = useState(false);
  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  /* 路由跳转后自动关闭抽屉（蓝图 §5.2）。 */
  useEffect(() => {
    setMobileOpen(false);
    setMenuOpen(false);
  }, [location.pathname, setMobileOpen]);

  /* 抽屉打开时把焦点送进抽屉，关闭后回到菜单按钮（蓝图 §5.2）。 */
  useEffect(() => {
    if (!mobileOpen) return;
    const node = railRef.current;
    const previous = document.activeElement;
    node?.querySelector<HTMLElement>("a, button")?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.stopPropagation();
        setMobileOpen(false);
      }
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => {
      document.removeEventListener("keydown", onKeyDown, true);
      if (previous instanceof HTMLElement && document.contains(previous)) previous.focus();
      else menuButtonRef.current?.focus();
    };
  }, [mobileOpen, setMobileOpen]);

  /* 用户菜单：点击外部与 Escape 关闭，关闭后焦点回到触发器。 */
  useEffect(() => {
    if (!menuOpen) return;
    const onDocDown = (event: MouseEvent) => {
      if (!menuRef.current?.contains(event.target as Node)) setMenuOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("mousedown", onDocDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onDocDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuOpen]);

  /* 顶栏滚动后才加阴影；滚动容器是窗口（body 自身滚动）。 */
  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 4);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <div className="cs-shell" data-collapsed={collapsed}>
      {mobileOpen ? (
        <button
          type="button"
          className="cs-shell__scrim"
          aria-label="关闭主导航"
          onClick={() => setMobileOpen(false)}
        />
      ) : null}

      <nav
        className="cs-shell__rail"
        ref={railRef}
        data-open={mobileOpen}
        data-collapsed={collapsed}
        aria-label="主导航"
      >
        <div className="cs-brand">
          <span className="cs-brand__mark" aria-hidden="true">
            {brandMark}
          </span>
          {!collapsed ? (
            <span className="cs-brand__text">
              <span className="cs-brand__name">{productName}</span>
              <span className="cs-brand__tag">{productTag}</span>
            </span>
          ) : null}
        </div>

        <div className="cs-nav">
          {navGroups.map((group, index) => {
            const links = <>{group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  end={item.to === "/"}
                  title={item.label}
                  className={({ isActive }) => `cs-nav__link${isActive || item.activePrefixes?.some((prefix) => location.pathname === prefix || location.pathname.startsWith(prefix + "/")) ? " active" : ""}`}
                >
                  <span className="cs-nav__icon" aria-hidden="true">
                    {item.icon}
                  </span>
                  {!collapsed ? <span className="cs-nav__text">{item.label}</span> : null}
                  {!collapsed && item.badge ? (
                    <span className="badge badge--info cs-nav__badge">{item.badge}</span>
                  ) : null}
                  {/* 收起态没有文字，用 aria-label 补足可访问名称（图标本身不可读）。 */}
                  {collapsed ? <span className="visually-hidden">{item.label}</span> : null}
                </NavLink>
              ))}</>;
            return group.collapsible && !collapsed ? (
              <details className="cs-nav__group cs-nav__more" key={group.label || index} open={group.items.some((item) => location.pathname === item.to || location.pathname.startsWith(item.to + "/")) || undefined}>
                <summary className="cs-nav__label">{group.label}</summary>
                {links}
              </details>
            ) : (
              <div className="cs-nav__group" key={group.label || index}>
                {group.label && !group.collapsible ? <span className="cs-nav__label">{group.label}</span> : null}
                {links}
              </div>
            );
          })}
        </div>

        <div className="cs-railfoot">
          {railFooter}
          <button
            type="button"
            className="cs-railfoot__collapse"
            aria-label={collapsed ? "展开导航" : "收起导航"}
            aria-expanded={!collapsed}
            title={collapsed ? "展开导航" : "收起导航"}
            onClick={() => setCollapsed((v) => !v)}
          >
            {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
            {!collapsed ? <span>收起导航</span> : null}
          </button>
        </div>
      </nav>

      <div className="cs-shell__main">
        <header className="cs-shell__topbar" data-scrolled={scrolled ? "true" : "false"}>
          <button
            type="button"
            className="cs-iconbtn"
            ref={menuButtonRef}
            aria-label="打开主导航"
            aria-expanded={mobileOpen}
            onClick={() => setMobileOpen(true)}
          >
            <Menu size={19} />
          </button>
          {mobileTitle ? <span className="cs-shell__topbar-title">{mobileTitle}</span> : null}
          <span className="cs-shell__spacer" />
          {commandTrigger}
          {headerExtra}
          {notifications}
          <div className="cs-usermenu" ref={menuRef}>
            <button
              type="button"
              className="cs-usermenu__trigger"
              aria-haspopup="menu"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen((v) => !v)}
            >
              <span className="cs-avatar" aria-hidden="true">
                {(user?.displayName ?? "?").slice(0, 1).toUpperCase()}
              </span>
              {user ? (
                <span className="cs-usermenu__name">
                  <b>{user.displayName}</b>
                  <span>{describeRoles(user.roles)}</span>
                </span>
              ) : (
                <span className="cs-usermenu__name">
                  <b>未登录</b>
                </span>
              )}
            </button>
            {menuOpen ? (
              <div className="cs-popover" role="menu" style={{ width: 280, right: 0 }}>
                <div className="cs-popover__head" style={{ flexDirection: "column", alignItems: "flex-start", gap: 2 }}>
                  <span className="cs-popover__title">{user?.displayName ?? "未登录"}</span>
                  {user?.email ? <span className="cs-popover__itemsub">{user.email}</span> : null}
                </div>
                <div style={{ padding: "var(--sp-3)", display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
                  {userMenuExtra}
                  <button type="button" className="btn btn--sm" role="menuitem" onClick={() => void onLogout()}>
                    <LogOut size={15} />
                    退出登录
                  </button>
                </div>
              </div>
            ) : null}
          </div>
        </header>

        <main className="cs-shell__content" id="main-content">
          {children}
        </main>
      </div>
    </div>
  );
}

/**
 * 角色文案。
 *
 * ShellUser.roles 由会话归一化（AuthProvider）保证是数组；这里仍做无条件兜底：
 * 后端曾把"该身份没有任何角色绑定"的 roles 序列化成 JSON null，任何一处直接
 * join 都会在渲染期抛 TypeError，React 卸载整棵树，整个应用只剩空白的 #root。
 * Shell 是最后一层，绝不假设上游一定干净：非数组按空集合处理，非字符串项忽略。
 */
function describeRoles(roles: unknown): string {
  if (!Array.isArray(roles)) return "no-role";
  return roles.filter((role): role is string => typeof role === "string").join(" / ") || "no-role";
}

/** 全局搜索框只是一个受控输入；实际检索由页面接管（禁止在 shell 里伪造结果）。 */
export function GlobalSearchBox({
  onSearch,
  placeholder,
}: {
  onSearch: (q: string) => void;
  placeholder?: string;
}) {
  const [value, setValue] = useState("");
  return (
    <form
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        if (value.trim().length >= 2) onSearch(value.trim());
      }}
      style={{ display: "flex", alignItems: "center", gap: "var(--sp-2)", maxWidth: 420, flex: 1 }}
    >
      <span className="input" style={{ display: "flex", alignItems: "center", gap: 8, padding: "6px 10px" }}>
        <Search size={15} aria-hidden="true" />
        <input
          aria-label="全局搜索"
          name="q"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder={placeholder ?? "搜索案件 / 调查 / 地址 / 交易…"}
          style={{ border: 0, outline: "none", background: "transparent", width: "100%", font: "inherit" }}
        />
      </span>
    </form>
  );
}

export function BackLink({ to, label }: { to: string; label: string }) {
  return (
    <NavLink to={to} className={cx("btn", "btn--ghost", "btn--sm")} style={{ alignSelf: "flex-start" }}>
      <ChevronLeft size={15} />
      {label}
    </NavLink>
  );
}

export function RoleScopeNote() {
  return <ShieldCheck size={14} aria-hidden="true" />;
}
