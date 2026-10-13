import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import { Button as AntButton, Card as AntCard, Checkbox as AntCheckbox, Collapse, DatePicker, Descriptions, Divider, Form, Input, Progress, Radio, Select, Space, Table, Tag, Tabs } from "antd";
import { ProForm } from "@ant-design/pro-components";
import dayjs from "dayjs";
import { Upload, RefreshCw } from "lucide-react";
import {
  Button,
  Card,
  DataTable,
  DatasetSelector,
  EmptyState,
  EntityLink,
  ErrorState,
  Field,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  MetricCard,
  PageSection,
  ParseProgressBody,
  RadioGroup,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  Stepper,
  StickyActionBar,
  dialogInputValue,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import type { KeyValueEntry } from "@chain/ui";
import { formatRawAmount, formatTime, useApi, useS5Action } from "../sprint5/common";
import { IMPORT_FILE_EXTENSIONS, IMPORT_MAX_ADDRESSES, type ImportedAddress, type LocalParseSummary } from "../import/parse-core";
import { ImportParseError, formatParseBudget, importParseTimeoutMs, parseAddressFileInWorker } from "../import/parse-client";
import { pageStateScope, readPageState, writePageState } from "../app/page-state";
import { datasetLabel, reusePolicyLabel } from "../app/business-labels";
import { createDownloadCase, createDownloadTag, listDownloadCases, listDownloadTags, type DownloadMetadataOption } from "./downloads/download.metadata-api";
import { ChainIcon } from "./explorer/AssetIcon";

const chainPriority: Record<string, number> = { bsc: 0, eth: 1, tron: 2 };


/* ============================================================================
 * U-DL-01 /downloads/new：真实顺序 对象 → 链与范围 → Dataset → 复用策略 →
 * Coverage/成本预览 → 确认创建。所有说明/风险/结果只通过 Dialog 呈现。
 *
 * 表现层（蓝图 §12.17）：顶部 Stepper 表达真实填写顺序与当前进度；
 * 桌面为「左侧步骤表单 + 右侧 sticky 预览摘要」（SplitPane，宽度只作本地视觉偏好），
 * ≤899px 时预览摘要移动到提交之前。业务 hook、请求参数与错误语义一律未改。
 * ==========================================================================*/

/* 类型、常量与纯函数已抽到 ./downloads/download.types.ts（规范 §45.1 的目标模块之一）。 */
import { parseAddressInput, groupAddressesByChain, submissionIdempotencyKey, clearSubmissionIdempotencyKey, toIso, percent, rangeText, STEP_ITEMS, datasetRangeCapabilities, previewEstimatedCu, previewEstimatedCost, previewModeClamp } from "./downloads/download.types";
import type {
  ChainMeta,
  DatasetMeta,
  RangeType,
  ReusePolicy,
  PerformanceMode,
  RangeBody,
  DownloadBody,
  PreviewCoverageItem,
  DownloadPreviewData,
  DownloadCreateData,
  ServerAddressVerdict,
  ServerFieldRejection,
  ServerMetaVerdict,
  ServerValidateResult,
} from "./downloads/download.types";

interface DownloadDraft {
  addressText: string; chain: string; imported: ImportedAddress[]; parsedFrom: string;
  rangeType: RangeType; fromBlock: string; toBlock: string; fromTime: string; toTime: string;
  datasets: string[]; reusePolicy: ReusePolicy; performanceMode: PerformanceMode;
  caseName?: string; defaultTags?: string[];
}

/**
 * 断点与 responsive.css 中 SplitPane 的折叠断点一致（≤899px 单列）。
 * 窄屏下预览摘要不再占侧栏，而是移动到提交之前。
 */
function useIsNarrow(): boolean {
  const [narrow, setNarrow] = useState(
    () => typeof window !== "undefined" && window.matchMedia("(max-width: 899px)").matches,
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = window.matchMedia("(max-width: 899px)");
    const update = () => setNarrow(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  return narrow;
}

export default function DownloadNewPage() {
  const { session } = useAuth();
  const [params] = useSearchParams();
  // 切换工作区或来源地址时重新读取对应草稿，旧工作区内容不能写到新作用域。
  return <DownloadForm key={JSON.stringify([pageStateScope(session), params.toString()])} />;
}

function DownloadForm() {
  const dialog = useDialog();
  const isNarrow = useIsNarrow();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();
  const { busy: metadataBusy, run: runMetadata } = useS5Action();
  const { session, can } = useAuth();
  const [params] = useSearchParams();
  const draftKey = `cs.download-draft.v1:${pageStateScope(session)}`;
  const [draft] = useState(() => {
    const saved = readPageState<DownloadDraft | null>(draftKey, null);
    const sourceAddress = params.get("address");
    return saved && !params.has("from_time") && !params.has("to_time") && !params.has("datasets") && (!sourceAddress || (saved.addressText === sourceAddress && saved.chain === params.get("chain"))) ? saved : null;
  });

  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");

  const [addressText, setAddressText] = useState(draft?.addressText ?? params.get("address") ?? "");
  const [addresses, setAddresses] = useState<string[]>([]);
  const [parsedFrom, setParsedFrom] = useState(draft?.parsedFrom ?? "");
  /** 服务端再次验证后的权威条目（含 per-address chain/case/tags）。 */
  const [imported, setImported] = useState<ImportedAddress[]>(draft?.imported ?? []);
  const [serverVerdict, setServerVerdict] = useState<ServerValidateResult | null>(null);
  const [chain, setChain] = useState(draft?.chain ?? params.get("chain") ?? "");
  const [rangeType, setRangeType] = useState<RangeType>(draft?.rangeType ?? (params.has("from_time") && params.has("to_time") ? "time" : "full"));
  const [fromBlock, setFromBlock] = useState(draft?.fromBlock ?? "");
  const [toBlock, setToBlock] = useState(draft?.toBlock ?? "");
  const [fromTime, setFromTime] = useState(draft?.fromTime ?? params.get("from_time") ?? "");
  const [toTime, setToTime] = useState(draft?.toTime ?? params.get("to_time") ?? "");
  const [datasets, setDatasets] = useState<string[]>(draft?.datasets ?? params.get("datasets")?.split(",").filter(Boolean) ?? []);
  const [reusePolicy, setReusePolicy] = useState<ReusePolicy>(draft?.reusePolicy ?? "prefer_local");
  const [performanceMode, setPerformanceMode] = useState<PerformanceMode>(draft?.performanceMode ?? "safe");
  const [caseName, setCaseName] = useState(draft?.caseName ?? "");
  const [defaultTags, setDefaultTags] = useState<string[]>(draft?.defaultTags ?? []);
  const [caseOptions, setCaseOptions] = useState<DownloadMetadataOption[]>([]);
  const [tagOptions, setTagOptions] = useState<DownloadMetadataOption[]>([]);
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets", [chain], chain ? { chain } : undefined);
  const rangeCapabilities = useMemo(() => datasetRangeCapabilities(datasetsApi.data?.items ?? [], datasets), [datasetsApi.data, datasets]);
  const [preview, setPreview] = useState<DownloadPreviewData | null>(null);
  const [previews, setPreviews] = useState<Array<{ chain: string; data: DownloadPreviewData }>>([]);
  const [previewFingerprint, setPreviewFingerprint] = useState<string | null>(null);
  /**
   * 上一次成功的真实预览（服务端原文），**不随参数变化清空**：
   * 参数变化后 preview / previewFingerprint 照旧被清空（可提交资格只看它们），
   * 这里保留一份灰化的对照值，避免用户失去"上一次到底算出什么"的参照。
   */
  const [lastPreview, setLastPreview] = useState<DownloadPreviewData | null>(null);
  const [lastTrace, setLastTrace] = useState<string | null>(null);

  const datasetsTouched = useRef(Boolean(draft?.datasets.length || params.has("datasets")));
  const lastNotifiedError = useRef<string | null>(null);
  const importFileRef = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);
  const importWork = useRef<{ controller: AbortController; review: ReturnType<typeof dialog.openSession> } | null>(null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      importWork.current?.controller.abort();
      importWork.current?.review.dismiss();
    };
  }, []);
  const draftSignature = JSON.stringify({ addressText, chain, imported, parsedFrom, rangeType, fromBlock, toBlock, fromTime, toTime, datasets, reusePolicy, performanceMode, caseName, defaultTags });
  const [submittedDraft, setSubmittedDraft] = useState<string | null>(null);
  const unsaved = Boolean(addressText.trim() || fromBlock || toBlock || fromTime || toTime) && submittedDraft !== draftSignature;
  useEffect(() => {
    if (unsaved) writePageState(draftKey, JSON.parse(draftSignature));
    else { try { sessionStorage.removeItem(draftKey); } catch { /* 存储不可用不影响下载。 */ } }
  }, [draftKey, draftSignature, unsaved]);
  useEffect(() => {
    if (!unsaved) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = ""; };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [unsaved]);
  const leaving = useRef(false);
  const leaveForm = useCallback(async (target: string) => {
    if (leaving.current) return;
    if (unsaved) {
      leaving.current = true;
      const answer = await dialog.confirm({
        title: "离开下载配置？",
        body: "填写内容已保存在当前会话和工作区，返回后可以继续配置；预览和校验结果需要重新确认。",
        actions: [{ label: "继续填写", result: "", variant: "default" }, { label: "离开页面", result: "leave", variant: "primary" }],
      });
      leaving.current = false;
      if (answer !== "leave") return;
    }
    navigate(target);
  }, [dialog, navigate, unsaved]);
  useEffect(() => {
    if (!unsaved) return;
    const intercept = (event: MouseEvent) => {
      if (event.defaultPrevented || event.button !== 0 || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
      const link = event.target instanceof Element ? event.target.closest<HTMLAnchorElement>("a[href]") : null;
      if (!link || link.target === "_blank" || link.hasAttribute("download")) return;
      const target = new URL(link.href, window.location.href);
      if (target.origin !== window.location.origin || (target.pathname === window.location.pathname && target.search === window.location.search)) return;
      const basename = document.querySelector("base")?.getAttribute("href")?.replace(/\/$/, "") || "/app-v2";
      if (!target.pathname.startsWith(basename + "/")) return;
      event.preventDefault(); event.stopPropagation();
      void leaveForm(target.pathname.slice(basename.length) + target.search + target.hash);
    };
    document.addEventListener("click", intercept, true);
    return () => document.removeEventListener("click", intercept, true);
  }, [unsaved, leaveForm]);

  /* 加载错误：ErrorState 只显示通用文案，原始错误正文只允许出现在 Dialog。 */
  useEffect(() => {
    for (const error of [chainsApi.error, datasetsApi.error]) {
      if (error && lastNotifiedError.current !== error) {
        lastNotifiedError.current = error;
        dialog.notify("error", "加载失败", error);
      }
    }
  }, [chainsApi.error, datasetsApi.error, dialog]);

  const chainItems = useMemo(
    () => [...(chainsApi.data?.items ?? [])].sort((a, b) =>
      (chainPriority[a.slug] ?? 3) - (chainPriority[b.slug] ?? 3)),
    [chainsApi.data?.items],
  );
  const datasetItems = datasetsApi.data?.items ?? [];

  useEffect(() => {
    if (chain) return;
    const first = chainItems.find((item) => item.enabled) ?? chainItems[0];
    if (first) setChain(first.slug);
  }, [chain, chainItems]);

  useEffect(() => {
    if (datasetsTouched.current) return;
    const available = datasetItems.filter((item) => item.status === "available").map((item) => item.id);
    if (available.length === 0) return;
    const preferred = ["transactions", "token_transfers"].filter((id) => available.includes(id));
    setDatasets(preferred.length > 0 ? preferred : [available[0]]);
  }, [datasetItems]);

  useEffect(() => {
    if (datasetsApi.loading || !datasetsApi.data) return;
    const available = new Set(datasetItems.filter(item => item.status === "available").map(item => item.id));
    setDatasets(current => current.every(id => available.has(id)) ? current : current.filter(id => available.has(id)));
  }, [datasetsApi.data, datasetsApi.loading]);

  const loadMetadata = useCallback(async () => {
    const result = await runMetadata(() => Promise.all([listDownloadCases(client), listDownloadTags(client)]));
    if (result) { setCaseOptions(result[0]); setTagOptions(result[1]); }
  }, [client, runMetadata]);
  const createMetadata = useCallback(async (kind: "case" | "tag") => {
    if (!can(kind === "case" ? "case:create" : "case:update")) return;
    const name = dialogInputValue(await dialog.prompt({ title: kind === "case" ? "新建案件" : "新建标签", input: { label: "名称", pattern: kind === "case" ? /^.{1,200}$/ : /^.{1,120}$/ }, submitLabel: "创建" }));
    if (!name) return;
    const created = await runMetadata(() => kind === "case" ? createDownloadCase(client, name) : createDownloadTag(client, name));
    if (!created) return;
    if (kind === "case") { setCaseOptions(current => [...current.filter(item => item.id !== created.id), created]); setCaseName(created.name); }
    else { setTagOptions(current => [...current.filter(item => item.id !== created.id), created]); setDefaultTags(current => [...new Set([...current, created.name])].slice(0, 20)); }
    setPreview(null); setPreviews([]); setPreviewFingerprint(null);
  }, [can, client, dialog, runMetadata]);

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
      const body: DownloadBody = { chain: groupChain, addresses: addrs, datasets, range, reuse_policy: reusePolicy, performance_mode: performanceMode, create_missing_meta: false };
      if (caseName) body.case_name = caseName;
      if (defaultTags.length) body.tags = defaultTags;
      if (Object.keys(subjectMeta).length > 0) body.subject_meta = subjectMeta;
      return body;
    },
    [caseName, defaultTags, datasets, fromBlock, fromTime, performanceMode, rangeType, reusePolicy, toBlock, toTime],
  );

  const importedAddressText = parsedFrom !== "" && parsedFrom === addressText.trim();

  // 见 groupAddressesByChain：纯函数，必须用**本次调用返回的 rows**，不能读闭包里的旧 state。

  /**
   * 服务端再次验证（§18.10 1042）：本地只做拆分与摘要，接受/拒绝与规范化地址
   * 一律以服务端 verdict 为准；被拒条目在 Dialog 中原样呈现，不静默丢弃。
   */
  const revalidateOnServer = useCallback(async (rows: ImportedAddress[], signal?: AbortSignal, beforeError?: () => void): Promise<ServerValidateResult | null> => {
    const response = await run(async () => {
      try {
        return await client.post<ServerValidateResult>("/api/v2/downloads/validate-addresses", {
          chain,
          case_name: caseName,
          tags: defaultTags,
          items: rows.map((row) => ({ address: row.input, chain: row.chain, case: row.caseName, tags: row.tags })),
          create_missing_meta: false,
        }, { signal });
      } catch (error) {
        beforeError?.();
        if (signal?.aborted) return null;
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    return response && !signal?.aborted ? response.data : null;
  }, [caseName, defaultTags, chain, client, run]);

  const applyServerVerdict = useCallback(async (
    verdict: ServerValidateResult,
    localDetails: Array<{ label: string; value: string }> = [],
    review?: ReturnType<typeof dialog.openSession>,
  ): Promise<ImportedAddress[] | null> => {
    // 只有本工作区**确实存在**（resolved）或本次已获授权创建（will_create）的案件/标签
    // 才会留在 payload 里。否则 create_missing_meta=false 时 EnsureLinked 会以
    // "case/tag not found" 直接失败——文案说"不绑定"但实际仍提交是不可接受的。
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
      { label: "链分布", value: Object.entries(verdict.summary.chains).map(([k, v]) => `${k}:${v}`).join("、") || "—" },
      { label: "作用域", value: `org ${verdict.scope.organization_id.slice(0, 8)}… / ws ${verdict.scope.workspace_id.slice(0, 8)}…` },
      { label: "校验来源", value: verdict.source },
      { label: "本次会话权限", value: `download:create=${verdict.grants["download:create"]} · case:create=${verdict.grants["case:create"]} · case:update=${verdict.grants["case:update"]}` },
    ];
    const metaText = (item: ServerMetaVerdict): string => {
      switch (item.status) {
        case "resolved": return `本工作区已存在（${((item.case_id ?? item.tag_id) ?? "").slice(0, 8)}…）`;
        case "will_create": return "本工作区不存在，create_missing_meta=true 且已授权创建";
        case "denied": return `本工作区不存在，且没有创建权限（需 ${item.required_permission ?? "—"}）—— 已从本次提交中移除`;
        default: return "本工作区不存在 —— 已从本次提交中移除，不会出现在创建请求里";
      }
    };
    for (const item of verdict.meta.cases) details.push({ label: `案件 ${item.name}`, value: metaText(item) });
    for (const item of verdict.meta.tags) details.push({ label: `标签 ${item.name}`, value: metaText(item) });
    verdict.rejected.slice(0, 20).forEach((item) => {
      details.push({ label: `拒绝 第${item.row}行 ${item.code}`, value: item.input || "（空）" });
    });
    if (verdict.rejected.length > 20) {
      details.push({ label: "其余拒绝条目", value: `${verdict.rejected.length - 20} 条` });
    }
    for (const item of verdict.field_rejections ?? []) {
      details.push({
        label: `字段拒绝 第${item.row}行 ${item.field} ${item.code}`,
        value: `${item.message}（${item.count}/${item.limit}）`,
      });
    }
    if ((verdict.summary.tags_over_limit ?? 0) > 0) {
      details.push({ label: "标签超额行", value: `${verdict.summary.tags_over_limit} 行；这些行不会绑定任何标签（非静默截断）` });
    }
    if ((verdict.summary.meta_denied ?? 0) > 0) {
      details.push({ label: "元数据创建被拒", value: `${verdict.summary.meta_denied} 项：当前会话无创建权限` });
    }
    if (droppedCases.size > 0) {
      details.push({ label: "本次不绑定的案件", value: [...droppedCases].join("、") });
    }
    if (droppedTags.size > 0) {
      details.push({ label: "本次不绑定的标签", value: [...droppedTags].join("、") });
    }
    if (droppedCases.size > 0 || droppedTags.size > 0) {
      details.push({ label: "实际提交", value: "上述案件和标签不会加入创建请求" });
    }
    const reviewRequest: Parameters<typeof dialog.open>[0] = {
      kind: accepted.length === 0 ? "error" : verdict.rejected.length > 0 || droppedCases.size > 0 || droppedTags.size > 0 || localDetails.some((item) => item.label === "超出单次上限") || (verdict.field_rejections?.length ?? 0) > 0 ? "warning" : "success",
      title: "地址导入校验结果",
      body: accepted.length === 0
        ? "服务端未接受任何地址，请修正文件后重试。"
        : "仅导入服务端接受并规范化的地址。拒绝地址及案件、标签的处理结果如下。",
      // 摘要与拒绝、权限处理结果优先，文件计数和技术字段排在后面。
      details: [...details.slice(0, 4), ...details.slice(7), ...localDetails, ...details.slice(4, 7)],
      actions: accepted.length === 0
        ? [{ label: "知道了", result: "ok", variant: "primary" }]
        : [
            { label: "取消", result: "", variant: "default" },
            { label: "使用服务端结果", result: "use", variant: "primary" },
          ],
    };
    if (review) review.update(reviewRequest);
    const answer = await (review ? review.result : dialog.open(reviewRequest));
    if (answer !== "use" || accepted.length === 0) return null;
    setImported(accepted);
    setAddresses(accepted.map((item) => item.normalized));
    setServerVerdict(verdict);
    const normalizedText = accepted.map((item) => item.normalized).join("\n");
    setAddressText(normalizedText);
    setParsedFrom(normalizedText);
    setPreview(null);
    setPreviews([]);
    setPreviewFingerprint(null);
    return accepted;
  }, [dialog]);

  const importAddresses = useCallback(async (): Promise<ImportedAddress[] | null> => {
    const parsed = parseAddressInput(addressText);
    if (parsed.tokens === 0) {
      dialog.notify("warning", "没有可导入的地址", "请粘贴至少一个地址，支持换行、逗号、分号或空白分隔。");
      return null;
    }
    const capped = parsed.list.slice(0, IMPORT_MAX_ADDRESSES);
    const details = [
      { label: "原始条目", value: String(parsed.tokens) },
      { label: "本地去重后", value: String(parsed.list.length) },
      { label: "重复条目", value: String(parsed.duplicates) },
      { label: "本次送校验", value: `${capped.length} 个地址` },
    ];
    if (parsed.list.length > IMPORT_MAX_ADDRESSES) {
      details.push({ label: "超出单次上限", value: `${parsed.list.length - IMPORT_MAX_ADDRESSES} 个未导入（上限 ${IMPORT_MAX_ADDRESSES}）` });
    }
    const controller = new AbortController();
    const review = dialog.openSession({
      kind: "info",
      title: "正在校验地址",
      body: "本地解析已完成，正在核对地址、链和当前工作区权限。",
      details,
      actions: [{ label: "取消导入", result: "cancel", variant: "default" }],
    });
    importWork.current = { controller, review };
    void review.result.then((answer) => {
      if (importWork.current?.controller === controller) importWork.current = null;
      if (answer !== "use") controller.abort();
    });
    const verdict = await revalidateOnServer(capped.map((value) => ({ input: value, normalized: "", chain: "", caseName: "", tags: [] })), controller.signal, review.dismiss);
    if (!verdict) { review.dismiss(); return null; }
    return applyServerVerdict(verdict, details, review);
  }, [addressText, dialog, revalidateOnServer, applyServerVerdict]);

  const importAddressesFromFile = useCallback(async (file: File): Promise<ImportedAddress[] | null> => {
    // 计划 1852 / §23.7：解析在隔离 Worker 中执行，主线程只等消息；
    // 有界超时到点即 terminate（真停止，不是"忽略结果"），用户也可随时取消。
    const timeoutMs = importParseTimeoutMs();
    const controller = new AbortController();
    // 进度与取消的唯一入口是弹窗（AGENTS.md：提示不得嵌入页面）。
    const review = dialog.openSession({
      kind: "info",
      title: "正在隔离解析文件",
      body: (
        <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-4)" }}>
          <p style={{ margin: 0 }}>
            {`${file.name} 在 Web Worker 中解析，页面保持可交互；超过 ${formatParseBudget(timeoutMs)}会自动中止，也可以立即取消。`}
          </p>
          <ParseProgressBody parsedRows={0} totalRows={null} accepted={0} rejected={0} duplicates={0} state="running" />
        </div>
      ),
      details: [
        { label: "文件名", value: file.name },
        { label: "文件大小", value: `${file.size} 字节` },
      ],
      actions: [{ label: "取消解析", result: "cancel", variant: "primary" }],
    });
    importWork.current = { controller, review };
    void review.result.then((answer) => {
      if (importWork.current?.controller === controller) importWork.current = null;
      if (answer !== "use") controller.abort();
    });
    let parsed: { rows: ImportedAddress[]; summary: LocalParseSummary };
    try {
      const result = await parseAddressFileInWorker(file, chain, { signal: controller.signal, timeoutMs });
      parsed = { rows: result.rows, summary: result.summary };
    } catch (error) {
      review.dismiss();
      if (!mounted.current) return null;
      const failure = error instanceof ImportParseError ? error : null;
      if (failure?.code === "PARSE_CANCELLED") {
        dialog.notify("warning", "解析已取消", `${file.name} 的解析已终止（Worker 已停止），未导入任何地址。`);
      } else if (failure?.code === "PARSE_TIMEOUT") {
        dialog.notify(
          "error",
          "解析超时已中止",
          `${file.name} 在 ${formatParseBudget(timeoutMs)}内没有完成，解析 Worker 已被终止（未导入任何地址）。请拆分文件或减少行数后重试。`,
        );
      } else if (failure?.code === "FORMAT_REJECTED") {
        dialog.notify("error", "文件解析失败", `无法读取 ${file.name}：${failure.message}`);
      } else {
        dialog.notify("error", "文件解析失败", `无法读取 ${file.name}：${failure ? failure.message : String(error)}`);
      }
      return null;
    }
    if (controller.signal.aborted) { review.dismiss(); return null; }
    const { rows, summary } = parsed;
    const details = [
      { label: "文件名", value: file.name },
      { label: "文件类型", value: file.name.toLowerCase().endsWith(".csv") ? "CSV" : "XLSX/Excel" },
      { label: "扫描行数", value: String(summary.rows) },
      { label: "空白行", value: String(summary.blank) },
      { label: "识别表头", value: summary.usedHeaderRow ? "address 表头（列映射）" : "无表头（整表当地址清单）" },
      { label: "可选列", value: [summary.chainColumn ? "chain" : null, summary.caseColumn ? "case" : null, summary.tagColumn ? "tags" : null].filter(Boolean).join("、") || "无" },
      { label: "链分布（本地）", value: Object.entries(summary.perChain).map(([k, v]) => `${k}:${v}`).join("、") || "—" },
      { label: "带案件/标签的行", value: String(summary.metaRows) },
      { label: "本次送校验", value: `${rows.length} 个地址` },
    ];
    if (summary.truncated > 0) {
      details.push({ label: "超出单次上限", value: `${summary.truncated} 行未导入（上限 ${IMPORT_MAX_ADDRESSES}）` });
    }
    review.update({
      kind: rows.length === 0 || summary.truncated > 0 ? "warning" : "info",
      title: rows.length === 0 ? "文件中没有可导入地址" : "正在校验地址",
      body: rows.length === 0
        ? "文件里没有可用的地址行，请检查文件内容与列名。"
        : "文件解析已完成，正在核对地址、链和当前工作区权限。",
      details,
      actions: rows.length === 0
        ? [{ label: "知道了", result: "ok", variant: "primary" }]
        : [{ label: "取消导入", result: "cancel", variant: "default" }],
    });
    if (rows.length === 0) { await review.result; return null; }
    const verdict = await revalidateOnServer(rows, controller.signal, review.dismiss);
    if (!verdict) { review.dismiss(); return null; }
    return applyServerVerdict(verdict, details, review);
  }, [applyServerVerdict, chain, dialog, revalidateOnServer]);

  const ensureAddresses = useCallback(async (): Promise<ImportedAddress[] | null> => {
    if (imported.length > 0 && importedAddressText) {
      if (serverVerdict) return imported;
      const verdict = await revalidateOnServer(imported);
      return verdict ? applyServerVerdict(verdict) : null;
    }
    return importAddresses();
  }, [applyServerVerdict, importAddresses, imported, importedAddressText, revalidateOnServer, serverVerdict]);

  const validateForm = useCallback(
    (addrs: string[]): boolean => {
      if (!chain) {
        dialog.notify("warning", "请选择链", "链选项来自服务端 /api/v2/meta/chains，未加载时无法提交。");
        return false;
      }
      if (addrs.length === 0) {
        dialog.notify("warning", "请导入地址", "至少需要一个地址才能创建下载。");
        return false;
      }
      if (addrs.length > 100) {
        dialog.notify("warning", "地址数量超出上限", "单次下载最多 100 个地址，请拆分后重试。");
        return false;
      }
      if (datasets.length === 0) {
        dialog.notify("warning", "请选择数据集", "至少选择一个数据集。");
        return false;
      }
      if (datasets.length > 6) {
        dialog.notify("warning", "数据集数量超出上限", "单次下载最多选择 6 个数据集。");
        return false;
      }
      if ((rangeCapabilities && !rangeCapabilities[rangeType]) || (!rangeCapabilities && chain === "solana" && rangeType !== "block")) {
        dialog.notify("warning", "范围类型不可用", "所选数据集不支持当前范围类型，请更改范围或数据集。");
        return false;
      }
      if (rangeType === "block") {
        const from = Number(fromBlock);
        const to = Number(toBlock);
        if (fromBlock.trim() === "" || toBlock.trim() === "" || !Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to < 0) {
          dialog.notify("warning", "区块范围不合法", "请填写从 0 开始的整数区块高度。");
          return false;
        }
        if (from > to) {
          dialog.notify("warning", "区块范围不合法", "起始区块不能大于结束区块。");
          return false;
        }
      }
      if (rangeType === "time") {
        const from = toIso(fromTime);
        const to = toIso(toTime);
        if (!from || !to) {
          dialog.notify("warning", "时间范围不合法", "请选择合法的起始与结束时间。");
          return false;
        }
        if (new Date(from).getTime() > new Date(to).getTime()) {
          dialog.notify("warning", "时间范围不合法", "起始时间不能晚于结束时间。");
          return false;
        }
      }
      return true;
    },
    [chain, datasets.length, dialog, fromBlock, fromTime, rangeCapabilities, rangeType, toBlock, toTime],
  );

  const previewDetails = useCallback((data: DownloadPreviewData) => {
    const details: Array<{ label: string; value: string }> = [
      { label: "复用策略", value: data.reuse.policy },
      { label: "数据覆盖命中", value: data.reuse.coverage_hit ? "是" : "否" },
      { label: "将复用现有任务", value: data.reuse.will_reuse ? "是" : "否" },
      { label: "provider_work_required", value: String(data.provider_work_required) },
      { label: "整体覆盖率", value: percent(data.coverage.overall_ratio) },
      { label: "未覆盖区块", value: formatRawAmount(String(data.missing_blocks), 0) },
      { label: "预估行数", value: data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0) },
      { label: "预估成本 USD", value: data.estimate.estimated_cost_usd ?? "—" },
      { label: "预估耗时（秒）", value: data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds) },
      { label: "估算依据", value: data.estimate.basis },
      { label: "预计 CU", value: String(previewEstimatedCu(data) ?? "—") },
      { label: "预计实际模式", value: previewModeClamp(data, data.request.performance_mode)?.effectiveMode ?? data.effective_mode ?? "—" },
    ];
    for (const item of data.provider_plan ?? []) details.push({ label: `${item.provider} / ${item.api_channel}`, value: `页数 ${item.estimated_pages ?? "—"} · RPC 调用 ${item.estimated_rpc_calls ?? "—"} · CU ${item.estimated_cu ?? "—"} · USD ${item.estimated_cost_usd ?? "—"}` });
    if (data.reuse.candidate) {
      details.splice(3, 0, {
        label: "候选任务",
        value: `${data.reuse.candidate.job_no} · ${data.reuse.candidate.status} · ${percent(data.reuse.candidate.coverage)}`,
      });
    }
    data.risks.forEach((risk, index) => details.push({ label: `风险 ${index + 1}`, value: risk }));
    return details;
  }, []);

  const showPreviewDialog = useCallback(
    async (data: DownloadPreviewData, groupChain?: string) => {
      const details = groupChain ? [{ label: "链", value: groupChain }, ...previewDetails(data)] : previewDetails(data);
      if (!data.provider_work_required) {
        await dialog.open({
          kind: "info",
          title: "无需新增 Provider 请求",
          body: "将复用本地数据/已有任务，不新增 Provider 请求。",
          details,
          actions: [{ label: "知道了", result: "ok", variant: "primary" }],
        });
        return;
      }
      await dialog.open({
        kind: data.risks.length > 0 ? "warning" : "success",
        title: data.risks.length > 0 ? "预览完成，存在风险提示" : "数据覆盖与成本预览完成",
        body: data.risks.length > 0 ? "以下风险由服务端预览接口返回，请确认后再创建任务。" : undefined,
        details,
        actions: [{ label: "知道了", result: "ok", variant: "primary" }],
      });
    },
    [dialog, previewDetails],
  );

  const handlePreview = useCallback(async (): Promise<{ items: Array<{ chain: string; data: DownloadPreviewData }>; fingerprint: string } | null> => {
    const importedRows = await ensureAddresses();
    if (!importedRows) return null;
    if (!validateForm(importedRows.map((row) => row.normalized))) return null;
    // 必须用本次返回的 rows 直接分组：首次"预览"会触发导入并 setState，
    // 此刻组件 state 仍是旧值，读闭包会提交被服务端驳回之前的旧集合。
    const groups = groupAddressesByChain(importedRows, chain);
    if (groups.length === 0) {
      dialog.notify("warning", "请选择链", "每个地址都需要确定一条链后才能预览。");
      return null;
    }
    // 契约 1043：一次请求只接受同一条链，因此按链分组分别预览。
    const payloads = groups.map((group) => buildPayload(group.items.map((item) => item.normalized), group.chain, group.items));
    const fingerprint = JSON.stringify(payloads);
    const responses = await Promise.all(payloads.map((payload) => run(async () => {
      try {
        return await client.post<DownloadPreviewData>("/api/v2/downloads/preview", payload);
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    })));
    if (responses.some((response) => !response)) return null;
    const items = responses.map((response, index) => ({ chain: payloads[index].chain, data: response!.data }));
    setPreviews(items);
    setPreview(items[0].data);
    setLastPreview(items[0].data);
    setPreviewFingerprint(fingerprint);
    for (const item of items) await showPreviewDialog(item.data, item.chain);
    return { items, fingerprint };
  }, [buildPayload, chain, client, dialog, ensureAddresses, run, showPreviewDialog, validateForm]);

  const handleCreate = useCallback(async () => {
    const importedRows = await ensureAddresses();
    if (!importedRows) return;
    if (!validateForm(importedRows.map((row) => row.normalized))) return;
    const groups = groupAddressesByChain(importedRows, chain);
    if (groups.length === 0) {
      dialog.notify("warning", "请选择链", "每个地址都需要确定一条链后才能创建。");
      return;
    }
    const payloads = groups.map((group) => buildPayload(group.items.map((item) => item.normalized), group.chain, group.items));
    const payload = payloads[0];
    const fingerprint = JSON.stringify(payloads);
    if (!preview || previewFingerprint !== fingerprint) {
      dialog.notify("info", "需要先完成真实预览", "创建前必须调用 /api/v2/downloads/preview 确认 数据覆盖与成本，参数变化后会重新预览。");
      await handlePreview();
      return;
    }
    const confirm = await dialog.open({
      kind: "confirm",
      title: "确认创建下载任务？",
      body:
        payload.reuse_policy === "refresh"
          ? "refresh 将新增 Provider 取数工作并产生成本，不复用本地已完成覆盖。"
          : "默认 prefer_local：优先复用本地数据或已有任务，服务端会再次校验 数据覆盖与复用候选。",
      details: [
        { label: "链分组", value: payloads.map((item) => `${item.chain}:${item.addresses.length}`).join("、") },
        { label: "地址总数", value: String(payloads.reduce((sum, item) => sum + item.addresses.length, 0)) },
        { label: "数据集", value: payload.datasets.join("、") },
        { label: "范围", value: rangeText(payload) },
        { label: "复用策略", value: payload.reuse_policy },
        { label: "性能模式", value: payload.performance_mode },
        { label: "预览结论", value: previews.some((item) => item.data.provider_work_required) ? "需要新增 Provider 工作" : "将复用本地数据/已有任务" },
        { label: "各组结论", value: previews.map((item) => `${item.chain}:${item.data.provider_work_required ? "需 Provider" : "复用本地"}`).join("、") || "—" },
        {
          label: "预估缺口 / 成本",
          value: `${formatRawAmount(String(previews.reduce((sum, item) => sum + item.data.missing_blocks, 0)), 0)} 区块 / ${previews.map((item) => item.data.estimate.estimated_cost_usd ?? "—").join(" + ")} USD`,
        },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "确认创建", result: "create", variant: "primary" },
      ],
    });
    if (confirm !== "create") return;

    // 多链是**多个独立请求**：某一组失败不能抹掉已经真实创建的组。
    // An unfinished multi-chain submission keeps its keys for safe retry.
    // A later deliberate submission gets fresh keys after every group succeeds.
    type GroupOutcome = { chain: string; ok: boolean; data?: DownloadCreateData; error?: string };
    const outcomes: GroupOutcome[] = [];
    const scopes = payloads.map((groupPayload) => `${fingerprint}|${groupPayload.chain}|${groupPayload.addresses.join(",")}`);
    for (const [index, groupPayload] of payloads.entries()) {
      const idempotencyKey = await submissionIdempotencyKey(scopes[index]);
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
    const succeeded = outcomes.filter((item): item is GroupOutcome & { data: DownloadCreateData } => item.ok && item.data !== undefined);
    const failed = outcomes.filter((item) => !item.ok);
    if (failed.length === 0) {
      await Promise.all(scopes.map(clearSubmissionIdempotencyKey));
      setSubmittedDraft(draftSignature);
    }
    const jobIDs = succeeded.map((item) => item.data.job_id).filter((value): value is string => Boolean(value));
    const allReady = succeeded.length > 0 && succeeded.every((item) => item.data.status === "ready");
    const anyReused = succeeded.some((item) => item.data.reused);
    const title = failed.length === 0
      ? (allReady ? "本地数据已覆盖" : anyReused ? "已复用现有任务" : "下载任务已创建")
      : `部分创建成功（${succeeded.length}/${outcomes.length}）`;
    const answer = await dialog.open({
      kind: failed.length === 0 ? "success" : "warning",
      title,
      body: failed.length === 0
        ? (allReady ? "本地数据覆盖已完整命中，未创建任务、未调用 Provider。" : undefined)
        : `失败组：${failed.map((item) => item.chain).join("、")}。已成功的组不会回滚、也不会被自动删除；重新点击创建会复用同一幂等键，已成功组返回既有任务而不是新建。`,
      details: [
        ...outcomes.flatMap((item) => item.ok && item.data
          ? [
              { label: `${item.chain} status`, value: item.data.status },
              { label: `${item.chain} created/reused`, value: `${item.data.created} / ${item.data.reused}` },
              { label: `${item.chain} job`, value: `${item.data.job_id ?? "—"}${item.data.job_no ? ` · ${item.data.job_no}` : ""}` },
            ]
          : [
              { label: `${item.chain} 失败`, value: item.error ?? "创建失败" },
            ]),
        { label: "provider_work_required", value: String(succeeded.some((item) => item.data.provider_work_required)) },
        { label: "coverage", value: succeeded.map((item) => `${item.chain}:${percent(item.data.coverage)}`).join("、") || "—" },
        { label: "已创建任务数", value: String(jobIDs.length) },
      ],
      actions: jobIDs.length > 0
        ? [
            { label: failed.length > 0 ? "稍后重试失败组" : jobIDs.length === 1 ? "查看任务" : "查看任务中心", result: "view", variant: "primary" as const },
          ]
        : [{ label: "知道了", result: "ok", variant: "primary" as const }],
    });
    if (answer === "view" && jobIDs.length > 0) navigate(jobIDs.length === 1 && failed.length === 0 ? `/jobs/${jobIDs[0]}` : "/jobs");
  }, [buildPayload, chain, client, dialog, draftSignature, ensureAddresses, handlePreview, navigate, preview, previewFingerprint, previews, run, validateForm]);

  const chooseReusePolicy = useCallback(
    async (next: ReusePolicy) => {
      if (next === "prefer_local") {
        setReusePolicy("prefer_local");
        setPreview(null);
        setPreviewFingerprint(null);
        return;
      }
      const impact = preview
        ? `当前预览：未覆盖 ${formatRawAmount(String(preview.missing_blocks), 0)} 个区块，预估成本 ${preview.estimate.estimated_cost_usd ?? "未知"} USD。`
        : "当前尚未预览，实际 Provider 工作与成本以预览和执行结果为准。";
      const answer = await dialog.open({
        kind: "confirm",
        title: "确认改用 refresh？",
        body: `refresh 会跳过本地 Coverage 复用，新增 Provider 取数工作并产生成本。${impact}`,
        details: [
          { label: "当前策略", value: reusePolicy },
          { label: "目标策略", value: "refresh" },
        ],
        actions: [
          { label: "保持 prefer_local", result: "keep", variant: "default" },
          { label: "确认使用 refresh", result: "refresh", variant: "danger" },
        ],
      });
      if (answer === "refresh") {
        setReusePolicy("refresh");
        setPreview(null);
        setPreviewFingerprint(null);
      } else {
        setReusePolicy("prefer_local");
      }
    },
    [dialog, preview, reusePolicy],
  );

  /** 参数变化统一失效：可提交资格只由 preview + previewFingerprint 决定（语义与原来一致）。 */
  const invalidatePreview = useCallback(() => {
    setPreview(null);
    setPreviewFingerprint(null);
  }, []);

  const toggleDataset = useCallback((next: string[]) => {
    if (next.length > 6) {
      dialog.notify("warning", "数据集数量超出上限", "单次下载最多选择 6 个数据集。");
      return;
    }
    datasetsTouched.current = true;
    setDatasets(next);
    setPreview(null);
    setPreviewFingerprint(null);
  }, [dialog]);

  const resetAddresses = useCallback(() => {
    setAddressText("");
    setAddresses([]);
    setImported([]);
    setServerVerdict(null);
    setParsedFrom("");
    setPreview(null);
    setPreviews([]);
    setPreviewFingerprint(null);
  }, []);

  /* ---------------------------------------------------------------- 派生视图 */


  const previewGroups = preview ? (previews.length ? previews : [{ chain, data: preview }]) : [];
  const coverageRows = previewGroups.flatMap(group => group.data.coverage.items.map((item, index) => ({
    ...item, chain: group.chain, key: `${group.chain}-${item.subject}-${item.dataset}-${index}`,
  })));
  const localTimeZone = Intl.DateTimeFormat().resolvedOptions().timeZone;

  return (
    <div className="cs-workspace-focus cs-download-page">
      <CsPageHeader
        title="新建下载"
        breadcrumb={[{ label: "数据工具" }, { label: "数据下载" }]}
        secondaryAction={<AntButton type="link" onClick={() => void leaveForm("/jobs")}>下载任务</AntButton>}
      />
      <div className="cs-download-layout">
        <AntCard className="cs-download-form" bordered>
          <Tabs activeKey="new" items={[
            { key: "new", label: "新建下载" },
            { key: "import", label: "批量导入" },
          ]} onChange={key => { if (key === "import") importFileRef.current?.click(); }} />
          <ProForm layout="vertical" submitter={false} onFinish={async () => { await handlePreview(); }}>
            <h2 className="cs-form-section-title">下载对象</h2>
            <div className="cs-download-object-toolbar">
              <Form.Item label="区块链" htmlFor="download-chain">
                <Select id="download-chain" value={chain || undefined} placeholder="请选择链" loading={chainsApi.loading}
                  options={chainItems.map(item => ({ value: item.slug, label: <Space size={8}><span aria-hidden="true"><ChainIcon chain={item.slug} /></span>{item.name}</Space>, disabled: !item.enabled }))}
                  onChange={value => { setChain(value); invalidatePreview(); }} />
              </Form.Item>
              <AntButton icon={<Upload size={16} />} onClick={() => importFileRef.current?.click()} disabled={busy}>导入 CSV / XLSX</AntButton>
              <input ref={importFileRef} type="file" accept={IMPORT_FILE_EXTENSIONS.join(",")} aria-label="导入地址文件" hidden
                onChange={event => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void importAddressesFromFile(file); }} />
            </div>
            <Form.Item label="地址列表" htmlFor="download-addresses">
              <Input.TextArea id="download-addresses" value={addressText} autoSize={{ minRows: 4, maxRows: 10 }}
                placeholder="每行一个地址，或以逗号、分号、空白分隔"
                onChange={event => { setAddressText(event.target.value); if (event.target.value.trim() !== parsedFrom) { setAddresses([]); invalidatePreview(); } }} />
            </Form.Item>
            <div className="cs-download-import-actions">
              <Space><AntButton onClick={() => void importAddresses()} disabled={busy}>解析导入</AntButton>
                <AntButton type="text" onClick={resetAddresses} disabled={!addressText.trim() && !addresses.length}>清空</AntButton></Space>
              <span data-testid="server-verdict-summary">已导入 {addresses.length} / {IMPORT_MAX_ADDRESSES} 个地址</span>
            </div>
            <Divider />
            <h2 className="cs-form-section-title">下载范围</h2>
            <Form.Item label="范围类型" htmlFor="download-range-type">
              <Radio.Group id="download-range-type" value={rangeType} onChange={event => { setRangeType(event.target.value as RangeType); invalidatePreview(); }}>
                <Radio value="full" disabled={rangeCapabilities ? !rangeCapabilities.full : chain === "solana"}>全部历史</Radio><Radio value="time" disabled={rangeCapabilities ? !rangeCapabilities.time : chain === "solana"}>时间区间</Radio><Radio value="block" disabled={rangeCapabilities ? !rangeCapabilities.block : false}>{chain === "solana" ? "Slot 区间" : "区块区间"}</Radio>
              </Radio.Group>
            </Form.Item>
            {rangeType === "block" && <div className="cs-form-two-columns">
              <Form.Item label={chain === "solana" ? "起始 Slot" : "起始区块"} htmlFor="download-from-block"><Input id="download-from-block" inputMode="numeric" value={fromBlock} onChange={event => { setFromBlock(event.target.value); invalidatePreview(); }} /></Form.Item>
              <Form.Item label={chain === "solana" ? "结束 Slot" : "结束区块"} htmlFor="download-to-block"><Input id="download-to-block" inputMode="numeric" value={toBlock} onChange={event => { setToBlock(event.target.value); invalidatePreview(); }} /></Form.Item>
            </div>}
            {rangeType === "time" && <Form.Item label={`时间范围（${localTimeZone}）`}>
              <DatePicker.RangePicker id={{ start: "download-from-time", end: "download-to-time" }} showTime format="YYYY-MM-DD HH:mm:ss"
                value={[fromTime && dayjs(fromTime).isValid() ? dayjs(fromTime) : null, toTime && dayjs(toTime).isValid() ? dayjs(toTime) : null]}
                onChange={value => { setFromTime(value?.[0]?.toISOString() ?? ""); setToTime(value?.[1]?.toISOString() ?? ""); invalidatePreview(); }} />
            </Form.Item>}
            <h2 className="cs-form-section-title">数据集</h2>
            <Form.Item>
              <AntCheckbox.Group aria-label="数据集" value={datasets} options={datasetItems.map(item => ({ label: datasetLabel(item.id) === item.id ? item.label : datasetLabel(item.id), value: item.id, disabled: item.status !== "available" }))}
                onChange={values => toggleDataset(values.map(String))} />
            </Form.Item>
            <Collapse ghost className="cs-download-advanced" items={[{ key: "advanced", label: "高级设置", children: <div className="cs-form-two-columns">
              <Form.Item label="复用策略"><Radio.Group name="reuse-policy" value={reusePolicy} onChange={event => void chooseReusePolicy(event.target.value as ReusePolicy)}>
                <Radio value="prefer_local">优先复用本地</Radio><Radio value="refresh">重新下载</Radio>
              </Radio.Group></Form.Item>
              <Form.Item label="性能模式" htmlFor="download-performance"><Select id="download-performance" value={performanceMode} options={[
                { value: "safe", label: "稳健" }, { value: "fast", label: "快速" }, { value: "turbo", label: "极速" },
              ]} onChange={value => { setPerformanceMode(value as PerformanceMode); invalidatePreview(); }} /></Form.Item>
            </div> }, { key: "metadata", label: "案件与标签", children: <>
              <div className="cs-form-two-columns">
                <Form.Item label="所属案件"><Space.Compact style={{ width: "100%" }}><Select aria-label="所属案件" value={caseName || undefined} allowClear showSearch optionFilterProp="label" loading={metadataBusy} options={caseOptions.map(item => ({ value: item.name, label: item.name }))} onChange={value => { setCaseName(value ?? ""); invalidatePreview(); }} style={{ flex: 1 }} />{can("case:create") && <AntButton onClick={() => void createMetadata("case")}>新建</AntButton>}</Space.Compact></Form.Item>
                <Form.Item label="默认标签"><Select aria-label="默认标签" mode="multiple" maxCount={20} value={defaultTags} loading={metadataBusy} options={tagOptions.map(item => ({ value: item.name, label: item.name }))} onChange={value => { setDefaultTags(value); invalidatePreview(); }} /></Form.Item>
              </div>
              <Space><AntButton onClick={() => void loadMetadata()} loading={metadataBusy}>刷新案件与标签</AntButton>{can("case:update") && <AntButton onClick={() => void createMetadata("tag")}>新建标签</AntButton>}</Space>
              {imported.length > 0 && <Table<ImportedAddress> size="small" pagination={false} rowKey={row => `${row.chain}:${row.normalized}`} dataSource={imported} scroll={{ x: 680 }} columns={[
                { title: "地址", dataIndex: "normalized", render: (value: string) => <HashValue value={value} label="地址" /> },
                { title: "单独案件", key: "case", render: (_, row) => <Select aria-label={`${row.normalized} 案件`} value={row.caseName || undefined} allowClear options={caseOptions.map(item => ({ value: item.name, label: item.name }))} onChange={value => { setImported(current => current.map(item => item === row ? { ...item, caseName: value ?? "" } : item)); invalidatePreview(); }} style={{ minWidth: 140 }} /> },
                { title: "单独标签", key: "tags", render: (_, row) => <Select aria-label={`${row.normalized} 标签`} mode="multiple" maxCount={20} value={row.tags} options={tagOptions.map(item => ({ value: item.name, label: item.name }))} onChange={value => { setImported(current => current.map(item => item === row ? { ...item, tags: value } : item)); invalidatePreview(); }} style={{ minWidth: 160 }} /> },
              ]} />}
            </> }]} onChange={keys => { if (keys.includes("metadata") && !caseOptions.length && !tagOptions.length) void loadMetadata(); }} />
          </ProForm>
          {coverageRows.length > 0 && <div className="cs-download-coverage">
            <h2 className="cs-form-section-title">数据覆盖</h2>
            <Table<(typeof coverageRows)[number]> size="small" rowKey="key" dataSource={coverageRows} pagination={false} scroll={{ x: 600 }} columns={[
              { title: "链 / 地址", key: "subject", render: (_, row) => <Space direction="vertical" size={0}><span>{row.chain}</span><HashValue value={row.subject} label="地址" /></Space> },
              { title: "数据集", dataIndex: "dataset", render: datasetLabel },
              { title: "本地覆盖", dataIndex: "ratio", render: (value: number) => percent(value) },
              { title: "已覆盖 / 总区块", key: "blocks", render: (_, row) => `${formatRawAmount(String(row.covered_blocks), 0)} / ${formatRawAmount(String(row.total_blocks), 0)}` },
              { title: "缺失范围", key: "gaps", render: (_, row) => row.gaps.length ? <AntButton type="link" size="small" onClick={() => dialog.notify("info", "数据覆盖缺口", null, row.gaps.map((gap, i) => ({ label: `范围 ${i + 1}`, value: `${gap.from_block} → ${gap.to_block}` })))}>{row.gaps.length} 段</AntButton> : "—" },
            ]} />
          </div>}
        </AntCard>
        <AntCard title="任务预览" className="cs-download-summary" extra={<Tag color={preview ? "blue" : undefined}>{preview ? "本次预览" : lastPreview ? "已失效" : "待预览"}</Tag>}>
          <Descriptions column={1} colon={false} items={[
            { key: "chain", label: "链", children: imported.length ? <Space wrap>{[...new Set(imported.map(item => item.chain || chain))].map(slug => <Space key={slug} size={6}><span aria-hidden="true"><ChainIcon chain={slug} size={18} /></span>{slug}</Space>)}</Space> : <Space size={6}>{chain && <span aria-hidden="true"><ChainIcon chain={chain} size={18} /></span>}{chainItems.find(item => item.slug === chain)?.name ?? chain ?? "—"}</Space> },
            { key: "addresses", label: "地址", children: `${addresses.length} 个` },
            { key: "datasets", label: "数据集", children: `${datasets.length} 个` },
          ]} />
          {previewGroups.map(group => <div key={group.chain} className="cs-download-preview-group">
            {previewGroups.length > 1 && <h3>{group.chain}</h3>}
            <Descriptions column={1} colon={false} items={[
              { key: "rows", label: "预计记录", children: group.data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(group.data.estimate.estimated_rows), 0) },
              { key: "coverage", label: "本地覆盖", children: <div className="cs-download-coverage-value"><span>{percent(group.data.coverage.overall_ratio)}</span><Progress percent={Math.max(0, Math.min(100, group.data.coverage.overall_ratio * 100))} showInfo={false} size="small" /></div> },
              { key: "missing", label: "待补区块", children: formatRawAmount(String(group.data.missing_blocks), 0) },
              { key: "cost", label: "预计费用（USD）", children: previewEstimatedCost(group.data) ?? "—" },
              { key: "time", label: "预计耗时（秒）", children: group.data.estimate.estimated_time_seconds ?? "—" },
              { key: "reuse", label: "数据来源", children: group.data.provider_work_required ? "本地 + 新增采集" : "复用本地" },
            ]} />
            <AntButton type="link" onClick={() => void showPreviewDialog(group.data, group.chain)}>预览详情</AntButton>
          </div>)}
          {!preview && <div className="cs-download-preview-empty"><span>预计记录</span><strong>—</strong><span>本地覆盖</span><strong>—</strong><span>预计费用（USD）</span><strong>—</strong></div>}
          <div className="cs-download-submit-actions">
            <AntButton icon={<RefreshCw size={15} />} loading={busy} onClick={() => void handlePreview()}>{preview ? "重新预览" : "预览数据覆盖与成本"}</AntButton>
            <AntButton type="primary" loading={busy} onClick={() => void handleCreate()}>创建下载任务</AntButton>
          </div>
        </AntCard>
      </div>
    </div>
  );
}
