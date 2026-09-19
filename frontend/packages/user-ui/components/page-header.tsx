import React from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { cx } from "../utils";

/**
 * 页面头（用户端重构规范 V4 §8）
 *
 * API 与 `@chain/ui` 的 `PageHeader` **兼容**（title/subtitle/actions/breadcrumb/status/
 * meta/primaryAction/secondaryAction 语义一致），因此页面迁移时 import 换一行即可，
 * 不需要重排业务结构（§59 的组件映射表）。
 *
 * 结构：
 *   Breadcrumb
 *   Title + Status + Tags
 *   Subtitle / Meta
 *                                   Primary Action / Secondary Actions
 *
 * 进入动效（§8.1）由 surface.css 承担：breadcrumb 先出现、title 再上移淡入、
 * actions 延迟 50ms。这里**不再**叠加 Motion，避免同一状态两个动画所有者（§9.6）。
 */

export interface CsBreadcrumbItem {
  label: string;
  /** 应用内路径。最后一项是当前页，即使给了 to 也不渲染成链接。 */
  to?: string;
}

export interface CsPageHeaderProps {
  title: string;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  breadcrumb?: CsBreadcrumbItem[];
  /** 标题右侧的状态徽标。 */
  status?: React.ReactNode;
  /** 标题右侧的标签（分类、风险等）。 */
  tags?: React.ReactNode;
  /** 数据时间、来源等界面元信息。 */
  meta?: React.ReactNode;
  /** 主操作：**危险操作不能成为默认主按钮**（§8.1）。 */
  primaryAction?: React.ReactNode;
  secondaryAction?: React.ReactNode;
  className?: string;
}

export function CsPageHeader({
  title,
  subtitle,
  actions,
  breadcrumb,
  status,
  tags,
  meta,
  primaryAction,
  secondaryAction,
  className,
}: CsPageHeaderProps) {
  const actionSlot =
    actions ?? (
      <>
        {secondaryAction}
        {primaryAction}
      </>
    );

  return (
    <div className={cx("cs-pageheader", className)}>
      <div className="cs-pageheader__main">
        {breadcrumb && breadcrumb.length > 0 ? (
          <nav className="cs-pageheader__crumbs" aria-label="面包屑">
            {breadcrumb.map((crumb, index) => {
              const isLast = index === breadcrumb.length - 1;
              return (
                <React.Fragment key={crumb.label + index}>
                  {index > 0 ? (
                    <ChevronRight size={12} aria-hidden="true" className="cs-pageheader__sep" />
                  ) : null}
                  {/* 面包屑是应用内导航：必须用 react-router 的 Link 而不是 <a href>，
                      否则整页刷新会丢掉筛选状态与滚动位置，也会绕过 basename（§0.4.1）。 */}
                  {crumb.to && !isLast ? (
                    <Link to={crumb.to}>{crumb.label}</Link>
                  ) : (
                    <span aria-current={isLast ? "page" : undefined}>{crumb.label}</span>
                  )}
                </React.Fragment>
              );
            })}
          </nav>
        ) : null}
        <div className="cs-pageheader__titlerow">
          <h1 className="cs-pageheader__title">{title}</h1>
          {status}
          {tags}
        </div>
        {subtitle ? <div className="cs-pageheader__sub">{subtitle}</div> : null}
        {meta ? <div className="cs-pageheader__meta">{meta}</div> : null}
      </div>
      {actionSlot ? <div className="cs-pageheader__actions">{actionSlot}</div> : null}
    </div>
  );
}

/** 兼容别名：迁移期两种名字都能用，R7 清理时统一。 */
export const CsPageHead = CsPageHeader;
