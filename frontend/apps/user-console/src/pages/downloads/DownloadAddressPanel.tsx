import React from "react";
import { AnimatePresence, motion } from "motion/react";
import { FileSpreadsheet, Loader2, Route as RouteIcon, RotateCcw, WandSparkles } from "lucide-react";
import type { ServerValidateResult } from "./download.types";
import { downloadMotion } from "./download.motion";
import { useDelayedFlag } from "./download.hooks";

/**
 * ① 对象与地址（规范 §11）
 *
 * 版面：section head（序号 1 / 标题 / 计数）+ 一行两列 —— 左 textarea（占满剩余宽度，
 * 最小高度 124px），右"从文件导入"面板。工具栏三个操作的层级是固定的：
 *   解析导入      —— 主要次级操作（把粘贴的地址送去本地解析 + 服务端校验）
 *   导入 CSV/XLSX —— 普通次级操作（走隔离 Worker）
 *   清空          —— 保持可用但不抢占主视觉
 *
 * 业务事实（解析计数、文件解析事实、服务端接受/拒绝）都以数据行呈现；
 * 非法/重复/超限的**解释**只出现在 Dialog 里（AGENTS.md：提示性内容不得嵌入页面）。
 */

export interface DownloadAddressPanelProps {
  addressText: string;
  /** 服务端 accepted 并已生效的地址数（业务事实）。 */
  importedCount: number;
  /** 已生效文本的快照：与当前文本一致时说明这批地址就是服务端接受的集合。 */
  parsedFrom: string;
  serverVerdict: ServerValidateResult | null;
  busy: boolean;
  /** 本地拆分计数（去抖 250ms 的真实计数）。 */
  recognized: number;
  deduped: number;
  duplicates: number;
  /** 上限（来自 import 层常量，不写死）。 */
  max: number;
  /** 字段级无效状态：只表达状态，不显示文案（文案走 Dialog）。 */
  invalid?: boolean;
  /** 文件解析中（Worker 隔离解析）。 */
  fileBusy: boolean;
  fileName?: string;
  onAddressTextChange: (value: string) => void;
  onParse: () => void;
  onFileCancel: () => void;
  onReset: () => void;
  /** 隐藏的真实文件输入：<input type="file" aria-label="导入地址文件">。 */
  fileInput: React.ReactNode;
}

export function DownloadAddressPanel({
  addressText,
  importedCount,
  parsedFrom,
  serverVerdict,
  busy,
  recognized,
  deduped,
  duplicates,
  max,
  invalid = false,
  fileBusy,
  fileName,
  onAddressTextChange,
  onParse,
  onFileCancel,
  onReset,
  fileInput,
}: DownloadAddressPanelProps) {
  /* 服务端接受之后，textarea 里就是服务端 normalized 的权威集合（§11.2）。 */
  /* 解析/校验超过 300ms 才显示运行反馈（§8.5），按钮不会在快响应时闪一下。 */
  const parseBusy = useDelayedFlag(busy || fileBusy, 300);
  const applied = parsedFrom !== "" && parsedFrom === addressText.trim();
  const accepted = applied && serverVerdict ? serverVerdict.summary.accepted : null;
  const rejected = applied && serverVerdict ? serverVerdict.summary.rejected : null;

  return (
    <div className="dl-address">
      <div className="dl-address__grid">
        <div className="dl-address__field">
          <label className="dl-field-label" htmlFor="download-addresses">
            地址列表
          </label>
          <textarea
            id="download-addresses"
            className="dl-textarea"
            value={addressText}
            onChange={(event) => onAddressTextChange(event.target.value)}
            placeholder="每行一个地址，或用逗号 / 分号 / 空格分隔多个地址"
            spellCheck={false}
            aria-label="地址列表"
            aria-invalid={invalid ? true : undefined}
          />
          <div className="dl-address__toolbar">
          <div className="dl-address__facts" role="group" aria-label="地址解析计数">
            <span className="dl-fact">
              <span className="dl-fact__label">已识别</span>
              <b className="dl-fact__value">{recognized}</b>
            </span>
            <span className="dl-fact">
              <span className="dl-fact__label">去重后</span>
              <b className="dl-fact__value">{deduped}</b>
            </span>
            {duplicates > 0 ? (
              <span className="dl-fact" data-tone="warn">
                <span className="dl-fact__label">重复条目</span>
                <b className="dl-fact__value">{duplicates}</b>
              </span>
            ) : null}
            {accepted !== null && rejected !== null ? (
              <>
                <span className="dl-fact" data-tone="ok">
                  <span className="dl-fact__label">服务端接受</span>
                  <b className="dl-fact__value">{accepted}</b>
                </span>
                <span className="dl-fact" data-tone={rejected > 0 ? "danger" : undefined}>
                  <span className="dl-fact__label">服务端拒绝</span>
                  <b className="dl-fact__value">{rejected}</b>
                </span>
              </>
            ) : null}
            {applied && serverVerdict ? (
              <span className="dl-fact">
                <span className="dl-fact__label">链分布</span>
                <b className="dl-fact__value">
                  {Object.entries(serverVerdict.summary.chains).map(([key, value]) => key + " " + value).join(" · ") || "—"}
                </b>
              </span>
            ) : null}
          </div>
          <div className="dl-address__actions">
            <button
              type="button"
              className="dl-btn dl-btn--primary-soft"
              onClick={onParse}
              disabled={busy || fileBusy}
              aria-busy={parseBusy || undefined}
            >
              {parseBusy ? <Loader2 className="dl-spin" size={15} aria-hidden="true" /> : <WandSparkles size={15} aria-hidden="true" />}
              <span>{parseBusy ? "正在解析…" : "解析导入"}</span>
            </button>
            <label className="dl-btn dl-btn--ghost dl-filebtn">
              <FileSpreadsheet size={15} aria-hidden="true" />
              <span>{fileBusy ? "解析中…" : "导入 CSV / XLSX"}</span>
              {fileInput}
            </label>
            <button
              type="button"
              className="dl-btn dl-btn--ghost dl-address__reset"
              onClick={onReset}
              disabled={importedCount === 0 && addressText.trim() === ""}
            >
              <RotateCcw size={15} aria-hidden="true" />
              <span>清空</span>
            </button>
          </div>
          </div>
        </div>

        <div className="dl-address__import" data-busy={fileBusy ? "true" : undefined}>
          <span className="dl-address__importicon" aria-hidden="true">
            {fileBusy ? <Loader2 className="dl-spin" size={22} /> : <RouteIcon size={22} />}
          </span>
          <p className="dl-address__importtitle">{fileBusy ? "正在解析文件" : "从文件导入"}</p>
          <p className="dl-address__importhint">{fileBusy ? fileName ?? "文件" : "支持 TXT、CSV、XLS、XLSX"}</p>
          <AnimatePresence initial={false}>
            {fileBusy ? (
              <motion.div
                className="dl-address__progress"
                role="progressbar"
                aria-label="文件解析进度"
                aria-valuetext="正在解析"
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0, transition: { duration: downloadMotion.fast } }}
              >
                <span className="dl-address__progressbar" />
                <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm" onClick={onFileCancel}>
                  取消解析
                </button>
              </motion.div>
            ) : null}
          </AnimatePresence>
        </div>
      </div>
    </div>
  );
}

export default DownloadAddressPanel;