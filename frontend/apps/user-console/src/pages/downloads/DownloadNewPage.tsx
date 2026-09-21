import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { MotionConfig, motion, useReducedMotion } from "motion/react";
import { TriangleAlert } from "lucide-react";
import { ApiError } from "@chain/api-client";
import { useDialog } from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { formatRawAmount, useApi, useS5Action } from "../../sprint5/common";
import { IMPORT_MAX_ADDRESSES, type ImportedAddress, type LocalParseSummary } from "../../import/parse-core";
import { ImportParseError, formatParseBudget, importParseTimeoutMs, parseAddressFileInWorker } from "../../import/parse-client";
import { DownloadFlowRail } from "./DownloadFlowRail";
import { DownloadAddressPanel } from "./DownloadAddressPanel";
import { DownloadScopePanel } from "./DownloadScopePanel";
import { DownloadDatasetGrid } from "./DownloadDatasetGrid";
import { DownloadCoveragePanel } from "./DownloadCoveragePanel";
import { DownloadTaskPreview } from "./DownloadTaskPreview";
import { DownloadMobileActionBar } from "./DownloadMobileActionBar";
import { DownloadSectionHead } from "./DownloadSectionHead";
import { useDialogFocusGuard, usePageVisible } from "./download.hooks";
import { AddressFileSummary, type FileParseFacts } from "./components/AddressFileSummary";
import { ProviderPlanTable } from "./components/ProviderPlanTable";
import type { ChainOption } from "./components/ChainSelect";
import type { DownloadStepKey, ProviderPlanItem } from "./download.types";
import {
  datasetRangeCapabilities,
  downloadStepState,
  percent,
  previewEstimatedCost,
  previewEstimatedCu,
  previewModeClamp,
  rangeText,
  toIso,
} from "./download.types";
import { dlDialogItem, dlDialogStagger, dlPageEnter, dlPageEnterStep, downloadMotion } from "./download.motion";
import {
  DEFAULT_REUSE_POLICY,
  groupAddressesByChain,
  parseAddressInput,
  stableIdempotencyKey,
  STEP_ITEMS,
} from "./download.types";
import type {
  ChainMeta,
  DatasetMeta,
  DownloadBody,
  DownloadCreateData,
  DownloadPreviewData,
  PerformanceMode,
  PreviewCoverageItem,
  RangeBody,
  RangeType,
  ServerMetaVerdict,
  ServerValidateResult,
} from "./download.types";
import "./download-new.css";

/* ============================================================================
 * U-DL-01 /downloads/new —— Flowline 数据轨道
 *
 * 版面（《新建数据下载 Flowline 界面重构实施规范》）：
 *   页面头部 → 横向 Flowline（5 步）→ 两栏工作区
 *     左：一块连续配置表面（对象与地址 / 链与范围 / Dataset / Coverage 与成本预览）
 *     右：sticky 任务预览 + 创建入口
 *   移动端（<640px）：竖向轨道 + 折叠摘要卡 + 底部安全区操作栏
 *
 * 业务契约完全冻结（§20.3）：路由、权限、API 路径、CSRF/受众头、地址规范化、
 * 100 地址与 6 Dataset 上限、金额与大整数精度、幂等语义、审计事件均未改动。
 *
 * 复用策略没有用户入口（§5）：唯一的取值是 DEFAULT_REUSE_POLICY（prefer_local），
 * 仍然显式出现在 preview / create 的请求体里；页面正文、弹窗、DOM 与可访问名称
 * 里都不存在对应控件，也没有任何 CSS 假隐藏。
 * ==========================================================================*/

/**
 * Provider 计划引导语（提示性内容：只在 Dialog 里出现，不进页面正文）。
 *
 * 口径说明刻意写清楚：Data API 的"页"与 JSON-RPC 的"调用"是两个计数单位，
 * 一页 100 条交易不等于 100 次请求（开发文档 §13.2）。
 */
const PROVIDER_PLAN_LEAD =
  "下表来自最近一次服务端预览返回的 Provider 计划，一行对应一个 Provider × API 通道；" +
  "Data API 页数与 RPC 调用数是两个口径，不合并计算。实际并发由服务端 Provider 档位解析。";

/** Provider 计划区块：预览弹窗与模式说明共用同一份渲染。 */
function ProviderPlanBlock({ items, label }: { items: ProviderPlanItem[]; label: string }) {
  return (
    <div className="dl-dialog__plan">
      <span className="dl-dialog__planlabel">{label}</span>
      <p className="dl-dialog__lead">{PROVIDER_PLAN_LEAD}</p>
      <ProviderPlanTable items={items} caption={label} />
    </div>
  );
}

export default function DownloadNewPage() {
  const dialog = useDialog();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();
  const reducedMotion = Boolean(useReducedMotion());
  /* reduced motion 下不播放进入动效，直接以最终状态渲染（§18.6）。 */
  const enterInitial = reducedMotion ? false : "hidden";
  /* 本页弹窗的焦点守卫：保证 Esc / Tab 在弹窗打开后立即可用（§17.5）。 */
  useDialogFocusGuard();
  /* 页面不可见时暂停唯一的循环动效（Flowline 光点，§8.11 / REM-006）。 */
  const pageVisible = usePageVisible();

  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");

  /* ---------------------------------------------------------------- 地址状态 */
  const [addressText, setAddressText] = useState("");
  const [addresses, setAddresses] = useState<string[]>([]);
  const [parsedFrom, setParsedFrom] = useState("");
  const [imported, setImported] = useState<ImportedAddress[]>([]);
  const [serverVerdict, setServerVerdict] = useState<ServerValidateResult | null>(null);
  const [localParse, setLocalParse] = useState<{ tokens: number; list: string[]; duplicates: number }>({ tokens: 0, list: [], duplicates: 0 });
  const [pendingFileRows, setPendingFileRows] = useState<ImportedAddress[] | null>(null);

  /* ------------------------------------------------------------ 链 / 范围状态 */
  const [chain, setChain] = useState("");
  const [rangeType, setRangeType] = useState<RangeType>("full");
  const [fromBlock, setFromBlock] = useState("");
  const [toBlock, setToBlock] = useState("");
  const [fromTime, setFromTime] = useState("");
  const [toTime, setToTime] = useState("");
  const [datasets, setDatasets] = useState<string[]>([]);
  const [performanceMode, setPerformanceMode] = useState<PerformanceMode>("safe");

  /* ---------------------------------------------------------------- 预览状态 */
  const [preview, setPreview] = useState<DownloadPreviewData | null>(null);
  const [previews, setPreviews] = useState<Array<{ chain: string; data: DownloadPreviewData }>>([]);
  const [previewFingerprint, setPreviewFingerprint] = useState<string | null>(null);
  const [previewState, setPreviewState] = useState<"idle" | "loading" | "error">("idle");
  const [lastPreview, setLastPreview] = useState<DownloadPreviewData | null>(null);
  const [lastTrace, setLastTrace] = useState<string | null>(null);

  /* ---------------------------------------------------------------- 文件导入 */
  const [importBusy, setImportBusy] = useState(false);
  const [importFileName, setImportFileName] = useState<string | undefined>(undefined);
  const importAbort = useRef<AbortController | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  /* ------------------------------------------------------------ 步骤与折叠态 */
  const [focusStep, setFocusStep] = useState<DownloadStepKey | null>(null);
  /** 地址解析成功后的一次性数据脉冲令牌（§8.5）：只在真实状态变化时自增。 */
  const [railPulse, setRailPulse] = useState(0);
  /** 创建成功后的一次性反馈令牌（§8.10）。 */
  const [createdPulse, setCreatedPulse] = useState(0);
  const [isMobile, setIsMobile] = useState(false);
  const [compactRail, setCompactRail] = useState(false);

  const datasetsTouched = useRef(false);

  /* 断点只用 matchMedia 判断一次来源，避免 CSS 与 JS 各自维护一套（§7.5）。 */
  useEffect(() => {
    const mobile = window.matchMedia("(max-width: 639px)");
    const compact = window.matchMedia("(max-width: 899px)");
    const sync = () => {
      setIsMobile(mobile.matches);
      setCompactRail(compact.matches);
    };
    sync();
    mobile.addEventListener("change", sync);
    compact.addEventListener("change", sync);
    return () => {
      mobile.removeEventListener("change", sync);
      compact.removeEventListener("change", sync);
    };
  }, []);

  /**
   * Dataset 白名单必须按链获取：服务端 chainDatasetsByChain 是唯一事实来源。
   * 路径字面量保持不变、链以 query 传递，契约冻结检查因此仍与基线逐字一致。
   */
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets", [chain], chain ? { chain } : undefined);

  const chainItems = chainsApi.data?.items ?? [];
  const datasetItems = datasetsApi.data?.items ?? [];
  const solanaChain = chain === "solana";
  /*
   * 元数据加载失败不再由页面统一 notify：链清单失败在 ② 链与范围面板提示（那里有
   * "重新加载"的真实入口），Dataset 清单失败在 ③ Dataset 网格提示（那里有"重试"）。
   * 三处内联错误横幅因此全部消失，提示一律走 Dialog（AGENTS.md / code-standards 第 5 条）。
   */
  const rangeCapabilities = useMemo(() => datasetRangeCapabilities(datasetItems, datasets), [datasetItems, datasets]);
  /* 能力表可用时以它为准；未返回时退回"Solana 只支持显式 Slot 区间"的旧规则。 */
  const blockOnly = solanaChain && rangeCapabilities === null;

  useEffect(() => {
    if (chain) return;
    const first = chainItems.find((item) => item.enabled) ?? chainItems[0];
    if (first) setChain(first.slug);
  }, [chain, chainItems]);

  useEffect(() => {
    if (datasetsTouched.current) return;
    const available = datasetItems.map((item) => item.id);
    if (available.length === 0) return;
    const preferred = ["transactions", "token_transfers"].filter((id) => available.includes(id));
    setDatasets(preferred.length > 0 ? preferred : [available[0]]);
  }, [datasetItems]);

  useEffect(() => {
    if (datasetItems.length === 0) return;
    const available = new Set(datasetItems.map((item) => item.id));
    setDatasets((current) => {
      const next = current.filter((id) => available.has(id));
      if (next.length > 0) return next;
      const preferred = ["transactions", "token_transfers"].filter((id) => available.has(id));
      return preferred.length > 0 ? preferred : [datasetItems[0].id];
    });
  }, [datasetItems]);

  /** 地址文本 → 本地计数：debounce 250ms。 */
  useEffect(() => {
    const timer = setTimeout(() => setLocalParse(parseAddressInput(addressText)), 250);
    return () => clearTimeout(timer);
  }, [addressText]);

  const buildPayload = useCallback(
    (addrs: string[], groupChain: string, groupItems: ImportedAddress[]): DownloadBody => {
      let range: RangeBody;
      if (rangeType === "block") {
        range = { type: "block", from_block: Number(fromBlock), to_block: Number(toBlock) };
      } else if (rangeType === "time") {
        range = { type: "time", from: toIso(fromTime), to: toIso(toTime) };
      } else {
        range = { type: "full" };
      }
      const subjectMeta: Record<string, { case_name: string; tags: string[] }> = {};
      for (const item of groupItems) {
        if (item.caseName || item.tags.length > 0) subjectMeta[item.normalized] = { case_name: item.caseName, tags: item.tags };
      }
      const body: DownloadBody = {
        chain: groupChain,
        addresses: addrs,
        datasets,
        range,
        reuse_policy: DEFAULT_REUSE_POLICY,
        performance_mode: performanceMode,
      };
      if (Object.keys(subjectMeta).length > 0) body.subject_meta = subjectMeta;
      return body;
    },
    [datasets, fromBlock, fromTime, performanceMode, rangeType, toBlock, toTime],
  );

  const importedAddressText = parsedFrom !== "" && parsedFrom === addressText.trim();

  const invalidatePreview = useCallback(() => {
    setPreview(null);
    setPreviewFingerprint(null);
    setPreviewState("idle");
  }, []);

  /* ------------------------------------------------------------ 当前步骤（§6.2） */

  const activeStep: DownloadStepKey =
    addresses.length === 0 ? "objects" : !chain ? "range" : datasets.length === 0 ? "datasets" : !preview ? "preview" : "create";

  /* 数据变化导致当前步骤前进/后退时，收起移动端的手工展开态。 */
  useEffect(() => {
    setFocusStep(null);
  }, [activeStep]);

  const jumpToStep = useCallback(
    (key: string) => {
      const stepKey = key as DownloadStepKey;
      setFocusStep(stepKey);
      const target = document.getElementById("download-step-" + stepKey);
      if (!target) return;
      /*
       * 桌面端左栏是独立滚动容器：只滚动它，绝不滚动页面根节点（整改规范 §8.8）。
       * 容器不存在（移动端/中窄屏）时才退回普通 scrollIntoView。
       */
      const scroller = target.closest<HTMLElement>(".dl-config-surface");
      if (scroller && scroller.scrollHeight > scroller.clientHeight + 1) {
        const top = target.offsetTop - scroller.offsetTop;
        scroller.scrollTo({ top, behavior: reducedMotion ? "auto" : "smooth" });
        return;
      }
      target.scrollIntoView({ behavior: reducedMotion ? "auto" : "smooth", block: "start" });
    },
    [reducedMotion],
  );

  /* ------------------------------------------------------------ 服务端校验 */

  const revalidateOnServer = useCallback(async (rows: ImportedAddress[]): Promise<ServerValidateResult | null> => {
    const response = await run(async () => {
      try {
        return await client.post<ServerValidateResult>("/api/v2/downloads/validate-addresses", {
          chain,
          items: rows.map((row) => ({ address: row.input, chain: row.chain, case: row.caseName, tags: row.tags })),
          create_missing_meta: false,
        });
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    return response ? response.data : null;
  }, [chain, client, run]);

  const applyServerVerdict = useCallback(async (verdict: ServerValidateResult): Promise<ImportedAddress[] | null> => {
    const caseStatus = new Map(verdict.meta.cases.map((item) => [item.name, item.status]));
    const tagStatus = new Map(verdict.meta.tags.map((item) => [item.name, item.status]));
    const usable = (status: string | undefined): boolean => status === "resolved" || status === "will_create";
    const droppedCases = new Set<string>();
    const droppedTags = new Set<string>();
    const accepted: ImportedAddress[] = verdict.accepted.map((item) => {
      let caseName = item.case_name;
      if (caseName && !usable(caseStatus.get(caseName))) {
        droppedCases.add(caseName);
        caseName = "";
      }
      const tags = item.tags.filter((tag) => {
        if (usable(tagStatus.get(tag))) return true;
        droppedTags.add(tag);
        return false;
      });
      return { input: item.input, normalized: item.normalized, chain: item.chain, caseName, tags };
    });
    const details = [
      { label: "服务端提交", value: String(verdict.summary.submitted) },
      { label: "服务端接受", value: String(verdict.summary.accepted) },
      { label: "服务端拒绝", value: String(verdict.summary.rejected) },
      { label: "链分布", value: Object.entries(verdict.summary.chains).map(([k, v]) => k + ":" + v).join("、") || "—" },
      { label: "作用域", value: "org " + verdict.scope.organization_id.slice(0, 8) + "… / ws " + verdict.scope.workspace_id.slice(0, 8) + "…" },
      { label: "校验来源", value: verdict.source },
      {
        label: "本次会话权限",
        value:
          "download:create=" + verdict.grants["download:create"] +
          " · case:create=" + verdict.grants["case:create"] +
          " · case:update=" + verdict.grants["case:update"],
      },
    ];
    const metaText = (item: ServerMetaVerdict): string => {
      switch (item.status) {
        case "resolved": return "本工作区已存在（" + ((item.case_id ?? item.tag_id) ?? "").slice(0, 8) + "…）";
        case "will_create": return "本工作区不存在，create_missing_meta=true 且已授权创建";
        case "denied": return "本工作区不存在，且没有创建权限（需 " + (item.required_permission ?? "—") + "）—— 已从本次提交中移除";
        default: return "本工作区不存在 —— 已从本次提交中移除，不会出现在创建请求里";
      }
    };
    for (const item of verdict.meta.cases) details.push({ label: "案件 " + item.name, value: metaText(item) });
    for (const item of verdict.meta.tags) details.push({ label: "标签 " + item.name, value: metaText(item) });
    verdict.rejected.slice(0, 20).forEach((item) => {
      details.push({ label: "拒绝 第" + item.row + "行 " + item.code, value: item.input || "（空）" });
    });
    if (verdict.rejected.length > 20) {
      details.push({ label: "其余拒绝条目", value: String(verdict.rejected.length - 20) + " 条" });
    }
    for (const item of verdict.field_rejections ?? []) {
      details.push({
        label: "字段拒绝 第" + item.row + "行 " + item.field + " " + item.code,
        value: item.message + "（" + item.count + "/" + item.limit + "）",
      });
    }
    if ((verdict.summary.tags_over_limit ?? 0) > 0) {
      details.push({ label: "标签超额行", value: verdict.summary.tags_over_limit + " 行；这些行不会绑定任何标签（非静默截断）" });
    }
    if ((verdict.summary.meta_denied ?? 0) > 0) {
      details.push({ label: "元数据创建被拒", value: verdict.summary.meta_denied + " 项：当前会话无创建权限" });
    }
    if (droppedCases.size > 0) details.push({ label: "本次不绑定的案件", value: [...droppedCases].join("、") });
    if (droppedTags.size > 0) details.push({ label: "本次不绑定的标签", value: [...droppedTags].join("、") });
    if (droppedCases.size > 0 || droppedTags.size > 0) {
      details.push({ label: "实际提交", value: "上述名称已从创建请求的 payload 中移除（不会被提交）" });
    }
    const answer = await dialog.open({
      kind: accepted.length === 0 ? "error" : verdict.rejected.length > 0 ? "warning" : "success",
      title: "服务端地址校验结果",
      body: accepted.length === 0
        ? "服务端未接受任何地址，请修正文件后重试。"
        : "只有服务端 accepted 的规范化地址会进入预览与创建；被拒条目不会静默丢弃。",
      details,
      actions: accepted.length === 0
        ? [{ label: "知道了", result: "ok", variant: "primary" }]
        : [
            { label: "取消", result: "", variant: "default" },
            { label: "使用服务端结果", result: "use", variant: "primary" },
          ],
    });
    if (answer !== "use" || accepted.length === 0) return null;
    setImported(accepted);
    setAddresses(accepted.map((item) => item.normalized));
    /* 真实数据落地：播放一次 420ms 数据脉冲（不伪造进度、不加业务请求）。 */
    setRailPulse((value) => value + 1);
    setServerVerdict(verdict);
    const normalizedText = accepted.map((item) => item.normalized).join("\n");
    setAddressText(normalizedText);
    setParsedFrom(normalizedText);
    invalidatePreview();
    return accepted;
  }, [dialog, invalidatePreview]);

  /** 「解析导入」：粘贴文本 → 本地拆分 → 服务端再次验证（§11.1 / §11.2）。 */
  const commitAddresses = useCallback(async (): Promise<ImportedAddress[] | null> => {
    const parsed = parseAddressInput(addressText);
    if (parsed.tokens === 0) {
      dialog.notify("warning", "没有可解析的地址", "请输入至少一个地址，或用 CSV / XLSX 文件导入。");
      return null;
    }
    if (parsed.list.length > IMPORT_MAX_ADDRESSES) {
      dialog.notify(
        "error",
        "地址数量超出上限",
        "单次最多提交 " + IMPORT_MAX_ADDRESSES + " 个地址，当前识别到 " + parsed.list.length + " 个。请拆分后分次提交。",
      );
      return null;
    }
    const capped = parsed.list.slice(0, IMPORT_MAX_ADDRESSES);
    const verdict = await revalidateOnServer(capped.map((value) => ({ input: value, normalized: "", chain: "", caseName: "", tags: [] })));
    if (!verdict) return null;
    return applyServerVerdict(verdict);
  }, [addressText, revalidateOnServer, applyServerVerdict, dialog]);

  /** 文件导入：隔离 Worker 解析 → 本地解析摘要 Dialog → 服务端再次验证（§11.2 / §11.3）。 */
  const importAddressesFromFile = useCallback(async (file: File): Promise<void> => {
    const timeoutMs = importParseTimeoutMs();
    const controller = new AbortController();
    importAbort.current = controller;
    setImportBusy(true);
    setImportFileName(file.name);
    let parsed: { rows: ImportedAddress[]; summary: LocalParseSummary };
    try {
      const result = await parseAddressFileInWorker(file, chain, { signal: controller.signal, timeoutMs });
      parsed = { rows: result.rows, summary: result.summary };
    } catch (error) {
      const failure = error instanceof ImportParseError ? error : null;
      if (failure?.code === "PARSE_CANCELLED") {
        dialog.notify("warning", "解析已取消", file.name + " 的解析已终止（Worker 已停止），未导入任何地址。");
      } else if (failure?.code === "PARSE_TIMEOUT") {
        dialog.notify(
          "error",
          "解析超时已中止",
          file.name + " 在 " + formatParseBudget(timeoutMs) + "内没有完成，解析 Worker 已被终止（未导入任何地址）。请拆分文件或减少行数后重试。",
        );
      } else if (failure?.code === "FORMAT_REJECTED") {
        dialog.notify("error", "文件解析失败", "无法读取 " + file.name + "：" + failure.message);
      } else {
        dialog.notify("error", "文件解析失败", "无法读取 " + file.name + "：" + (failure ? failure.message : String(error)));
      }
      return;
    } finally {
      importAbort.current = null;
      setImportBusy(false);
    }

    const { rows, summary } = parsed;
    const facts: FileParseFacts = {
      name: file.name,
      kind: file.name.toLowerCase().endsWith(".csv") ? "CSV" : "XLSX/Excel",
      rows: summary.rows,
      blank: summary.blank,
      truncated: summary.truncated,
      usedHeaderRow: summary.usedHeaderRow,
      columns: [summary.chainColumn ? "chain" : null, summary.caseColumn ? "case" : null, summary.tagColumn ? "tags" : null].filter(Boolean) as string[],
      perChain: summary.perChain,
      metaRows: summary.metaRows,
    };

    if (rows.length === 0) {
      await dialog.open({
        kind: "warning",
        title: "文件里没有可用的地址",
        body: file.name + " 扫描 " + summary.rows + " 行，其中空白 " + summary.blank + " 行，没有可提交的地址。",
        details: [{ label: "文件", value: file.name }],
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
      return;
    }

    /* 本地解析摘要是**数据**（不是提示）：摘要本体仍然放在 Dialog 里，
       正文只保留计数与操作（AGENTS.md / code-standards 第 5 条）。 */
    const answer = await dialog.open({
      kind: "info",
      title: "本地解析摘要",
      body: "以下计数由浏览器本地解析得出；提交后由服务端再次验证，只有服务端 accepted 的地址会被采用。",
      /* 摘要本体：data-testid="file-parse-summary" 仍然挂在同一个 <dl> 上。 */
      details: [{ label: "解析结果", value: <AddressFileSummary facts={facts} /> }],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "提交服务端校验", result: "submit", variant: "primary", testId: "local-parse-submit" },
      ],
    });
    if (answer !== "submit") return;

    setPendingFileRows(rows);
    const verdict = await revalidateOnServer(rows);
    if (!verdict) return;
    const applied = await applyServerVerdict(verdict);
    if (applied) setPendingFileRows(null);
  }, [applyServerVerdict, chain, dialog, revalidateOnServer]);

  const ensureAddresses = useCallback(async (): Promise<ImportedAddress[] | null> => {
    if (imported.length > 0 && (importedAddressText || (addressText.trim() === "" && serverVerdict !== null))) return imported;
    if (pendingFileRows && pendingFileRows.length > 0) {
      const verdict = await revalidateOnServer(pendingFileRows);
      if (!verdict) return null;
      const applied = await applyServerVerdict(verdict);
      if (applied) setPendingFileRows(null);
      return applied;
    }
    return commitAddresses();
  }, [applyServerVerdict, commitAddresses, imported, importedAddressText, pendingFileRows, revalidateOnServer, addressText, serverVerdict]);

  /* ---------------------------------------------------------------- 校验 */

  const validateAll = useCallback((addrs: string[]): { title: string; message: string } | null => {
    if (!chain) return { title: "请选择链", message: "链与范围是预览与创建的前置条件。" };
    if (addrs.length === 0) return { title: "请输入至少一个地址", message: "可以先粘贴地址，或用 CSV / XLSX 文件导入。" };
    if (addrs.length > IMPORT_MAX_ADDRESSES) {
      return { title: "地址数量超出上限", message: "最多支持 " + IMPORT_MAX_ADDRESSES + " 个地址。" };
    }
    if (datasets.length === 0) return { title: "请选择至少一个 Dataset", message: "Dataset 决定这次下载包含哪些数据表。" };
    if (datasets.length > 6) return { title: "Dataset 数量超出上限", message: "单次最多选择 6 种 Dataset。" };
    if (rangeType === "block") {
      const from = Number(fromBlock);
      const to = Number(toBlock);
      if (fromBlock.trim() === "" || toBlock.trim() === "" || !Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to < 0) {
        return { title: "区块范围不合法", message: "请填写从 0 开始的整数区块高度。" };
      }
      if (from > to) return { title: "区块范围不合法", message: "起始区块不能大于结束区块。" };
    }
    if (rangeType === "time") {
      const from = toIso(fromTime);
      const to = toIso(toTime);
      if (!from || !to) return { title: "时间范围不合法", message: "请选择合法的起始与结束时间。" };
      if (new Date(from).getTime() > new Date(to).getTime()) return { title: "时间范围不合法", message: "起始时间不能晚于结束时间。" };
    }
    return null;
  }, [chain, datasets.length, fromBlock, fromTime, rangeType, toBlock, toTime]);

  const reportIssue = useCallback((issue: { title: string; message: string }) => {
    dialog.notify("warning", issue.title, issue.message);
  }, [dialog]);

  /* ---------------------------------------------------------------- 预览 */

  /**
   * 预览弹窗的明细行（共享 Dialog 的键值表）。
   *
   * 顺序（§17.2）：结论与数据路径在正文里 → 这里接 Coverage 逐项明细 → 最后是风险列表。
   * 不得出现复用策略内部字段（§5.4 / §14.3）；估算依据只在"成本与风险说明"弹窗里。
   */
  const previewDetails = useCallback((data: DownloadPreviewData, groupChain?: string) => {
    const details: Array<{ label: string; value: React.ReactNode }> = [];
    if (groupChain) details.push({ label: "链", value: groupChain });
    details.push({ label: "Coverage 命中", value: data.reuse.coverage_hit ? "是" : "否" });
    details.push({ label: "将复用现有任务", value: data.reuse.will_reuse ? "是" : "否" });
    for (const item of data.coverage.items.slice(0, 12)) {
      const head = item.subject.length > 18 ? item.subject.slice(0, 10) + "…" + item.subject.slice(-6) : item.subject;
      details.push({
        label: "Coverage " + head + " · " + item.dataset,
        value:
          percent(item.ratio) + " · 已覆盖 " + formatRawAmount(String(item.covered_blocks), 0) + "/" +
          formatRawAmount(String(item.total_blocks), 0) + " · 缺口 " + item.gaps.length,
      });
    }
    if (data.coverage.items.length > 12) {
      details.push({ label: "其余 Coverage 明细", value: String(data.coverage.items.length - 12) + " 项" });
    }
    /* 风险列表必须排在所有明细之后（§17.2 的顺序）：共享 renderer 把 details 渲染在正文之后。 */
    data.risks.forEach((risk, index) =>
      details.push({ label: "风险 " + (index + 1), value: <span className="dl-dialog__risk">{risk}</span> }),
    );
    return details;
  }, []);

  /** 预览弹窗正文（§17.2 的顺序：结论 → 数据路径 → 详情 → 风险）。 */
  const previewDialogBody = useCallback((data: DownloadPreviewData, groupChain?: string) => {
    const providerNeeded = data.provider_work_required;
    const path = [
      { key: "local", label: "本地 Coverage", hint: data.reuse.coverage_hit ? "命中" : "部分覆盖", state: data.reuse.coverage_hit ? "done" : "partial" },
      { key: "provider", label: "Provider", hint: providerNeeded ? "需要新增" : "未使用", state: providerNeeded ? "partial" : "unused" },
      { key: "task", label: "下载任务", hint: data.reuse.will_reuse ? "复用现有任务" : "创建新任务", state: "todo" },
    ] as const;
    return (
      /* MotionConfig reducedMotion="user"：系统开启"减少动效"时，Motion 侧的位移/缩放
         一并停用（与 CSS 侧 @media (prefers-reduced-motion) 形成双保险，§18.6）。 */
      <MotionConfig reducedMotion="user">
      <motion.div className="dl-dialog" variants={dlDialogStagger} initial="hidden" animate="show">
        <motion.p className="dl-dialog__lead" variants={dlDialogItem}>
          {data.risks.length > 0
            ? "以下风险由服务端预览接口返回，请确认后再创建任务。"
            : "以下结论由服务端预览接口返回，全部为真实数据。"}
        </motion.p>
        <motion.div className="dl-dialog__facts" variants={dlDialogItem}>
          <div className="dl-dialog__fact">
            <span className="dl-dialog__factlabel">Coverage</span>
            <span className="dl-dialog__factvalue">{percent(data.coverage.overall_ratio)}</span>
          </div>
          <div className="dl-dialog__fact">
            <span className="dl-dialog__factlabel">预估成本</span>
            <span className="dl-dialog__factvalue">{data.estimate.estimated_cost_usd ? data.estimate.estimated_cost_usd + " USD" : "—"}</span>
          </div>
          <div className="dl-dialog__fact">
            <span className="dl-dialog__factlabel">Provider 工作</span>
            <span className="dl-dialog__factvalue">{providerNeeded ? "需要新增" : "无需新增"}</span>
          </div>
        </motion.div>
        <motion.div className="dl-dialog__path" variants={dlDialogItem} aria-label="数据路径">
          {path.map((node, index) => (
            <React.Fragment key={node.key}>
              {index > 0 ? <span className="dl-dialog__arrow" aria-hidden="true">→</span> : null}
              <span className="dl-dialog__node" data-state={node.state}>
                <b>{node.label}</b>
                <span>{node.hint}</span>
              </span>
            </React.Fragment>
          ))}
        </motion.div>
        <motion.dl className="dl-dialog__meta" variants={dlDialogItem}>
          {groupChain ? (
            <div className="dl-dialog__metarow">
              <dt>链</dt>
              <dd>{groupChain}</dd>
            </div>
          ) : null}
          <div className="dl-dialog__metarow">
            <dt>候选任务</dt>
            <dd>{data.reuse.candidate ? data.reuse.candidate.job_no + " · " + data.reuse.candidate.status : "无"}</dd>
          </div>
          <div className="dl-dialog__metarow">
            <dt>未覆盖区块</dt>
            <dd>{formatRawAmount(String(data.missing_blocks), 0)}</dd>
          </div>
          <div className="dl-dialog__metarow">
            <dt>预估缺失区块</dt>
            <dd>{data.estimate.estimated_missing_blocks === null ? "—" : formatRawAmount(String(data.estimate.estimated_missing_blocks), 0)}</dd>
          </div>
          <div className="dl-dialog__metarow">
            <dt>预估行数</dt>
            <dd>{data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0)}</dd>
          </div>
          <div className="dl-dialog__metarow">
            <dt>预估 CU</dt>
            <dd>{previewEstimatedCu(data) === null ? "—" : String(previewEstimatedCu(data))}</dd>
          </div>
          <div className="dl-dialog__metarow">
            <dt>预估耗时（秒）</dt>
            <dd>{data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds)}</dd>
          </div>
        </motion.dl>
        {/* Provider 计划区块：服务端返回 provider_plan 时才出现（旧后端不返回 → 整块不渲染）。 */}
        {data.provider_plan && data.provider_plan.length > 0 ? (
          <motion.div variants={dlDialogItem}>
            <ProviderPlanBlock items={data.provider_plan} label={"Provider 计划" + (groupChain ? "（" + groupChain + "）" : "")} />
          </motion.div>
        ) : null}
      </motion.div>
      </MotionConfig>
    );
  }, []);

  const showPreviewDialog = useCallback(
    async (data: DownloadPreviewData, groupChain?: string) => {
      const details = previewDetails(data, groupChain);
      if (!data.provider_work_required) {
        await dialog.open({
          kind: "info",
          title: "无需新增 Provider 请求",
          body: previewDialogBody(data, groupChain),
          details,
          actions: [{ label: "知道了", result: "ok", variant: "primary" }],
        });
        return;
      }
      await dialog.open({
        kind: data.risks.length > 0 ? "warning" : "success",
        title: data.risks.length > 0 ? "预览完成，存在风险提示" : "Coverage 与成本预览完成",
        body: previewDialogBody(data, groupChain),
        details,
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
    },
    [dialog, previewDetails, previewDialogBody],
  );

  /**
   * 预览。`prepared` 是同一次异步流程里已经拿到手的服务端规范化条目
   * （避免 setState 未重渲染时重复调用 validate-addresses）。
   */
  const handlePreview = useCallback(async (prepared?: ImportedAddress[]): Promise<{ items: Array<{ chain: string; data: DownloadPreviewData }>; fingerprint: string } | null> => {
    const importedRows = prepared ?? (await ensureAddresses());
    if (!importedRows) return null;
    const issue = validateAll(importedRows.map((row) => row.normalized));
    if (issue) {
      reportIssue(issue);
      return null;
    }
    const groups = groupAddressesByChain(importedRows, chain);
    if (groups.length === 0) {
      reportIssue({ title: "缺少链", message: "每个地址都需要确定一条链后才能预览。" });
      return null;
    }
    const payloads = groups.map((group) => buildPayload(group.items.map((item) => item.normalized), group.chain, group.items));
    const fingerprint = JSON.stringify(payloads);
    setPreviewState("loading");
    const responses = await Promise.all(payloads.map((payload) => run(async () => {
      try {
        return await client.post<DownloadPreviewData>("/api/v2/downloads/preview", payload);
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    })));
    if (responses.some((response) => !response)) {
      setPreviewState("error");
      return null;
    }
    const items = responses.map((response, index) => ({ chain: payloads[index].chain, data: response!.data }));
    setPreviews(items);
    setPreview(items[0].data);
    setLastPreview(items[0].data);
    setPreviewFingerprint(fingerprint);
    setPreviewState("idle");
    for (const item of items) await showPreviewDialog(item.data, item.chain);
    return { items, fingerprint };
  }, [buildPayload, chain, client, ensureAddresses, reportIssue, run, showPreviewDialog, validateAll]);

  /** 创建任务：必须先有与当前参数指纹一致的真实预览（§16.3）。 */
  const handleCreate = useCallback(async () => {
    const importedRows = await ensureAddresses();
    if (!importedRows) return;
    const issue = validateAll(importedRows.map((row) => row.normalized));
    if (issue) {
      reportIssue(issue);
      return;
    }
    const groups = groupAddressesByChain(importedRows, chain);
    if (groups.length === 0) {
      reportIssue({ title: "缺少链", message: "每个地址都需要确定一条链后才能创建。" });
      return;
    }
    const payloads = groups.map((group) => buildPayload(group.items.map((item) => item.normalized), group.chain, group.items));
    const payload = payloads[0];
    const fingerprint = JSON.stringify(payloads);
    if (!preview || previewFingerprint !== fingerprint) {
      const result = await handlePreview(importedRows);
      if (!result) return;
    }
    /*
     * 低档位提示（开发文档 §6.4 / §13.1 第 7 条）：
     * 服务端在预览里回 effective_mode 低于请求模式，或给出钳制原因码时，
     * 确认弹窗必须同时显示"请求模式 / 预计实际模式 / 原因"，用户确认后才提交。
     */
    const clamps = previews
      .map((item) => ({ chain: item.chain, clamp: previewModeClamp(item.data, payload.performance_mode) }))
      .filter((item): item is { chain: string; clamp: NonNullable<ReturnType<typeof previewModeClamp>> } => item.clamp !== null);
    const clampDetails = clamps.length === 0
      ? []
      : [
          { label: "请求模式", value: payload.performance_mode },
          {
            label: "预计实际模式",
            value: clamps
              .map((item) => (previews.length > 1 ? item.chain + "：" : "") + (item.clamp.effectiveMode || "服务端未返回"))
              .join("、"),
          },
          {
            label: "低档位原因",
            value:
              [...new Set(clamps.map((item) => item.clamp.reasonText || item.clamp.reasonCode).filter(Boolean))].join("；") ||
              "服务端未返回原因码",
          },
        ];
    const confirm = await dialog.open({
      kind: "confirm",
      title: "确认创建下载任务？",
      body: clamps.length > 0
        ? "服务端预估本次任务无法按请求模式运行，确认后将按上面的实际模式创建任务。"
        : "系统将优先使用已有本地数据，并由服务端再次核验 Coverage、候选任务和 Provider 工作量。",
      details: [
        ...clampDetails,
        { label: "链分组", value: payloads.map((item) => item.chain + ":" + item.addresses.length).join("、") },
        { label: "地址总数", value: String(payloads.reduce((sum, item) => sum + item.addresses.length, 0)) },
        { label: "Dataset", value: payload.datasets.join("、") },
        { label: "范围", value: rangeText(payload) },
        /* 被钳制时上面已经有"请求模式"这一行，这里不再重复同一个值。 */
        ...(clamps.length > 0 ? [] : [{ label: "性能模式", value: payload.performance_mode }]),
        { label: "预览结论", value: previews.some((item) => item.data.provider_work_required) ? "需要新增 Provider 工作" : "将复用本地数据/已有任务" },
        { label: "各组结论", value: previews.map((item) => item.chain + ":" + (item.data.provider_work_required ? "需 Provider" : "复用本地")).join("、") || "—" },
        {
          label: "预估缺口 / 成本",
          value:
            formatRawAmount(String(previews.reduce((sum, item) => sum + item.data.missing_blocks, 0)), 0) + " 区块 / " +
            previews.map((item) => previewEstimatedCost(item.data) ?? "—").join(" + ") + " USD",
        },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "确认创建", result: "create", variant: "primary" },
      ],
    });
    if (confirm !== "create") return;

    type GroupOutcome = { chain: string; ok: boolean; data?: DownloadCreateData; error?: string };
    const outcomes: GroupOutcome[] = [];
    for (const groupPayload of payloads) {
      const idempotencyKey = await stableIdempotencyKey(fingerprint + "|" + groupPayload.chain + "|" + groupPayload.addresses.join(","));
      const response = await run(async () => {
        try {
          return await client.post<DownloadCreateData>("/api/v2/downloads", groupPayload, { idempotencyKey });
        } catch (error) {
          if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
          throw error;
        }
      });
      if (response) outcomes.push({ chain: groupPayload.chain, ok: true, data: response.data });
      else outcomes.push({ chain: groupPayload.chain, ok: false, error: "创建失败（错误详情见上方 Dialog 或 Trace）" });
    }
    if (outcomes.some((item) => item.ok)) setCreatedPulse((value) => value + 1);
    const succeeded = outcomes.filter((item): item is GroupOutcome & { data: DownloadCreateData } => item.ok && item.data !== undefined);
    const failed = outcomes.filter((item) => !item.ok);
    const jobIDs = succeeded.map((item) => item.data.job_id).filter((value): value is string => Boolean(value));
    const allReady = succeeded.length > 0 && succeeded.every((item) => item.data.status === "ready");
    const anyReused = succeeded.some((item) => item.data.reused);
    const title = failed.length === 0
      ? (allReady ? "本地数据已覆盖" : anyReused ? "已复用现有任务" : "下载任务已创建")
      : "部分创建成功（" + succeeded.length + "/" + outcomes.length + "）";
    const answer = await dialog.open({
      kind: failed.length === 0 ? "success" : "warning",
      title,
      body: failed.length === 0
        ? (allReady ? "本地 Coverage 已完整命中，未创建任务、未调用 Provider。" : undefined)
        : "失败组：" + failed.map((item) => item.chain).join("、") + "。已成功的组不会回滚、也不会被自动删除；重新点击创建会复用同一幂等键，已成功组返回既有任务而不是新建。",
      details: [
        ...outcomes.flatMap((item) => item.ok && item.data
          ? [
              { label: item.chain + " status", value: item.data.status },
              { label: item.chain + " created/reused", value: item.data.created + " / " + item.data.reused },
              { label: item.chain + " job", value: (item.data.job_id ?? "—") + (item.data.job_no ? " · " + item.data.job_no : "") },
            ]
          : [
              { label: item.chain + " 失败", value: item.error ?? "创建失败" },
            ]),
        { label: "Provider 工作", value: succeeded.some((item) => item.data.provider_work_required) ? "需要新增" : "无需新增" },
        { label: "Coverage", value: succeeded.map((item) => item.chain + ":" + percent(item.data.coverage)).join("、") || "—" },
        { label: "已创建任务数", value: String(jobIDs.length) },
      ],
      actions: jobIDs.length > 0
        ? [
            {
              label: failed.length > 0 ? "稍后重试失败组" : jobIDs.length === 1 ? "查看任务" : "查看任务中心",
              result: "view",
              variant: "primary" as const,
            },
          ]
        : [{ label: "知道了", result: "ok", variant: "primary" as const }],
    });
    if (answer === "view" && jobIDs.length > 0) navigate(jobIDs.length === 1 && failed.length === 0 ? "/jobs/" + jobIDs[0] : "/jobs");
  }, [buildPayload, chain, client, dialog, ensureAddresses, handlePreview, navigate, preview, previewFingerprint, previews, reportIssue, run, validateAll]);

  /* ---------------------------------------------------------------- 交互副作用 */

  /**
   * 范围类型按能力收敛（开发文档 §5.2 / §13.1）。
   *
   * 能力表可用（服务端返回了 supports_*）：只保留被支持的范围类型，当前类型不被支持时
   * 收敛到"区块范围优先、其次全量、最后时间"的第一项；
   * 能力表不可用：退回旧规则 —— Solana 只支持显式 slot 区间（服务端 solanaRangeGuard 同判）。
   * 收敛原因只走 Dialog，正文不出现提示横幅。
   */
  useEffect(() => {
    if (!chain) return;
    const supported = rangeCapabilities
      ? rangeCapabilities
      : blockOnly
        ? { full: false, time: false, block: true }
        : null;
    if (!supported) return;
    const allowed: RangeType[] = [];
    if (supported.full) allowed.push("full");
    if (supported.time) allowed.push("time");
    if (supported.block) allowed.push("block");
    if (allowed.length === 0 || allowed.includes(rangeType)) return;
    const next: RangeType = allowed.includes("block") ? "block" : allowed[0];
    const label: Record<RangeType, string> = { full: "全量", block: "区块范围", time: "时间范围" };
    setRangeType(next);
    invalidatePreview();
    dialog.notify(
      "info",
      "已切换为" + label[next],
      rangeCapabilities
        ? "所选 Dataset 在当前链（" + chain + "）上不支持「" + label[rangeType] + "」，已切换为「" + label[next] + "」。"
        : "Solana 目前只支持显式 slot（区块）区间：全量与时间区间没有可用的本地索引。",
    );
  }, [blockOnly, chain, dialog, invalidatePreview, rangeCapabilities, rangeType]);

  const onChainChange = useCallback((next: string) => {
    setChain(next);
    invalidatePreview();
  }, [invalidatePreview]);

  const onRangeTypeChange = useCallback((next: RangeType) => {
    setRangeType(next);
    invalidatePreview();
  }, [invalidatePreview]);

  const onPerformanceModeChange = useCallback((next: PerformanceMode) => {
    setPerformanceMode(next);
    invalidatePreview();
  }, [invalidatePreview]);

  const onDatasetsChange = useCallback((next: string[]) => {
    datasetsTouched.current = true;
    setDatasets(next);
    invalidatePreview();
  }, [invalidatePreview]);

  const onDatasetLimitReached = useCallback((label: string) => {
    dialog.notify(
      "warning",
      "Dataset 数量超出上限",
      "单次下载最多选择 6 种 Dataset，无法再加入 " + label + "。请先取消一个已选项。",
    );
  }, [dialog]);

  const resetAddresses = useCallback(() => {
    setPendingFileRows(null);
    setAddressText("");
    setAddresses([]);
    setImported([]);
    setServerVerdict(null);
    setParsedFrom("");
    setPreviews([]);
    invalidatePreview();
  }, [invalidatePreview]);

  /**
   * 模式说明（§12.3）：只走 Dialog。
   *
   * 有预览时展示**本任务真实的 Provider 计划**（provider_plan，一行一个 Provider × 通道），
   * 没有预览时展示三种模式的业务含义，并明确"实际并发由服务端 Provider 档位解析"。
   * 这里**不出现任何写死的并发数值** —— Ankr 的单一并发不是所有链的值（开发文档 §13.1 第 6 条）。
   */
  const modeMeaning: Record<PerformanceMode, string> = useMemo(
    () => ({
      safe: "串行、低频请求，Provider 压力最小，适合首次下载或配额紧张时使用。",
      fast: "有限并发，吞吐明显高于 safe，适合区间较大但可以接受一定并发时使用。",
      turbo: "最大并发，耗时最短，Provider 压力与失败重试成本最高。",
    }),
    [],
  );

  const onModeHelp = useCallback((mode: PerformanceMode) => {
    const plan = (preview ?? lastPreview)?.provider_plan ?? [];
    if (plan.length > 0) {
      void dialog.open({
        kind: "info",
        title: mode + " 性能模式 · Provider 计划",
        body: (
          <div className="dl-dialog">
            <ProviderPlanBlock items={plan} label="本任务 Provider 计划" />
          </div>
        ),
        details: [
          { label: mode + " 模式含义", value: modeMeaning[mode] },
          { label: "并发解析", value: "实际并发由服务端 Provider 档位解析：按 Provider × 通道 × 操作类别取档位，并受额度、管理数据新鲜度与机器背压钳制。" },
        ],
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
      return;
    }
    void dialog.open({
      kind: "info",
      title: mode + " 性能模式",
      body: (
        <div className="dl-dialog">
          <p className="dl-dialog__lead">
            尚无本次预览的 Provider 计划，下面是三种模式的业务含义。实际并发由服务端 Provider 档位解析，前端不展示写死的并发值。
          </p>
        </div>
      ),
      details: [
        { label: "safe", value: modeMeaning.safe },
        { label: "fast", value: modeMeaning.fast },
        { label: "turbo", value: modeMeaning.turbo },
        { label: mode + "（当前选中）", value: modeMeaning[mode] },
      ],
      actions: [{ label: "知道了", result: "ok", variant: "primary" }],
    });
  }, [dialog, lastPreview, modeMeaning, preview]);

  const onOpenCostDetails = useCallback(() => {
    const data = preview ?? lastPreview;
    if (!data) return;
    void dialog.open({
      kind: "info",
      title: "成本与风险说明",
      body: "以下口径与风险全部来自服务端预览接口，不在前端重新估算。",
      details: [
        { label: "估算依据", value: data.estimate.basis },
        { label: "未覆盖区块", value: formatRawAmount(String(data.missing_blocks), 0) },
        { label: "预估缺失区块", value: data.estimate.estimated_missing_blocks === null ? "—" : formatRawAmount(String(data.estimate.estimated_missing_blocks), 0) },
        { label: "预估行数", value: data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0) },
        { label: "预估 CU", value: previewEstimatedCu(data) === null ? "—" : String(previewEstimatedCu(data)) },
        { label: "预估成本 USD", value: previewEstimatedCost(data) ?? "—" },
        { label: "预估耗时（秒）", value: data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds) },
        { label: "Coverage 来源", value: String(data.source.coverage ?? "—") },
        { label: "Provider 来源", value: String(data.source.provider ?? "—") },
        { label: "数据时点", value: String(data.source.as_of ?? "—") },
        ...data.risks.map((risk, index) => ({ label: "风险 " + (index + 1), value: risk })),
      ],
      actions: [{ label: "知道了", result: "ok", variant: "primary" }],
    });
  }, [dialog, lastPreview, preview]);

  /* ---------------------------------------------------------------- 派生视图 */

  const chainOptions: ChainOption[] = useMemo(
    () => chainItems.map((item) => ({ value: item.slug, name: item.name, enabled: item.enabled })),
    [chainItems],
  );
  const currentChain = chainItems.find((item) => item.slug === chain) ?? null;
  const datasetLabels = useMemo(
    () => datasetItems.filter((item) => datasets.includes(item.id)).map((item) => item.label),
    [datasetItems, datasets],
  );
  const coverageItems: PreviewCoverageItem[] = useMemo(() => {
    const data = preview ?? lastPreview;
    return data ? data.coverage.items : [];
  }, [preview, lastPreview]);

  const rangeLabel = rangeType === "full" ? "全量" : rangeType === "block" ? "区块 " + (fromBlock || "—") + " → " + (toBlock || "—") : "时间区间";
  const stale = preview === null && lastPreview !== null;
  const runningStep = previewState === "loading" ? "preview" : busy ? "create" : null;
  const canAttemptCreate = Boolean(chain) && (addresses.length > 0 || localParse.list.length > 0) && datasets.length > 0;
  const addressCount = addresses.length || localParse.list.length;

  /**
   * "查看完整预览明细"（整改规范 §6.10）：主卡放不下的业务字段全部在这里，
   * 值一个都不丢（含逐项 Coverage 明细、来源、数据时点、最近 Trace、估算依据）。
   */
  const onOpenPreviewDetails = useCallback(() => {
    const data = preview ?? lastPreview;
    if (!data) return;
    const details: Array<{ label: string; value: React.ReactNode }> = [
      { label: "链", value: chain || "—" },
      { label: "地址数", value: String(addressCount) },
      { label: "Dataset", value: datasetLabels.length > 0 ? datasetLabels.join("、") : "—" },
      { label: "范围", value: rangeLabel },
      { label: "性能模式", value: performanceMode },
      { label: "整体覆盖率", value: percent(data.coverage.overall_ratio) },
      { label: "未覆盖区块", value: formatRawAmount(String(data.missing_blocks), 0) },
      { label: "预估缺失区块", value: data.estimate.estimated_missing_blocks === null ? "—" : formatRawAmount(String(data.estimate.estimated_missing_blocks), 0) },
      { label: "预估行数", value: data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0) },
      { label: "预估成本 USD", value: data.estimate.estimated_cost_usd ?? "—" },
      { label: "预估耗时（秒）", value: data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds) },
      { label: "估算依据", value: data.estimate.basis },
      { label: "Coverage 命中", value: data.reuse.coverage_hit ? "是" : "否" },
      { label: "将复用现有任务", value: data.reuse.will_reuse ? "是" : "否" },
      { label: "候选任务", value: data.reuse.candidate ? data.reuse.candidate.job_no + " · " + data.reuse.candidate.status + " · " + percent(data.reuse.candidate.coverage) : "无" },
      { label: "Provider 工作", value: data.provider_work_required ? "需要新增" : "无需新增" },
      { label: "Coverage 来源", value: String(data.source.coverage ?? "—") },
      { label: "Provider 来源", value: String(data.source.provider ?? "—") },
      { label: "数据时点", value: String(data.source.as_of ?? "—") },
      { label: "最近 Trace", value: lastTrace ?? "—" },
    ];
    for (const item of data.coverage.items.slice(0, 20)) {
      const head = item.subject.length > 18 ? item.subject.slice(0, 10) + "…" + item.subject.slice(-6) : item.subject;
      details.push({
        label: "Coverage " + head + " · " + item.dataset,
        value:
          percent(item.ratio) + " · 已覆盖 " + formatRawAmount(String(item.covered_blocks), 0) + "/" +
          formatRawAmount(String(item.total_blocks), 0) + " · 缺口 " + item.gaps.length,
      });
    }
    if (data.coverage.items.length > 20) details.push({ label: "其余 Coverage 明细", value: String(data.coverage.items.length - 20) + " 项" });
    for (const [index, risk] of data.risks.entries()) {
      details.push({ label: "风险 " + (index + 1), value: <span className="dl-dialog__risk">{risk}</span> });
    }
    void dialog.open({
      kind: "info",
      title: "完整预览明细",
      body: "以下为服务端预览接口返回的全部业务字段，与任务预览卡上的值同源。",
      details,
      actions: [{ label: "知道了", result: "ok", variant: "primary" }],
    });
  }, [addressCount, chain, datasetLabels, dialog, lastPreview, lastTrace, performanceMode, preview, rangeLabel]);

  const statusBadge = (
    <span className="dl-status">
      已导入 <b>{addresses.length}</b> 个地址
    </span>
  );
  const backAction = (
    <button type="button" className="dl-btn dl-btn--ghost dl-btn--sm" onClick={() => navigate("/jobs")}>
      返回任务中心
    </button>
  );

  /* 步骤状态：轨道节点、章节序号、移动端折叠共用同一判定（§6.2 / §19.1）。 */
  const stepState = (key: DownloadStepKey) => downloadStepState(key, activeStep);
  const collapsedFor = (key: DownloadStepKey) => isMobile && stepState(key) !== "current" && focusStep !== key;

  const addressBody = (
    <DownloadAddressPanel
      addressText={addressText}
      importedCount={addresses.length}
      parsedFrom={parsedFrom}
      serverVerdict={serverVerdict}
      busy={busy}
      recognized={localParse.tokens}
      deduped={localParse.list.length}
      duplicates={localParse.duplicates}
      max={IMPORT_MAX_ADDRESSES}
      fileBusy={importBusy}
      fileName={importFileName}
      onAddressTextChange={(next) => {
        setAddressText(next);
        if (next.trim() !== parsedFrom) {
          setAddresses([]);
          invalidatePreview();
        }
      }}
      onParse={() => void commitAddresses()}
      onFileChange={(file) => void importAddressesFromFile(file)}
      onFileCancel={() => importAbort.current?.abort()}
      onReset={resetAddresses}
      fileInputRef={fileInputRef}
    />
  );

  const scopeBody = (
    <DownloadScopePanel
      chains={chainOptions}
      chain={chain}
      rangeType={rangeType}
      fromBlock={fromBlock}
      toBlock={toBlock}
      fromTime={fromTime}
      toTime={toTime}
      performanceMode={performanceMode}
      busy={busy}
      chainsLoading={chainsApi.loading}
      chainsError={chainsApi.error}
      blockOnly={blockOnly}
      rangeCapabilities={rangeCapabilities}
      onReloadChains={() => void chainsApi.reload()}
      onChainChange={onChainChange}
      onRangeTypeChange={onRangeTypeChange}
      onFromBlockChange={(next) => { setFromBlock(next); invalidatePreview(); }}
      onToBlockChange={(next) => { setToBlock(next); invalidatePreview(); }}
      onFromTimeChange={(next) => { setFromTime(next); invalidatePreview(); }}
      onToTimeChange={(next) => { setToTime(next); invalidatePreview(); }}
      onPerformanceModeChange={onPerformanceModeChange}
      onOpenModeHelp={onModeHelp}
    />
  );

  const datasetBody = (
    <DownloadDatasetGrid
      items={datasetItems}
      values={datasets}
      max={6}
      loading={datasetsApi.loading}
      error={datasetsApi.error}
      busy={busy}
      onChange={onDatasetsChange}
      onRetry={() => void datasetsApi.reload()}
      onLimitReached={onDatasetLimitReached}
    />
  );

  const coverageBody = (
    <DownloadCoveragePanel
      preview={preview}
      lastPreview={lastPreview}
      stale={stale}
      busy={previewState === "loading"}
      coverageItems={coverageItems}
      onPreview={() => void handlePreview()}
      onOpenDetails={onOpenCostDetails}
    />
  );

  const taskPreview = (
    <DownloadTaskPreview
      preview={preview}
      lastPreview={lastPreview}
      stale={stale}
      trace={lastTrace}
      busy={busy}
      canAttemptCreate={canAttemptCreate}
      onCreate={() => void handleCreate()}
      onOpenDetails={onOpenPreviewDetails}
      onPreview={() => void handlePreview()}
      createdToken={createdPulse}
      addressCount={addressCount}
      chainName={currentChain?.name ?? null}
      chainSlug={chain || null}
      datasetLabels={datasetLabels}
      rangeLabel={rangeLabel}
      performanceMode={performanceMode}
    />
  );

  const sections = (
    <>
      <section id="download-step-objects" className="dl-config-section" data-state={stepState("objects")} data-collapsed={collapsedFor("objects") ? "true" : "false"} aria-label="对象与地址">
        <DownloadSectionHead
          index={1}
          title="对象与地址"
          description="选择区块链与输入地址或导入地址列表"
          meta={<>已导入 {addresses.length} 个 · 上限 {IMPORT_MAX_ADDRESSES}</>}
          collapsed={collapsedFor("objects")}
          onEdit={() => jumpToStep("objects")}
        />
        {collapsedFor("objects") ? (
          <div className="dl-step-summary">
            <span className="dl-step-summary__item"><span>链</span>{currentChain ? currentChain.name : "—"}</span>
            <span className="dl-step-summary__item"><span>已导入</span>{addresses.length} 个地址</span>
          </div>
        ) : (
          <div className="dl-section-body">{addressBody}</div>
        )}
      </section>

      <section id="download-step-range" className="dl-config-section" data-state={stepState("range")} data-collapsed={collapsedFor("range") ? "true" : "false"} aria-label="链与范围">
        <DownloadSectionHead
          index={2}
          title="链与范围"
          description="设置下载的区块范围"
          meta={rangeLabel}
          collapsed={collapsedFor("range")}
          onEdit={() => jumpToStep("range")}
        />
        {collapsedFor("range") ? (
          <div className="dl-step-summary">
            <span className="dl-step-summary__item"><span>范围</span>{rangeType === "full" ? "全量" : rangeType === "block" ? "区块范围" : "时间范围"}</span>
            <span className="dl-step-summary__item"><span>性能模式</span>{performanceMode}</span>
          </div>
        ) : (
          <div className="dl-section-body">{scopeBody}</div>
        )}
      </section>

      <section id="download-step-datasets" className="dl-config-section" data-state={stepState("datasets")} data-collapsed={collapsedFor("datasets") ? "true" : "false"} aria-label="Dataset">
        <DownloadSectionHead
          index={3}
          title="Dataset"
          description="选择需要下载的数据集（最多 6 项）"
          meta={<>已选 {datasets.length} / 上限 6</>}
          collapsed={collapsedFor("datasets")}
          onEdit={() => jumpToStep("datasets")}
        />
        {collapsedFor("datasets") ? (
          <div className="dl-step-summary">
            <span className="dl-step-summary__item"><span>已选</span>{datasets.length} / 上限 6</span>
            <span className="dl-step-summary__item">{datasetLabels.join("、") || "—"}</span>
          </div>
        ) : (
          <div className="dl-section-body">{datasetBody}</div>
        )}
      </section>

      <section id="download-step-preview" className="dl-config-section" data-state={stepState("preview")} data-collapsed={collapsedFor("preview") ? "true" : "false"} aria-label="Coverage 与成本预览">
        <DownloadSectionHead
          index={4}
          title="Coverage 预览"
          description="预览所选配置下的覆盖范围与成本"
          meta={stale ? "上次预览 · 已失效" : preview ? "本次预览" : "尚未预览"}
          collapsed={collapsedFor("preview")}
          onEdit={() => jumpToStep("preview")}
        />
        {collapsedFor("preview") ? (
          <div className="dl-step-summary">
            {stale ? (
              /*
               * 失效状态不能只靠颜色、也不能要求用户先展开才知道（REM-008）：
               * 琥珀图标 + "预览已失效" 文案 + 真实"重新预览"入口，灰度截图同样可辨。
               */
              <>
                <span className="dl-stale-chip" role="status">
                  <TriangleAlert size={13} aria-hidden="true" />
                  预览已失效
                </span>
                <button type="button" className="dl-btn dl-btn--sm" onClick={() => void handlePreview()}>
                  重新预览
                </button>
              </>
            ) : (
              <span className="dl-step-summary__item">
                {preview ? "本次预览已生成" : "将根据所选配置生成预览结果"}
              </span>
            )}
          </div>
        ) : (
          <div className="dl-section-body">{coverageBody}</div>
        )}
      </section>
    </>
  );

  /* 第 ⑤ 步 = 右侧任务预览与创建入口（桌面 sticky / 移动端折叠卡）。 */
  const createSection = isMobile ? (
    <section
      id="download-step-create"
      className="dl-config-section dl-config-section--create"
      data-state={stepState("create")}
      data-collapsed={collapsedFor("create") ? "true" : "false"}
      aria-label="创建任务"
    >
      <DownloadSectionHead
        index={5}
        title="创建任务"
        meta={stale ? "需要重新预览" : preview ? "可以创建" : "尚未预览"}
        collapsed={collapsedFor("create")}
        onEdit={() => jumpToStep("create")}
      />
      {collapsedFor("create") ? (
        <div className="dl-step-summary">
          <span className="dl-step-summary__item">确认后将创建数据下载任务</span>
        </div>
      ) : (
        taskPreview
      )}
    </section>
  ) : (
    <section id="download-step-create" className="dl-aside-card" aria-label="创建任务">
      {taskPreview}
    </section>
  );

  return (
    <MotionConfig reducedMotion="user">
    <div className="dl-flow-page">
      {/*
        页面进入（§8.4）：标题 → Flowline（+45ms）→ 工作区（+45ms），总时长 < 420ms；
        不逐卡片 stagger，表单立即可操作（只动 opacity/transform，不阻塞交互）。
        reduced motion 下直接显示最终状态。
      */}
      <motion.div className="dl-page-head" variants={dlPageEnter} initial={enterInitial} animate="show" transition={dlPageEnterStep(reducedMotion, 0)}>
        <CsPageHeader
          title="新建数据下载"
          breadcrumb={[{ label: "任务中心", to: "/jobs" }, { label: "新建数据下载" }]}
          status={statusBadge}
          secondaryAction={backAction}
        />
      </motion.div>

      {!isMobile ? (
        <motion.div className="dl-flow-rail-shell" variants={dlPageEnter} initial={enterInitial} animate="show" transition={dlPageEnterStep(reducedMotion, downloadMotion.stagger)}>
          <DownloadFlowRail
            steps={STEP_ITEMS}
            current={activeStep}
            onJump={jumpToStep}
            reducedMotion={reducedMotion}
            running={runningStep}
            compactLabels={compactRail}
            pulseToken={railPulse}
            pageVisible={pageVisible}
          />
        </motion.div>
      ) : null}

      {isMobile ? (
        <motion.div className="dl-mobile-flow-layout" variants={dlPageEnter} initial={enterInitial} animate="show" transition={dlPageEnterStep(reducedMotion, downloadMotion.stagger * 2)}>
          <DownloadFlowRail
            steps={STEP_ITEMS}
            current={activeStep}
            onJump={jumpToStep}
            orientation="vertical"
            reducedMotion={reducedMotion}
            running={runningStep}
            compactLabels
            pageVisible={pageVisible}
          />
          <div className="dl-mobile-col">
            <main className="dl-config-surface">{sections}</main>
            {createSection}
          </div>
        </motion.div>
      ) : (
        <motion.div className="dl-workspace" variants={dlPageEnter} initial={enterInitial} animate="show" transition={dlPageEnterStep(reducedMotion, downloadMotion.stagger * 2)}>
          <div className="dl-config-viewport">
            <main className="dl-config-surface">{sections}</main>
          </div>
          <aside className="dl-aside">{createSection}</aside>
        </motion.div>
      )}

      {isMobile ? (
        <DownloadMobileActionBar
          busy={previewState === "loading"}
          creating={busy}
          canPreview={canAttemptCreate}
          /* 失效预览不锁按钮：点击创建会先自动重新预览（§16.3），disabled 会让它变成死路。 */
          canAttemptCreate={canAttemptCreate}
          onPreview={() => void handlePreview()}
          onCreate={() => void handleCreate()}
        />
      ) : null}
    </div>
    </MotionConfig>
  );
}
