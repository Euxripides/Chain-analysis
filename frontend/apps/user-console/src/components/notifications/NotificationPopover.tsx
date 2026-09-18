import React, { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { Bell, CheckCheck } from "lucide-react";
import { RelativeTime } from "@chain/ui";
import { useNotifications } from "./useNotifications";

/**
 * 顶栏通知 Popover（蓝图 §10.3）
 *
 * - 铃铛显示未读数，最大 `99+`。
 * - 宽度 360–420px，展示最近 8 条。
 * - 每条包含类型图标、标题、对象、时间和未读标记。
 * - "查看全部"进入 /notifications。
 *
 * Popover **不是提示通道**：它只展示服务端的业务通知数据，
 * 成功/失败/说明仍然走 DialogProvider（AGENTS.md）。
 */
export function NotificationPopover() {
  const { items, unreadCount, markRead, markAllRead, reload } = useNotifications();
  const [open, setOpen] = useState(false);
  const boxRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    const onDocDown = (event: MouseEvent) => {
      if (!boxRef.current?.contains(event.target as Node)) setOpen(false);
    };
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setOpen(false);
      triggerRef.current?.focus();
    };
    document.addEventListener("mousedown", onDocDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onDocDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  const recent = items.slice(0, 8);

  return (
    <div className="cs-usermenu" ref={boxRef}>
      <button
        type="button"
        ref={triggerRef}
        className="cs-iconbtn"
        aria-label={unreadCount > 0 ? `通知，${unreadCount} 条未读` : "通知"}
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => {
          const next = !open;
          setOpen(next);
          if (next) void reload();
        }}
      >
        <Bell size={18} />
        {unreadCount > 0 ? <span className="cs-iconbtn__dot">{unreadCount > 99 ? "99+" : unreadCount}</span> : null}
      </button>

      {open ? (
        <div className="cs-popover" role="dialog" aria-label="通知">
          <div className="cs-popover__head">
            <span className="cs-popover__title">通知</span>
            {unreadCount > 0 ? <span className="cs-bulkbar__count">{unreadCount} 条未读</span> : null}
            <button
              type="button"
              className="btn btn--sm btn--ghost"
              onClick={() => void markAllRead()}
              disabled={unreadCount === 0}
            >
              <CheckCheck size={14} />
              全部已读
            </button>
          </div>
          <div className="cs-popover__list">
            {recent.length === 0 ? (
              <div className="cs-command__empty">当前没有通知。</div>
            ) : (
              recent.map((item) => (
                <Link
                  key={item.id}
                  to="/notifications"
                  className="cs-popover__item"
                  data-unread={!item.read_at ? "true" : "false"}
                  onClick={() => {
                    if (!item.read_at) void markRead(item.id);
                    setOpen(false);
                  }}
                >
                  {!item.read_at ? <span className="cs-popover__dot" aria-hidden="true" /> : <span style={{ width: 7, flex: "none" }} />}
                  <span className="cs-popover__main">
                    <span className="cs-popover__itemtitle">{item.title || item.title_key}</span>
                    <span className="cs-popover__itemsub">
                      {item.object_type ? item.object_type + " · " : ""}
                      <RelativeTime value={item.created_at} />
                    </span>
                  </span>
                </Link>
              ))
            )}
          </div>
          <div className="cs-popover__foot">
            <Link to="/notifications" className="btn btn--sm" onClick={() => setOpen(false)}>
              查看全部
            </Link>
          </div>
        </div>
      ) : null}
    </div>
  );
}
