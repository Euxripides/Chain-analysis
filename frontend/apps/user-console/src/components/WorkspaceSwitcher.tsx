import { useCallback, useEffect, useState } from "react";
import { ApiError, useAuth, type WorkspaceOption } from "@chain/api-client";
import { useDialog } from "@chain/ui";

/**
 * 工作区切换器（P1-20260913 后半段）
 *
 * 与会话是两件事：登录页的选择器决定"用哪个工作区建立会话"，
 * 这里决定"已建立的会话切到哪个工作区"，由服务端按成员资格判定并写审计。
 * 一切提示只走弹窗（AGENTS.md）。
 *
 * `data-testid="workspace-switcher"` 与 `data-audience="user"` **保持不变**（蓝图 §19.10）。
 * `variant` 只影响外观位置：桌面在顶栏，移动端放进用户菜单（§5.2）。
 */
export function WorkspaceSwitcher({ variant = "topbar" }: { variant?: "topbar" | "menu" }) {
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
  const label = current ? current.name + "（" + current.slug + "）" : currentId.slice(0, 8) + "…";

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
            label: option.id === currentId ? option.name + "（当前）" : option.name + "（" + option.slug + "）",
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

  if (variant === "menu") {
    return (
      <button
        type="button"
        className="btn btn--sm"
        data-testid="workspace-switcher"
        data-audience="user"
        data-variant="menu"
        aria-label="切换工作区"
        style={{ justifyContent: "flex-start", width: "100%" }}
        onClick={() => void open()}
        disabled={busy}
      >
        {busy ? "切换中…" : label}
      </button>
    );
  }

  return (
    <button
      type="button"
      className="btn btn--ghost btn--sm cs-workspace-switcher"
      data-testid="workspace-switcher"
      data-audience="user"
      aria-label="切换工作区"
      title={label}
      onClick={() => void open()}
      disabled={busy}
    >
      {busy ? "切换中…" : label}
    </button>
  );
}
