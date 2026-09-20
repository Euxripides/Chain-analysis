import React from "react";
import { createPortal } from "react-dom";
import { LoaderCircle } from "lucide-react";

/**
 * 移动端底部操作栏（规范 §16.2）
 *
 * **必须 Portal 到 body**：页面根节点带 `cs-page-enter`（入场动画 fill-mode: both），
 * 浏览器会因此把页面根当成 fixed 元素的包含块 —— 不 Portal 的话这条固定栏会跟着文档
 * 跑到页面底部（实测 rect.top = 1525 而不是视口底部）。Portal 后它才真正贴住视口。
 *
 * 固定/粘性在页面底部，背景是不透明白色（alpha ≥ .96 的实色层），顶部 1px 分隔线，
 * 并避开 iOS 安全区：padding-bottom = calc(12px + env(safe-area-inset-bottom))。
 * 两个操作对应业务上的两步：先预览、再创建 —— 创建是主按钮（§16.1 / §16.2）。
 */

export interface DownloadMobileActionBarProps {
  busy: boolean;
  creating: boolean;
  canPreview: boolean;
  canAttemptCreate: boolean;
  onPreview: () => void;
  onCreate: () => void;
}

export function DownloadMobileActionBar({
  busy,
  creating,
  canPreview,
  canAttemptCreate,
  onPreview,
  onCreate,
}: DownloadMobileActionBarProps) {
  const bar = (
    <div className="dl-actionbar" role="group" aria-label="下载操作">
      <button
        type="button"
        className="dl-btn dl-btn--secondary dl-actionbar__secondary"
        onClick={onPreview}
        disabled={!canPreview || busy || creating}
      >
        预览
      </button>
      <button
        type="button"
        className="dl-btn dl-btn--primary dl-actionbar__primary"
        onClick={onCreate}
        disabled={!canAttemptCreate || busy || creating}
        aria-busy={creating || undefined}
      >
        {creating ? <LoaderCircle className="dl-spin" size={17} aria-hidden="true" /> : null}
        <span>{creating ? "正在创建…" : "创建下载任务"}</span>
      </button>
    </div>
  );

  if (typeof document === "undefined") return bar;
  return createPortal(bar, document.body);
}

export default DownloadMobileActionBar;