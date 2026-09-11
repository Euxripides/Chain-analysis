import React, { useState } from "react";
import { Bell, ChevronLeft, LogOut, Menu, PanelLeftClose, PanelLeftOpen, Search, ShieldCheck } from "lucide-react";
import { NavLink, useNavigate } from "react-router-dom";
import { useDialog } from "./Dialog";

/** §17.3 AppShell：左侧模块导航 + 顶部环境/组织上下文 + 主工作区。 */

export interface NavItem {
  to: string;
  label: string;
  icon?: React.ReactNode;
  badge?: number;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
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
}: AppShellProps) {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const dialog = useDialog();
  const navigate = useNavigate();

  const markActive = ({ isActive }: { isActive: boolean }) => ({
    className: "shell__link",
    "aria-current": isActive ? ("page" as const) : undefined,
  });

  const confirmLogout = async () => {
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
  };

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
              <NavLink key={item.to} to={item.to} end={item.to === "/"} {...markActive} title={item.label}>
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
                  <span style={{ fontSize: "var(--fs-11)" }}>{user.roles.join(" / ") || "no-role"}</span>
                </span>
              </>
            ) : (
              <span>未登录</span>
            )}
          </span>
          {user ? (
            <button type="button" className="btn btn--ghost btn--sm" onClick={confirmLogout}>
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
    <NavLink to={to} className="btn btn--ghost btn--sm" style={{ alignSelf: "flex-start" }}>
      <ChevronLeft size={15} />
      {label}
    </NavLink>
  );
}

export function RoleScopeNote() {
  return <ShieldCheck size={14} aria-hidden="true" />;
}
