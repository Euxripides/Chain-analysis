import React from "react";
import { FileSpreadsheet, Loader2, RotateCcw, WandSparkles } from "lucide-react";
import { IMPORT_FILE_EXTENSIONS } from "../../import/parse-core";
import type { ServerValidateResult } from "./download.types";
import { useDelayedFlag } from "./download.hooks";

/**
 * ① 对象与地址（规范 §11）
 *
 * 版面（《全量整改实施规范》§7.5.3）：一行两列 —— 左 textarea、右文件导入触发器，
 * 两者共用同一标签行（20px）与控制区高度（桌面 96px / 短屏桌面 84px），top/bottom/height
 * 差值不超过 1px。工具行只保留：
 *   解析导入 —— textarea 文本解析动作（不打开文件选择器）
 *   清空     —— 保持可用但不抢占主视觉
 * 文件能力收敛为唯一入口：1 个原生 input[type=file] + 1 个可见触发器
 * （data-testid="address-file-trigger"），不再有第二张"从文件导入"卡或"导入 CSV / XLSX"按钮。
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
  /** 唯一文件入口选中文件后的回调（Worker 隔离解析）。 */
  onFileChange: (file: File) => void;
  onFileCancel: () => void;
  onReset: () => void;
  /**
   * 唯一原生文件输入（隐藏）：由页面持有 ref，面板负责渲染到文件字段里。
   * 触发器点击时调用 fileInputRef.current.click()，因此点击 / Enter / Space 都能打开选择器。
   */
  fileInputRef: React.RefObject<HTMLInputElement | null>;
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
  onFileChange,
  onFileCancel,
  onReset,
  fileInputRef,
}: DownloadAddressPanelProps) {
  /* 服务端接受之后，textarea 里就是服务端 normalized 的权威集合（§11.2）。 */
  /* 解析/校验超过 300ms 才显示运行反馈（§8.5），按钮不会在快响应时闪一下。 */
  const parseBusy = useDelayedFlag(busy || fileBusy, 300);
  const applied = parsedFrom !== "" && parsedFrom === addressText.trim();
  const accepted = applied && serverVerdict ? serverVerdict.summary.accepted : null;
  const rejected = applied && serverVerdict ? serverVerdict.summary.rejected : null;

  return (
    <div className="dl-address">
      <div className="dl-address__input-grid">
        <div className="dl-address__text-field">
          <label className="dl-field-label" htmlFor="download-addresses">
            地址列表
          </label>
          <textarea
            id="download-addresses"
            className="dl-textarea dl-address__textarea"
            value={addressText}
            onChange={(event) => onAddressTextChange(event.target.value)}
            placeholder="每行一个地址，或用逗号 / 分号 / 空格分隔多个地址"
            spellCheck={false}
            aria-label="地址列表"
            aria-invalid={invalid ? true : undefined}
          />
        </div>

        <div className="dl-address__file-field" data-busy={fileBusy ? "true" : undefined}>
          <span className="dl-field-label">文件导入</span>
          <input
            ref={fileInputRef}
            id="download-address-file"
            className="sr-only"
            type="file"
            accept={IMPORT_FILE_EXTENSIONS.join(",")}
            aria-label="导入地址文件"
            data-testid="address-file-input"
            onChange={(event) => {
              const file = event.target.files?.[0];
              event.target.value = "";
              if (file) onFileChange(file);
            }}
          />
          <button
            type="button"
            className="dl-address__file-trigger"
            data-testid="address-file-trigger"
            aria-describedby="download-address-file-help"
            aria-busy={fileBusy || undefined}
            disabled={fileBusy}
            onClick={() => fileInputRef.current?.click()}
          >
            <FileSpreadsheet size={20} aria-hidden="true" />
            <span className="dl-address__file-trigger-title">{fileBusy ? "正在解析文件" : "从文件导入"}</span>
            <span id="download-address-file-help" className="dl-address__file-trigger-hint">
              {fileBusy ? fileName ?? "文件" : "支持 TXT、CSV、XLS、XLSX"}
            </span>
          </button>
          {fileBusy ? (
            <div className="dl-address__import" data-busy="true">
              <span className="dl-address__progress" role="progressbar" aria-label="文件解析进度" aria-valuetext="正在解析文件">
                <span className="dl-address__progressbar" />
              </span>
              <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm" onClick={onFileCancel}>
                取消解析
              </button>
            </div>
          ) : null}
        </div>
      </div>

      {/* 工具行：只保留"解析导入"（解析 textarea 文本）与"清空"；文件入口已在右侧唯一触发器上。 */}
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
  );
}

export default DownloadAddressPanel;