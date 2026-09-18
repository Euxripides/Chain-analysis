import React, { useCallback, useState } from "react";
import { Check, Copy, ExternalLink } from "lucide-react";
import { useDialog } from "./Dialog";
import { cx } from "./layout";

/**
 * 对象展示组件（蓝图 §8.3）
 *
 * CopyButton / HashValue / KeyValueList / Timeline / MetaLine。
 *
 * 蓝图 §2.5 的硬约束在这里落地：**界面允许视觉省略，但复制、title、详情和
 * 提交值必须保留完整原文**。HashValue 的 `title` 永远是完整值，复制走完整值，
 * 只有被渲染出来的那段文字才是缩略的。
 */

/** 复制完整值；成功/失败结果继续走 Dialog（AGENTS.md 唯一提示通道）。 */
export function CopyButton({
  value,
  label,
  title,
  className,
}: {
  /** 要复制到剪贴板的**完整原文**，不得传缩略值。 */
  value: string;
  /** 无障碍名称里描述的对象，例如"交易哈希"。 */
  label: string;
  title?: string;
  className?: string;
}) {
  const dialog = useDialog();
  const [copied, setCopied] = useState(false);

  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      /* 反馈只做 1.2s 的图标变化，不弹窗——复制是高频操作，弹窗会打断连续工作。
         失败必须弹窗（用户需要知道为什么没复制上）。 */
      window.setTimeout(() => setCopied(false), 1200);
      return true;
    } catch {
      dialog.notify("warning", "无法访问剪贴板", "浏览器拒绝了剪贴板访问，请手动选中并复制。完整值已显示在悬停提示中。");
      return false;
    }
  }, [dialog, value]);

  return (
    <button
      type="button"
      className={cx("cs-copybtn", className)}
      data-copied={copied ? "true" : undefined}
      aria-label={"复制" + label}
      title={title ?? "复制完整值"}
      onClick={() => void copy()}
    >
      {copied ? <Check size={13} aria-hidden="true" /> : <Copy size={13} aria-hidden="true" />}
    </button>
  );
}

/**
 * 地址 / 哈希 / 金额的 monospace 展示。
 *
 * - `title` 恒为完整值（§2.5）。
 * - `wrap` 用于详情页头部：长值可以换行，而不是撑破容器。
 * - 表格里用视觉省略 + 完整 title，符合 §13"长地址可以在详情区换行"。
 */
export function HashValue({
  value,
  head = 10,
  tail = 8,
  wrap = false,
  copyable = true,
  href,
  label,
  className,
}: {
  value: string;
  /** 缩略时保留的前缀字符数；`full` 时忽略。 */
  head?: number;
  tail?: number;
  /** true 显示完整值并允许换行（详情页用）。 */
  wrap?: boolean;
  copyable?: boolean;
  href?: string;
  label?: string;
  className?: string;
}) {
  const full = value || "";
  const short = !full ? "—" : full.length > head + tail + 1 ? full.slice(0, head) + "…" + full.slice(-tail) : full;
  const text = wrap ? full : short;
  const name = label ?? "值";
  return (
    <span className={cx("cs-hash", className)}>
      {href ? (
        <a className={cx("cs-hash__text", wrap && "cs-hash__text--wrap")} href={href} title={full}>
          {text}
        </a>
      ) : (
        <span className={cx("cs-hash__text", wrap && "cs-hash__text--wrap")} title={full}>
          {text}
        </span>
      )}
      {copyable && full ? <CopyButton value={full} label={name} /> : null}
    </span>
  );
}

/** 外链到对象详情页时的标准图标按钮（不承担复制职责）。 */
export function EntityOpenLink({ href, label }: { href: string; label: string }) {
  return (
    <a className="cs-copybtn" href={href} aria-label={label} title={label}>
      <ExternalLink size={13} aria-hidden="true" />
    </a>
  );
}

/** 元数据与 provenance 的键值列表（§8.3 KeyValueList）。 */
export interface KeyValueEntry {
  label: string;
  value: React.ReactNode;
  /** 长值（哈希、JSON）横跨整行，避免被挤成窄列。 */
  wide?: boolean;
}

export function KeyValueList({ items, ariaLabel }: { items: KeyValueEntry[]; ariaLabel?: string }) {
  return (
    <dl className="cs-kv" aria-label={ariaLabel}>
      {items.map((item) => (
        <div className={cx("cs-kv__row", item.wide && "cs-kv__row--wide")} key={item.label}>
          <dt>{item.label}</dt>
          <dd>{item.value === undefined || item.value === null || item.value === "" ? "—" : item.value}</dd>
        </div>
      ))}
    </dl>
  );
}

/** 业务时间线：案件动态、任务事件、审批记录（§8.3 Timeline）。 */
export interface TimelineEntry {
  key: string;
  title: React.ReactNode;
  meta?: React.ReactNode;
  tone?: "neutral" | "ok" | "warn" | "danger" | "info";
  icon?: React.ReactNode;
  /** 右侧附加内容，例如状态徽标。 */
  aside?: React.ReactNode;
}

export function Timeline({ items, ariaLabel = "时间线" }: { items: TimelineEntry[]; ariaLabel?: string }) {
  return (
    <ol className="cs-timeline" aria-label={ariaLabel}>
      {items.map((item) => (
        <li className="cs-timeline__item" key={item.key}>
          <span className="cs-timeline__rail">
            <span className="cs-timeline__dot" data-tone={item.tone ?? "neutral"}>
              {item.icon}
            </span>
          </span>
          <div className="cs-timeline__body">
            <div className="cs-timeline__title">{item.title}</div>
            {item.meta ? <div className="cs-timeline__meta">{item.meta}</div> : null}
          </div>
          {item.aside}
        </li>
      ))}
    </ol>
  );
}

/**
 * 页面元信息条：数据时间、来源、范围。
 *
 * 这些属于"界面本身"（字段名、数据来源标签），不是提示内容——
 * 但对它们的**额外解释**必须走 Dialog（code-standards.md 末段）。
 */
export function MetaLine({ items }: { items: Array<{ label: string; value: React.ReactNode }> }) {
  return (
    <div className="cs-meta">
      {items.map((item) => (
        <span className="cs-meta__item" key={item.label}>
          {item.label}
          <b>{item.value}</b>
        </span>
      ))}
    </div>
  );
}
