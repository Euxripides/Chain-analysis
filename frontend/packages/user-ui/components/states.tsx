import React from "react";
import {
  AlertTriangle,
  CircleAlert,
  Inbox,
  Lock,
  RefreshCw,
  SearchX,
} from "lucide-react";
import { cx } from "../utils";

/**
 * 页面业务状态（用户端重构规范 V4 §24）
 *
 * 约束（仓库 AGENTS.md / docs/code-standards.md）：
 *   这里只表达「这一屏没有可展示的业务数据」这一事实，以及最主要的下一步操作。
 *   任何**解释性/提示性**内容（口径说明、风险说明、操作结果、失败原因详情）
 *   都必须通过 `useDialog()` 弹窗呈现，不能作为说明段落嵌进页面布局。
 *
 * 因此这些组件的 body 只接受"为什么这一屏是空的"这一句事实性描述，
 * 不承载业务建议；需要展开说明时由调用方打开 Dialog。
 */

interface StateBaseProps {
  title: string;
  /** 一句话原因。不要在此写业务建议或口径说明——那属于弹窗。 */
  body?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}

function StateShell({
  kind,
  icon,
  title,
  body,
  action,
  className,
  extra,
}: StateBaseProps & { kind: string; icon: React.ReactNode; extra?: React.ReactNode }) {
  return (
    <div className={cx("cs-" + kind, className)}>
      <span className={cx("cs-" + kind + "__icon")} aria-hidden="true">
        {icon}
      </span>
      <div className={cx("cs-" + kind + "__title")}>{title}</div>
      {body ? <div className={cx("cs-" + kind + "__body")}>{body}</div> : null}
      {extra}
      {action ? <div className={cx("cs-" + kind + "__action")}>{action}</div> : null}
    </div>
  );
}

/** 空状态（§24.1）：不是大块空白，高度 140–180px，并给出最主要下一步。 */
export function CsEmpty(props: StateBaseProps) {
  return <StateShell kind="empty" icon={<Inbox size={20} strokeWidth={2.1} />} {...props} />;
}

/** 错误状态（§24.2）：安全文案 + 重试；不暴露后端 stack。trace id 可选。 */
export function CsError({
  traceId,
  onRetry,
  retryLabel = "重试",
  ...props
}: StateBaseProps & { traceId?: string; onRetry?: () => void; retryLabel?: string }) {
  return (
    <StateShell
      kind="errorstate"
      icon={<CircleAlert size={20} strokeWidth={2.2} />}
      extra={traceId ? <div className="cs-errorstate__trace">trace id: {traceId}</div> : null}
      action={
        props.action ?? (
          onRetry ? (
            <button type="button" className="btn btn--sm" onClick={onRetry}>
              <RefreshCw size={14} aria-hidden="true" />
              {retryLabel}
            </button>
          ) : null
        )
      }
      {...props}
    />
  );
}

/**
 * 无权限状态（§24.3）：显示缺少的 permission 名称与返回入口，
 * **不伪装成 404**（仓库既有约束，保持）。
 */
export function CsNoPermission({
  permission,
  title = "没有访问该功能的权限",
  action,
  className,
}: {
  permission?: string;
  title?: string;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("cs-nopermission", className)}>
      <span className="cs-nopermission__icon" aria-hidden="true">
        <Lock size={20} strokeWidth={2.2} />
      </span>
      <div className="cs-nopermission__title">{title}</div>
      <div className="cs-nopermission__body">
        当前角色未被授予所需权限
        {permission ? (
          <>
            （<span className="mono">{permission}</span>）
          </>
        ) : null}
        。
      </div>
      {action ? <div className="cs-nopermission__action">{action}</div> : null}
    </div>
  );
}

/** 未知对象/路由（§24.4）：只说明路径没有对应对象，不暗示"未来会实现"。 */
export function CsNotFound({ title = "找不到该对象", body, action, className }: StateBaseProps) {
  return (
    <StateShell
      kind="notfound"
      icon={<SearchX size={20} strokeWidth={2.2} />}
      title={title}
      body={body ?? "当前地址没有对应的对象。可能是链接已过期，或对象标识被改动过。"}
      action={action}
      className={className}
    />
  );
}

/* ------------------------------------------------------------------ Skeleton */

/**
 * Skeleton（§23.1）：**七种形状**，不是同一条灰线糊所有页面。
 * 首次加载才用完整 skeleton；已有数据刷新用 `CsRefreshBar`（§23.3）。
 */
export type CsSkeletonShape = "dashboard" | "table" | "detail" | "form" | "workspace" | "metric" | "inspector" | "chart" | "graph";

function Bar({ w, h = 12, className }: { w: string | number; h?: number | string; className?: string }) {
  return <div className={cx("cs-skeleton", className)} style={{ width: w, height: h, flex: h === "auto" ? "1 1 auto" : undefined }} aria-hidden="true" />;
}

function TableSkeleton({ rows = 8, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }} aria-hidden="true">
      <div style={{ display: "flex", gap: 12 }}>
        {Array.from({ length: cols }).map((_, i) => (
          <Bar key={i} w={i === 0 ? "22%" : `${Math.floor(78 / (cols - 1))}%`} h={14} />
        ))}
      </div>
      {Array.from({ length: rows }).map((_, r) => (
        <div key={r} style={{ display: "flex", gap: 12 }}>
          {Array.from({ length: cols }).map((_, c) => (
            <Bar key={c} w={c === 0 ? "22%" : `${Math.floor(78 / (cols - 1))}%`} h={12} />
          ))}
        </div>
      ))}
    </div>
  );
}

export function CsSkeleton({ shape = "detail", className }: { shape?: CsSkeletonShape; className?: string }) {
  let inner: React.ReactNode;
  switch (shape) {
    case "table":
      inner = <TableSkeleton />;
      break;
    case "dashboard":
      inner = (
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          <Bar w="42%" h={26} />
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))", gap: 12 }}>
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="cs-surface" style={{ padding: 16, gap: 8 }}>
                <Bar w="52%" h={12} />
                <Bar w="72%" h={24} />
              </div>
            ))}
          </div>
          <Bar w="100%" h={180} />
        </div>
      );
      break;
    case "form":
      inner = (
        <div style={{ display: "flex", flexDirection: "column", gap: 18, maxWidth: 840 }}>
          {Array.from({ length: 5 }).map((_, i) => (
            <div key={i} style={{ display: "flex", flexDirection: "column", gap: 8 }}>
              <Bar w={110} h={12} />
              <Bar w="100%" h={40} />
            </div>
          ))}
          <Bar w={200} h={40} />
        </div>
      );
      break;
    case "metric":
      inner = (
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))", gap: 12 }}>
          {Array.from({ length: 4 }).map((_, i) => (
            <div key={i} className="cs-surface" style={{ padding: 16, gap: 8 }}>
              <Bar w={28} h={28} />
              <Bar w="46%" h={12} />
              <Bar w="70%" h={24} />
            </div>
          ))}
        </div>
      );
      break;
    case "inspector":
      inner = (
        <div style={{ display: "flex", flexDirection: "column", gap: 14, width: 320 }}>
          <Bar w="60%" h={16} />
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <Bar w="38%" h={11} />
              <Bar w="86%" h={14} />
            </div>
          ))}
        </div>
      );
      break;
    case "chart":
      inner = (
        <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
          <Bar w="30%" h={14} />
          <Bar w="100%" h={240} />
        </div>
      );
      break;
    case "graph":
      inner = (
        <div style={{ display: "flex", gap: 16, minHeight: 420 }}>
          <Bar w={200} h="auto" />
          <Bar w="100%" h="auto" />
          <Bar w={320} h="auto" />
        </div>
      );
      break;
    case "workspace":
      inner = (
        <div style={{ display: "flex", gap: 16, minHeight: 360 }}>
          <Bar w={168} h="auto" />
          <Bar w="100%" h="auto" />
        </div>
      );
      break;
    case "detail":
    default:
      inner = (
        <div style={{ display: "flex", flexDirection: "column", gap: 14 }}>
          <Bar w="34%" h={24} />
          <Bar w="58%" h={13} />
          <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))", gap: 14 }}>
            {Array.from({ length: 4 }).map((_, i) => (
              <div key={i} style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <Bar w="40%" h={11} />
                <Bar w="76%" h={14} />
              </div>
            ))}
          </div>
          <Bar w="100%" h={150} />
        </div>
      );
  }
  return (
    <div className={className} role="status" aria-busy="true" aria-live="polite">
      <span className="visually-hidden">正在加载</span>
      {inner}
    </div>
  );
}

/**
 * 刷新已有数据（§23.3）：**不清屏**。
 * 保留旧数据，只在容器顶部显示 2px 进度条。
 */
export function CsRefreshBar({ active }: { active: boolean }) {
  if (!active) return null;
  return <div className="cs-refreshbar" role="progressbar" aria-label="正在刷新" aria-valuetext="正在刷新" />;
}

export { AlertTriangle };
