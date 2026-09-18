import React, { useCallback, useEffect, useState } from "react";

import { useLocation, useNavigate } from "react-router-dom";
import { Command, ShieldCheck } from "lucide-react";
import { AppShell } from "@chain/ui";
import { useAuth } from "@chain/api-client";
import { navGroups, pageTitleFor } from "./navigation";
import { WorkspaceSwitcher } from "../components/WorkspaceSwitcher";
import { NotificationPopover } from "../components/notifications/NotificationPopover";
import { NotificationsProvider } from "../components/notifications/useNotifications";
import { CommandPalette } from "../components/command/CommandPalette";
import { describeRoute, pushRecentObject } from "../components/command/recentObjects";

/**
 * 用户端 Shell（蓝图 §5 / §16）
 *
 * 组合：AppShell(variant="user") + 工作区切换 + 通知 Popover + 命令面板 + 最近访问登记。
 *
 * 路由、权限与 `navGroups` 的裁剪规则来自 `./navigation`，
 * 与重构前**完全一致**，服务端 Guard 仍是权威判据。
 */
export function UserShell({ children }: { children: React.ReactNode }) {
  const { session, logout } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const [commandOpen, setCommandOpen] = useState(false);

  /* Ctrl+K / Cmd+K 打开命令面板（§5.3）。输入框内也响应，因为它不替代任何现有操作。 */
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setCommandOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  /*
   * 页面不可见时降低非必要动画（蓝图 §15 / §9.3）。
   *
   * 只在 <html> 上打一个标记，由 CSS 暂停装饰性动画与过渡；
   * **不停止任何业务订阅**——SSE、轮询、React Query 继续按原频率运行，
   * 只是把渲染与动画压低，回到前台后立刻恢复。
   */
  useEffect(() => {
    const sync = () => {
      document.documentElement.dataset.pageVisible = document.visibilityState === "hidden" ? "false" : "true";
    };
    sync();
    document.addEventListener("visibilitychange", sync);
    return () => {
      document.removeEventListener("visibilitychange", sync);
      delete document.documentElement.dataset.pageVisible;
    };
  }, []);

  /*
   * 页面标题随路由更新（蓝图 §14"页面标题和主标题随路由正确更新"）。
   * 屏幕阅读器与浏览器历史都依赖它，因此每个路由都要有一个明确标题。
   */
  useEffect(() => {
    const base = "ChainSight 调查工作台";
    const title = pageTitleFor(location.pathname);
    document.title = title === "ChainSight" ? base : title + " · " + base;
  }, [location.pathname]);

  /* 登记"最近访问对象"：纯本地加速入口，不缓存业务字段（§5.3）。 */
  useEffect(() => {
    const described = describeRoute(location.pathname);
    if (described) pushRecentObject({ path: location.pathname, kind: described.kind, label: described.label });
  }, [location.pathname]);

  const handleLogout = useCallback(async () => {
    await logout();
    navigate("/login", { replace: true });
  }, [logout, navigate]);

  const permissions = session?.permissions ?? [];

  return (
    <NotificationsProvider>
      <AppShell
        variant="user"
        productName="ChainSight"
        productTag="用户调查工作台"
        brandMark="CS"
        env={session?.audience === "user" ? "用户层系统" : (session?.audience ?? "")}
        navGroups={navGroups(permissions)}
        user={
          session
            ? { displayName: session.display_name, email: session.email, roles: session.roles }
            : null
        }
        mobileTitle={pageTitleFor(location.pathname)}
        railFooter={
          <span className="cs-railfoot__scope">
            <ShieldCheck size={13} aria-hidden="true" />
            按当前角色权限裁剪
          </span>
        }
        commandTrigger={
          <button
            type="button"
            className="cs-cmdtrigger"
            onClick={() => setCommandOpen(true)}
            aria-label="打开命令面板"
            aria-keyshortcuts="Control+K"
          >
            <Command size={15} aria-hidden="true" />
            <span className="cs-cmdtrigger__label">搜索或跳转…</span>
            <span className="cs-cmdtrigger__keys" aria-hidden="true">
              <span className="cs-kbd">Ctrl</span>
              <span className="cs-kbd">K</span>
            </span>
          </button>
        }
        notifications={<NotificationPopover />}
        headerExtra={
          /* 桌面显示在顶栏；<900px 由 CSS 隐藏，改由用户菜单里的 variant="menu" 承担（§5.2）。 */
          <span className="cs-topbar-workspace">
            <WorkspaceSwitcher />
          </span>
        }
        userMenuExtra={<WorkspaceSwitcher variant="menu" />}
        onLogout={handleLogout}
      >
        {children}
      </AppShell>
      <CommandPalette open={commandOpen} onClose={() => setCommandOpen(false)} />
    </NotificationsProvider>
  );
}
