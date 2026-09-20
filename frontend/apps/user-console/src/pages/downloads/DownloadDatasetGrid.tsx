import React, { useEffect, useRef } from "react";
import { motion } from "motion/react";
import { EmptyState, useDialog } from "@chain/ui";
import {
  ArrowLeftRight,
  Boxes,
  Check,
  Coins,
  FileText,
  Hash,
  Image as ImageIcon,
  Waypoints,
  WalletMinimal,
} from "lucide-react";
import { datasetDescription, datasetSelectable, datasetUnavailableReason } from "./dataset-copy";
import { DL_EASE_ENTER, downloadMotion } from "./download.motion";
import type { DatasetMeta } from "./download.types";

/**
 * ③ Dataset（规范 §13）
 *
 * 页面专用组件：只接收服务端 Dataset 元数据与当前值，**不自行请求接口**。
 *
 * 每张卡都是真实 checkbox（<label> 包住铺满整卡的 input[type=checkbox]）：
 * Tab 遍历、空格切换、读屏名称与 getByRole("checkbox") 全部与重构前一致（§13.4），
 * 不允许退化成"只有 onClick 的 div"。
 *
 * 视觉（§13.5）：白底实色 + 1px 灰蓝边框；hover 上移 2px 且只加强边框（不缩放文字）；
 * 选中是 1.5px 主蓝边框 + 右上角蓝色勾选方块 + 底部一条极淡蓝青弧线，180ms。
 *
 * 上限（§13.6）：达到 6 项后未选项不可再选，点击/键盘操作由页面用 Dialog 说明上限 ——
 * 这里不做"假 disabled"（真的 disabled 会让点击事件根本发不出来），也不加页面警告条。
 *
 * 元数据加载失败（§20）：页面正文**不留**错误横幅，改由 Dialog 呈现（danger）并给出"重试"；
 * 服务端返回空清单是业务空态，用 EmptyState 表达（属于业务数据区，不是提示横幅）。
 *
 * 可用性（开发文档 §5.2）：只有 status === "available" 可勾选；unsupported 整格禁用，
 * 不可用原因只挂在 title / aria-label 上，并以虚线边框作为形状通道，不写内联说明段落。
 */

export interface DownloadDatasetGridProps {
  items: DatasetMeta[];
  values: string[];
  max: number;
  loading: boolean;
  error: string | null;
  busy: boolean;
  onChange: (next: string[]) => void;
  onRetry: () => void;
  /** 超过上限的尝试：交给页面用 Dialog 说明（§13.6）。 */
  onLimitReached: (label: string) => void;
}

const DATASET_ICONS: Record<string, React.ComponentType<{ size?: number; "aria-hidden"?: boolean }>> = {
  transactions: ArrowLeftRight,
  token_transfers: Coins,
  nft_transfers: ImageIcon,
  logs: FileText,
  balances: WalletMinimal,
  internal_transactions: Boxes,
  trace: Waypoints,
};

function DatasetGlyph({ id }: { id: string }) {
  const Icon = DATASET_ICONS[id] ?? Hash;
  return <Icon size={18} aria-hidden />;
}

export function DownloadDatasetGrid({
  items,
  values,
  max,
  loading,
  error,
  busy,
  onChange,
  onRetry,
  onLimitReached,
}: DownloadDatasetGridProps) {
  const dialog = useDialog();
  /* 同一个错误只提示一次；用户点"重试"后清空，失败再次提示（不会无限弹窗）。 */
  const notifiedError = useRef<string | null>(null);

  useEffect(() => {
    if (!error || notifiedError.current === error) return;
    notifiedError.current = error;
    void (async () => {
      const answer = await dialog.open({
        kind: "error",
        title: "Dataset 元数据加载失败",
        body: error,
        actions: [
          { label: "关闭", result: "", variant: "default" },
          { label: "重试", result: "retry", variant: "primary" },
        ],
      });
      if (answer === "retry") {
        notifiedError.current = null;
        onRetry();
      }
    })();
  }, [dialog, error, onRetry]);

  if (loading) {
    return (
      <div className="dl-dataset-grid" aria-busy="true">
        {[0, 1, 2, 3].map((index) => (
          <span key={index} className="dl-dataset-card dl-dataset-card--skeleton" aria-hidden="true" />
        ))}
      </div>
    );
  }

  if (items.length === 0) {
    /* 空态是**业务数据区**的一部分（服务端确实没有返回可用 Dataset），
       不是提示横幅：只有一句业务结论 + 重新加载入口。 */
    return (
      <EmptyState
        title="服务端未返回可用 Dataset"
        action={
          <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm" onClick={onRetry}>
            重新加载 Dataset
          </button>
        }
      />
    );
  }

  const selected = new Set(values);
  const atLimit = selected.size >= max;

  const toggle = (item: DatasetMeta, next: boolean) => {
    if (!next) {
      onChange(values.filter((value) => value !== item.id));
      return;
    }
    if (selected.has(item.id)) return;
    if (selected.size >= max) {
      onLimitReached(item.label);
      return;
    }
    /* 保持服务端返回顺序：payload 里的 dataset 顺序因此是可复现的。 */
    const order = new Map(items.map((entry, index) => [entry.id, index]));
    const list = [...values, item.id].sort((a, b) => (order.get(a) ?? 0) - (order.get(b) ?? 0));
    onChange(list);
  };

  return (
    <div className="dl-dataset-grid" role="group" aria-label="Dataset">
      {items.map((item) => {
        const isSelected = selected.has(item.id);
        /* unsupported（或服务端没标 available）= 整格禁用；busy 只是暂时不可操作，两者分开。 */
        const reason = datasetUnavailableReason(item);
        const unavailable = reason !== null;
        const disabled = unavailable || busy;
        const description = datasetDescription(item.id);
        const name = item.label + "（" + item.id + "）";
        const title = [name, description, reason].filter(Boolean).join(" · ");
        return (
          <label
            key={item.id}
            className="dl-dataset-card"
            data-selected={isSelected ? "true" : undefined}
            data-disabled={disabled ? "true" : undefined}
            data-unavailable={unavailable ? "true" : undefined}
            data-blocked={!isSelected && atLimit ? "true" : undefined}
            title={title}
          >
            <input
              type="checkbox"
              className="dl-dataset-card__input"
              checked={isSelected}
              disabled={disabled}
              onChange={(event) => toggle(item, event.target.checked)}
              /* 禁用原因必须能被读屏读到：aria-label 带上原因，而正文不写说明段落。 */
              aria-label={unavailable ? name + "：" + reason : name}
              aria-disabled={disabled || undefined}
            />
            <span className="dl-dataset-card__icon" aria-hidden="true">
              <DatasetGlyph id={item.id} />
            </span>
            <span className="dl-dataset-card__label">{item.label}</span>
            <motion.span
              className="dl-dataset-card__check"
              aria-hidden="true"
              initial={false}
              animate={isSelected ? { opacity: 1, scale: 1 } : { opacity: 0, scale: 0.8 }}
              transition={{ duration: downloadMotion.fast, ease: DL_EASE_ENTER }}
            >
              <Check size={12} strokeWidth={3} />
            </motion.span>
            {description ? <span className="dl-dataset-card__desc">{description}</span> : null}
            {/* 底部极淡蓝青弧线：选中时用 Motion 画出来（§13.5 / §18.4）。 */}
            <svg className="dl-dataset-card__arc" viewBox="0 0 100 6" preserveAspectRatio="none" aria-hidden="true" focusable="false">
              <motion.path
                d="M 2 4.5 Q 50 -1.5 98 4.5"
                fill="none"
                stroke="var(--dl-cyan)"
                strokeWidth={1.5}
                vectorEffect="non-scaling-stroke"
                strokeLinecap="round"
                initial={false}
                animate={{ pathLength: isSelected ? 1 : 0, opacity: isSelected ? 0.55 : 0 }}
                transition={{ duration: downloadMotion.fast, ease: DL_EASE_ENTER }}
              />
            </svg>
          </label>
        );
      })}
    </div>
  );
}

export default DownloadDatasetGrid;