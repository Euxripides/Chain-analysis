import React from "react";

/**
 * 配置区章节头（规范 §7.1 / §11.1）
 *
 * 连续工作表面里的一个章节由四部分组成：32px 圆形序号、章节标题、业务副标签、
 * 右侧业务计数/操作槽位。序号由这里渲染，标题文本里不再重复写 ①②③（§6.1）。
 *
 * 移动端（<640px）用它右上角的"编辑"入口把已完成步骤折叠成摘要卡（§7.5）：
 * 折叠/展开的状态由页面持有，这里只渲染按钮。
 */

export interface DownloadSectionHeadProps {
  index: number;
  title: string;
  /** 业务副标签：说明这一节要填写什么，属于字段标签而不是提示文案。 */
  description?: string;
  /** 右侧业务计数（例如"已导入 3 个 · 上限 100"）。 */
  meta?: React.ReactNode;
  /** 移动端折叠态的"编辑"入口。 */
  onEdit?: () => void;
  editLabel?: string;
  /** 折叠态：只显示标题与摘要，不渲染正文。 */
  collapsed?: boolean;
}

export function DownloadSectionHead({
  index,
  title,
  description,
  meta,
  onEdit,
  editLabel = "编辑",
  collapsed = false,
}: DownloadSectionHeadProps) {
  return (
    <header className="dl-section-head">
      <span className="dl-section-head__no" aria-hidden="true">
        {index}
      </span>
      <div className="dl-section-head__text">
        <h2 className="dl-section-head__title">{title}</h2>
        {description ? <p className="dl-section-head__desc">{description}</p> : null}
      </div>
      {meta ? <div className="dl-section-head__meta">{meta}</div> : null}
      {onEdit && collapsed ? (
        <button type="button" className="btn btn--sm dl-section-head__edit" onClick={onEdit}>
          {editLabel}
        </button>
      ) : null}
    </header>
  );
}

export default DownloadSectionHead;
