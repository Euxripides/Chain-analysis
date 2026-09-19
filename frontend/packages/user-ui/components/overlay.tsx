import React from "react";
import { X } from "lucide-react";
import { cx } from "../utils";
import { CsContentFade } from "./motion";

/**
 * Overlay / Inspector / SplitView（用户端重构规范 V4 §22）
 *
 * 层级全部来自 `theme/z-index.ts` 的 registry，页面**禁止**自己写 z-index（§22.4）。
 * Portal 归属由 `getPopupContainer()` 统一决定，业务页面不再各自选择挂载点。
 */

/* ----------------------------------------------------------------- Inspector */

export interface CsInspectorProps {
  title: React.ReactNode;
  onClose?: () => void;
  size?: "default" | "large";
  /** 底部固定操作区（sticky）。 */
  actions?: React.ReactNode;
  /** 身份标识变化时做内容 crossfade，而不是让整块重排（§22.3）。 */
  contentKey?: string;
  className?: string;
  children: React.ReactNode;
  "data-testid"?: string;
}

/**
 * 右侧检查器（§22.1）：默认 340px，large 420px；移动端变全屏 Drawer。
 *
 * 内容标准（§22.3）：Object identity / Status / Core fields / Risk-tags /
 * Relations / Actions / Evidence —— 由调用方排布，本组件只保证容器与滚动行为。
 */
export function CsInspector({
  title,
  onClose,
  size = "default",
  actions,
  contentKey,
  className,
  children,
  ...rest
}: CsInspectorProps) {
  const body = <div className="cs-inspectorpanel__body">{children}</div>;
  return (
    <aside
      className={cx("cs-inspectorpanel", className)}
      data-size={size}
      aria-label={typeof title === "string" ? title : "对象检查器"}
      {...rest}
    >
      <header className="cs-inspectorpanel__head">
        <span className="cs-inspectorpanel__title">{title}</span>
        {onClose ? (
          <button
            type="button"
            className="cs-iconbtn"
            style={{ marginLeft: "auto" }}
            aria-label="关闭检查器"
            onClick={onClose}
          >
            <X size={16} />
          </button>
        ) : null}
      </header>
      {contentKey ? <CsContentFade contentKey={contentKey}>{body}</CsContentFade> : body}
      {actions ? <div className="cs-inspectorpanel__actions">{actions}</div> : null}
    </aside>
  );
}

/* -------------------------------------------------------------- StickyAction */

/**
 * 表单页底部操作条（§15.2）：底部半透明白 + 上方渐隐，保存/提交始终可见。
 * 页面滚到底时边界减弱（调用方传 `atEnd`）。
 */
export function CsStickyActions({
  children,
  atEnd,
  className,
}: {
  children: React.ReactNode;
  atEnd?: boolean;
  className?: string;
}) {
  return (
    <div className={cx("cs-actionbar", className)} data-at-end={atEnd ? "true" : "false"}>
      {children}
    </div>
  );
}

/* ------------------------------------------------------------------ SplitView */

/**
 * 主区 + 侧栏（§22.1）。
 *
 * `aside` 为空时自动退化为单列——这样"关闭 Inspector"只是传 null，
 * 不需要页面自己切换两套布局分支。
 */
export function CsSplitView({
  children,
  aside,
  className,
}: {
  children: React.ReactNode;
  aside?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("cs-splitview", className)} data-aside={aside ? "true" : "false"}>
      <div style={{ minWidth: 0 }}>{children}</div>
      {aside ? <div style={{ minWidth: 0 }}>{aside}</div> : null}
    </div>
  );
}

/* --------------------------------------------------------------- Page layout */

/**
 * 页面骨架的 5 种形态（§6.4）：Dashboard / ListPage / DetailPage / FormPage / WorkspacePage。
 * 页面只需要声明自己是哪一种，间距与最大宽度由设计系统统一决定。
 */
export function CsPage({
  kind = "list",
  className,
  children,
}: {
  kind?: "dashboard" | "list" | "detail" | "form" | "workspace";
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div
      className={cx(
        "cs-page",
        kind === "form" && "cs-formpage",
        kind === "detail" && "cs-page--reading",
        className,
      )}
      data-page-kind={kind}
    >
      {children}
    </div>
  );
}

/** 表单页的字段区宽度限制（§15.1：核心字段区 720–840px）。 */
export function CsFormFields({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cx("cs-formpage__fields", className)}>{children}</div>;
}
