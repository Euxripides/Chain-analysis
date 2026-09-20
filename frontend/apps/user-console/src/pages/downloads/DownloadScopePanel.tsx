import React, { useEffect, useRef } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ChevronsRight, Info, Shield, Zap } from "lucide-react";
import { useDialog } from "@chain/ui";
import { ChainSelect, type ChainOption } from "./components/ChainSelect";
import { DL_EASE_ENTER, DL_EASE_EXIT, DL_EXIT_RATIO, downloadMotion } from "./download.motion";
import type { PerformanceMode, RangeCapabilities, RangeType } from "./download.types";

/**
 * ② 链与范围（规范 §12）
 *
 * 字段：链 / 范围类型 / 起止区块 / 起止时间 / 性能模式 safe·fast·turbo。
 *
 * 范围类型不再是原生 <select>，而是**保留可访问单选语义**的分段控件
 * （<fieldset> + radio，§12.2）：选中态同时有背景色、图标与 aria-checked，
 * 不靠颜色单独表意。
 *
 * 性能模式是三个模式卡（§12.3）：图标只用 Lucide（safe=盾牌、fast=双线速度、
 * turbo=闪电），模式解释属于提示性说明，点击信息图标后由页面通过 Dialog 呈现 ——
 * 卡片里不堆积说明段落（AGENTS.md / code-standards 第 5 条）。
 *
 * 范围类型按 Dataset 能力开放（开发文档 §5.2 / §13.1）：能力表由页面按所选 Dataset 求交集后
 * 传入，不支持的范围类型在原位禁用并把原因挂在 title / aria-label 上；
 * 能力未返回（旧后端）时退回 blockOnly（Solana 只支持显式 Slot 区间）。
 *
 * 链为 Solana 时区块标签写作"起始 / 结束 Slot"（Slot 就是 Solana 的区块高度），
 * 字段 id 与提交负载保持不变。
 *
 * 网络元数据加载失败（§20）：页面正文不留错误横幅，用 Dialog 呈现并给出"重新加载"。
 */

export interface DownloadScopePanelProps {
  chains: ChainOption[];
  chain: string;
  rangeType: RangeType;
  fromBlock: string;
  toBlock: string;
  fromTime: string;
  toTime: string;
  performanceMode: PerformanceMode;
  busy: boolean;
  chainsLoading: boolean;
  chainsError: string | null;
  /** Solana 只支持显式区块区间（服务端 solanaRangeGuard 同判；能力表可用时以能力表为准）。 */
  blockOnly: boolean;
  /**
   * 任务级范围能力（所选 Dataset 的交集）。
   * null = 服务端未返回能力字段，或只有 balances（快照语义，没有范围概念）—— 此时不施加能力约束。
   */
  rangeCapabilities: RangeCapabilities | null;
  onReloadChains: () => void;
  onChainChange: (value: string) => void;
  onRangeTypeChange: (value: RangeType) => void;
  onFromBlockChange: (value: string) => void;
  onToBlockChange: (value: string) => void;
  onFromTimeChange: (value: string) => void;
  onToTimeChange: (value: string) => void;
  onPerformanceModeChange: (value: PerformanceMode) => void;
  /** 模式解释：只走 Dialog（§12.3）。 */
  onOpenModeHelp: (mode: PerformanceMode) => void;
  invalid?: { chain?: boolean; range?: boolean };
}

const RANGE_OPTIONS: ReadonlyArray<{ value: RangeType; label: string; slotLabel?: string }> = [
  { value: "full", label: "全量" },
  { value: "block", label: "区块范围", slotLabel: "Slot 范围" },
  { value: "time", label: "时间范围" },
];

const MODE_OPTIONS: ReadonlyArray<{ value: PerformanceMode; label: string; hint: string }> = [
  { value: "safe", label: "safe", hint: "稳定优先" },
  { value: "fast", label: "fast", hint: "并发提速" },
  { value: "turbo", label: "turbo", hint: "最大吞吐" },
];

function ModeIcon({ mode }: { mode: PerformanceMode }) {
  if (mode === "safe") return <Shield size={18} aria-hidden="true" />;
  if (mode === "fast") return <ChevronsRight size={18} aria-hidden="true" />;
  return <Zap size={18} aria-hidden="true" />;
}

export function DownloadScopePanel({
  chains,
  chain,
  rangeType,
  fromBlock,
  toBlock,
  fromTime,
  toTime,
  performanceMode,
  busy,
  chainsLoading,
  chainsError,
  blockOnly,
  rangeCapabilities,
  onReloadChains,
  onChainChange,
  onRangeTypeChange,
  onFromBlockChange,
  onToBlockChange,
  onFromTimeChange,
  onToTimeChange,
  onPerformanceModeChange,
  onOpenModeHelp,
  invalid,
}: DownloadScopePanelProps) {
  const dialog = useDialog();
  /* 同一个错误只提示一次；点"重新加载"后清空，再次失败会再次提示。 */
  const notifiedError = useRef<string | null>(null);
  const slotLabels = chain === "solana";

  useEffect(() => {
    if (!chainsError || notifiedError.current === chainsError) return;
    notifiedError.current = chainsError;
    void (async () => {
      const answer = await dialog.open({
        kind: "error",
        title: "网络元数据加载失败",
        body: chainsError,
        actions: [
          { label: "关闭", result: "", variant: "default" },
          { label: "重新加载", result: "reload", variant: "primary" },
        ],
      });
      if (answer === "reload") {
        notifiedError.current = null;
        onReloadChains();
      }
    })();
  }, [chainsError, dialog, onReloadChains]);

  /** 该范围类型不可用的原因；可用时返回 null。只陈述服务端能力表与链的事实。 */
  const disabledReason = (value: RangeType): string | null => {
    if (rangeCapabilities && !rangeCapabilities[value]) {
      if (value === "full") return "所选 Dataset 在当前链上没有全量来源（服务端能力表 supports_full=false）";
      if (value === "time") return "所选 Dataset 在当前链上不支持时间范围（服务端能力表 supports_time_range=false）";
      return "所选 Dataset 在当前链上不支持区块区间（服务端能力表 supports_block_range=false）";
    }
    if (blockOnly && value !== "block") return "Solana 只支持显式 Slot（区块）区间";
    return null;
  };

  return (
    <div className="dl-scope">
      <div className="dl-scope__row">
        <div className="dl-field dl-field--chain">
          <label className="dl-field-label" htmlFor="download-chain">
            链
          </label>
          <ChainSelect
            id="download-chain"
            value={chain}
            options={chains}
            loading={chainsLoading}
            onChange={onChainChange}
            invalid={Boolean(invalid?.chain)}
          />
        </div>

        <fieldset className="dl-field dl-field--range" id="download-range-type">
          <legend className="dl-field-label">范围类型</legend>
          <div className="dl-segmented" data-invalid={invalid?.range ? "true" : undefined}>
            {RANGE_OPTIONS.map((option) => {
              const reason = disabledReason(option.value);
              const label = slotLabels && option.slotLabel ? option.slotLabel : option.label;
              return (
                <label
                  key={option.value}
                  className="dl-segmented__item"
                  data-active={rangeType === option.value ? "true" : undefined}
                  data-disabled={reason ? "true" : undefined}
                  title={reason ?? undefined}
                >
                  <input
                    type="radio"
                    name="download-range-type"
                    value={option.value}
                    checked={rangeType === option.value}
                    disabled={Boolean(reason) || busy}
                    /* 禁用原因必须能被读屏读到：aria-label 只在不可用时覆盖（可用时保持原可访问名）。 */
                    aria-label={reason ? label + "：" + reason : undefined}
                    onChange={() => onRangeTypeChange(option.value)}
                  />
                  <span className="dl-segmented__mark" aria-hidden="true" />
                  <span className="dl-segmented__text">{label}</span>
                </label>
              );
            })}
          </div>
        </fieldset>
      </div>

      <AnimatePresence initial={false} mode="wait">
        {rangeType === "block" ? (
          <motion.div
            key="block"
            className="dl-scope__row dl-scope__row--fields"
            variants={{
              hidden: { opacity: 0, y: 8 },
              show: { opacity: 1, y: 0, transition: { duration: downloadMotion.standard, ease: DL_EASE_ENTER } },
              exit: {
                opacity: 0,
                y: -8,
                transition: { duration: downloadMotion.standard * DL_EXIT_RATIO, ease: DL_EASE_EXIT },
              },
            }}
            initial="hidden"
            animate="show"
            exit="exit"
          >
            <div className="dl-field">
              <label className="dl-field-label" htmlFor="download-from-block">
                {slotLabels ? "起始 Slot" : "起始区块"}
              </label>
              <input
                id="download-from-block"
                className="dl-input mono"
                inputMode="numeric"
                value={fromBlock}
                onChange={(event) => onFromBlockChange(event.target.value)}
              />
            </div>
            <div className="dl-field">
              <label className="dl-field-label" htmlFor="download-to-block">
                {slotLabels ? "结束 Slot" : "结束区块"}
              </label>
              <input
                id="download-to-block"
                className="dl-input mono"
                inputMode="numeric"
                value={toBlock}
                onChange={(event) => onToBlockChange(event.target.value)}
              />
            </div>
          </motion.div>
        ) : null}

        {rangeType === "time" ? (
          <motion.div
            key="time"
            className="dl-scope__row dl-scope__row--fields"
            variants={{
              hidden: { opacity: 0, y: 8 },
              show: { opacity: 1, y: 0, transition: { duration: downloadMotion.standard, ease: DL_EASE_ENTER } },
              exit: {
                opacity: 0,
                y: -8,
                transition: { duration: downloadMotion.standard * DL_EXIT_RATIO, ease: DL_EASE_EXIT },
              },
            }}
            initial="hidden"
            animate="show"
            exit="exit"
          >
            <div className="dl-field">
              <label className="dl-field-label" htmlFor="download-from-time">
                开始时间
              </label>
              <input
                id="download-from-time"
                className="dl-input"
                type="datetime-local"
                value={fromTime}
                onChange={(event) => onFromTimeChange(event.target.value)}
              />
            </div>
            <div className="dl-field">
              <label className="dl-field-label" htmlFor="download-to-time">
                结束时间
              </label>
              <input
                id="download-to-time"
                className="dl-input"
                type="datetime-local"
                value={toTime}
                onChange={(event) => onToTimeChange(event.target.value)}
              />
            </div>
          </motion.div>
        ) : null}
      </AnimatePresence>

      <fieldset className="dl-field dl-field--mode" id="download-performance">
        <legend className="dl-field-label">性能模式</legend>
        <div className="dl-modes">
          {MODE_OPTIONS.map((option) => (
            <div key={option.value} className="dl-mode" data-active={performanceMode === option.value ? "true" : undefined}>
              <label className="dl-mode__hit">
                <input
                  type="radio"
                  name="download-performance"
                  value={option.value}
                  checked={performanceMode === option.value}
                  disabled={busy}
                  onChange={() => onPerformanceModeChange(option.value)}
                />
                <span className="dl-mode__icon" aria-hidden="true">
                  <ModeIcon mode={option.value} />
                </span>
                <span className="dl-mode__text">
                  <span className="dl-mode__label">{option.label}</span>
                  <span className="dl-mode__hint">{option.hint}</span>
                </span>
                <span className="dl-mode__mark" aria-hidden="true" />
              </label>
              {/* 模式解释是提示性说明：只走 Dialog，不在这里堆段落（§12.3）。 */}
              <button
                type="button"
                className="dl-mode__info"
                aria-label={option.label + " 模式说明"}
                onClick={() => onOpenModeHelp(option.value)}
              >
                <Info size={14} aria-hidden="true" />
              </button>
            </div>
          ))}
        </div>
      </fieldset>
    </div>
  );
}

export default DownloadScopePanel;