import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError } from "@chain/api-client";
import {
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Field,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";
import { IMPORT_FILE_EXTENSIONS, IMPORT_MAX_ADDRESSES, type ImportedAddress, type LocalParseSummary } from "../import/parse-core";
import { ImportParseError, formatParseBudget, importParseTimeoutMs, parseAddressFileInWorker } from "../import/parse-client";


/* ============================================================================
 * U-DL-01 /downloads/new：真实顺序 对象 → 链与范围 → Dataset → 复用策略 →
 * Coverage/成本预览 → 确认创建。所有说明/风险/结果只通过 Dialog 呈现。
 * ==========================================================================*/

interface ChainMeta {
  slug: string;
  name: string;
  type: string;
  enabled: boolean;
  supports_trace: boolean;
}

interface DatasetMeta {
  id: string;
  label: string;
  status: string;
}

type RangeType = "full" | "block" | "time";
type ReusePolicy = "prefer_local" | "refresh";
type PerformanceMode = "safe" | "fast" | "turbo";

interface RangeBody {
  type: RangeType;
  from_block?: number;
  to_block?: number;
  from?: string;
  to?: string;
}

interface DownloadBody {
  chain: string;
  addresses: string[];
  datasets: string[];
  range: RangeBody;
  reuse_policy: ReusePolicy;
  performance_mode: PerformanceMode;
  /** §23 per-address 案件/标签覆盖；键为服务端 normalized 地址。 */
  subject_meta?: Record<string, { case_name: string; tags: string[] }>;
}

interface PreviewCoverageItem {
  subject: string;
  dataset: string;
  covered_blocks: number;
  total_blocks: number;
  ratio: number;
  gaps: Array<{ from_block: number; to_block: number }>;
}

interface PreviewCandidate {
  job_id: string;
  job_no: string;
  status: string;
  coverage: number;
  updated_at: string;
}

interface PreviewReuse {
  policy: string;
  will_reuse: boolean;
  candidate: PreviewCandidate | null;
  coverage_hit: boolean;
  refresh: boolean;
}

interface DownloadEstimate {
  estimated_missing_blocks: number | null;
  estimated_rows: number | null;
  estimated_cost_usd: string | null;
  estimated_time_seconds: number | null;
  basis: string;
}

interface DownloadPreviewData {
  request: {
    chain: string;
    addresses: string[];
    datasets: string[];
    range: RangeBody;
    reuse_policy: ReusePolicy;
    performance_mode: PerformanceMode;
  };
  coverage: { chain: string; datasets: string[]; items: PreviewCoverageItem[]; overall_ratio: number };
  missing_blocks: number;
  provider_work_required: boolean;
  reuse: PreviewReuse;
  estimate: DownloadEstimate;
  risks: string[];
  source: Record<string, unknown>;
}

interface DownloadCreateData {
  status: "ready" | "reused" | "planned";
  created: boolean;
  reused: boolean;
  provider_work_required: boolean;
  coverage: number;
  job_id: string | null;
  job_no?: string;
}

function parseAddressInput(raw: string) {
  const tokens = raw
    .split(/[\s,;，；|]+/)
    .map((value) => value.trim())
    .filter(Boolean);
  const seen = new Set<string>();
  const list: string[] = [];
  let duplicates = 0;
  for (const token of tokens) {
    const localKey = token.toLowerCase();
    if (seen.has(localKey)) {
      duplicates += 1;
      continue;
    }
    seen.add(localKey);
    list.push(token);
  }
  return { tokens: tokens.length, list, duplicates };
}

/* ---- §18.10 导入：CSV/XLSX 解析（浏览器） + 服务端再次验证 ---- */

export interface ServerAddressVerdict {
  index: number;
  row: number;
  input: string;
  chain: string;
  code: string;
  message: string;
}

export interface ServerFieldRejection {
  row: number;
  index: number;
  field: string;
  code: string;
  count: number;
  limit: number;
  message: string;
}

export interface ServerMetaVerdict {
  name: string;
  kind: string;
  status: "resolved" | "missing" | "will_create" | "denied";
  case_id?: string | null;
  tag_id?: string | null;
  required_permission?: string;
}

export interface ServerValidateResult {
  accepted: Array<{ index: number; row: number; input: string; chain: string; normalized: string; case_name: string; tags: string[] }>;
  rejected: ServerAddressVerdict[];
  field_rejections: ServerFieldRejection[];
  scope: { organization_id: string; workspace_id: string };
  grants: { "download:create": boolean; "case:create": boolean; "case:update": boolean; create_missing_meta_requested: boolean };
  summary: {
    submitted: number; accepted: number; rejected: number; blank: number; invalid: number;
    duplicate: number; cap_rejected: number; tags_over_limit: number; field_rejections: number;
    meta_denied: number; max_addresses: number; max_tags: number;
    chains: Record<string, number>; distinct_chains: number;
  };
  meta: { cases: ServerMetaVerdict[]; tags: ServerMetaVerdict[] };
  source: string;
}
/**
 * 按链分组：契约 1043 要求"每个地址可单独选择链"，而服务端一次请求只接受同一条链。
 *
 * **纯函数**：只依赖入参。若改成读组件 state，首次"预览"触发导入后 setState 尚未生效，
 * 就会用旧集合提交（服务端过滤/normalize 之后更是如此）。
 */
export function groupAddressesByChain(rows: ImportedAddress[], defaultChain: string): Array<{ chain: string; items: ImportedAddress[] }> {
  const groups = new Map<string, ImportedAddress[]>();
  for (const item of rows) {
    const key = item.chain || defaultChain;
    if (!key) continue;
    const list = groups.get(key) ?? [];
    list.push(item);
    groups.set(key, list);
  }
  return [...groups.entries()].map(([chain, items]) => ({ chain, items }));
}

/**
 * 以"预览指纹 + 链 + 地址集合"派生稳定幂等键：同一批地址重试时复用同一个 key，
 * 避免第一次部分成功后重试又创建一遍。
 */
export async function stableIdempotencyKey(scope: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(scope));
  const hex = [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-5${hex.slice(13, 16)}-a${hex.slice(17, 20)}-${hex.slice(20, 32)}`;
}


function toIso(localValue: string): string {
  if (!localValue) return "";
  const date = new Date(localValue);
  if (Number.isNaN(date.getTime())) return "";
  return date.toISOString();
}

function percent(value: number | null | undefined, digits = 1): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "—";
  return `${(value * 100).toFixed(digits)}%`;
}

function rangeText(body: { range: RangeBody }): string {
  const range = body.range;
  if (range.type === "full") return "全量范围";
  if (range.type === "block") return `区块 ${range.from_block ?? "—"} → ${range.to_block ?? "—"}`;
  return `时间 ${range.from ? formatTime(range.from) : "—"} → ${range.to ? formatTime(range.to) : "—"}`;
}

export default function DownloadNewPage() {
  const dialog = useDialog();
  const navigate = useNavigate();
  const { busy, run, client } = useS5Action();

  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets");

  const [addressText, setAddressText] = useState("");
  const [addresses, setAddresses] = useState<string[]>([]);
  const [parsedFrom, setParsedFrom] = useState("");
  /** 服务端再次验证后的权威条目（含 per-address chain/case/tags）。 */
  const [imported, setImported] = useState<ImportedAddress[]>([]);
  const [serverVerdict, setServerVerdict] = useState<ServerValidateResult | null>(null);
  const [chain, setChain] = useState("");
  const [rangeType, setRangeType] = useState<RangeType>("full");
  const [fromBlock, setFromBlock] = useState("");
  const [toBlock, setToBlock] = useState("");
  const [fromTime, setFromTime] = useState("");
  const [toTime, setToTime] = useState("");
  const [datasets, setDatasets] = useState<string[]>([]);
  const [reusePolicy, setReusePolicy] = useState<ReusePolicy>("prefer_local");
  const [performanceMode, setPerformanceMode] = useState<PerformanceMode>("safe");
  const [preview, setPreview] = useState<DownloadPreviewData | null>(null);
  const [previews, setPreviews] = useState<Array<{ chain: string; data: DownloadPreviewData }>>([]);
  const [previewFingerprint, setPreviewFingerprint] = useState<string | null>(null);
  const [lastTrace, setLastTrace] = useState<string | null>(null);

  const datasetsTouched = useRef(false);
  const lastNotifiedError = useRef<string | null>(null);
  const importFileRef = useRef<HTMLInputElement>(null);

  /* 加载错误：ErrorState 只显示通用文案，原始错误正文只允许出现在 Dialog。 */
  useEffect(() => {
    for (const error of [chainsApi.error, datasetsApi.error]) {
      if (error && lastNotifiedError.current !== error) {
        lastNotifiedError.current = error;
        dialog.notify("error", "加载失败", error);
      }
    }
  }, [chainsApi.error, datasetsApi.error, dialog]);

  const chainItems = chainsApi.data?.items ?? [];
  const datasetItems = datasetsApi.data?.items ?? [];

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
      const body: DownloadBody = { chain: groupChain, addresses: addrs, datasets, range, reuse_policy: reusePolicy, performance_mode: performanceMode };
      if (Object.keys(subjectMeta).length > 0) body.subject_meta = subjectMeta;
      return body;
    },
    [datasets, fromBlock, fromTime, performanceMode, rangeType, reusePolicy, toBlock, toTime],
  );

  const importedAddressText = parsedFrom !== "" && parsedFrom === addressText.trim();

  // 见 groupAddressesByChain：纯函数，必须用**本次调用返回的 rows**，不能读闭包里的旧 state。

  /**
   * 服务端再次验证（§18.10 1042）：本地只做拆分与摘要，接受/拒绝与规范化地址
   * 一律以服务端 verdict 为准；被拒条目在 Dialog 中原样呈现，不静默丢弃。
   */
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
    const answer = await dialog.open({
      kind: parsed.list.length > IMPORT_MAX_ADDRESSES ? "warning" : "info",
      title: "地址导入解析摘要（浏览器）",
      body: "服务端最终以 chain + 规范化地址 作为去重键，本地仅做初步拆分与大小写去重。",
      details,
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "提交服务端校验", result: "use", variant: "primary" },
      ],
    });
    if (answer !== "use") return null;
    const verdict = await revalidateOnServer(capped.map((value) => ({ input: value, normalized: "", chain: "", caseName: "", tags: [] })));
    if (!verdict) return null;
    return applyServerVerdict(verdict);
  }, [addressText, dialog, revalidateOnServer, applyServerVerdict]);

  const importAddressesFromFile = useCallback(async (file: File): Promise<ImportedAddress[] | null> => {
    // 计划 1852 / §23.7：解析在隔离 Worker 中执行，主线程只等消息；
    // 有界超时到点即 terminate（真停止，不是"忽略结果"），用户也可随时取消。
    const timeoutMs = importParseTimeoutMs();
    const controller = new AbortController();
    let settled = false;
    // 进度与取消的唯一入口是弹窗（AGENTS.md：提示不得嵌入页面）。
    const progress = dialog.open({
      kind: "info",
      title: "正在隔离解析文件",
      body: `${file.name} 在 Web Worker 中解析，页面保持可交互；超过 ${formatParseBudget(timeoutMs)}会自动中止，也可以立即取消。`,
      details: [
        { label: "文件名", value: file.name },
        { label: "文件大小", value: `${file.size} 字节` },
      ],
      actions: [{ label: "取消解析", result: "cancel", variant: "primary" }],
    });
    void progress.then(() => {
      if (!settled) controller.abort();
    });
    let parsed: { rows: ImportedAddress[]; summary: LocalParseSummary };
    try {
      const result = await parseAddressFileInWorker(file, chain, { signal: controller.signal, timeoutMs });
      parsed = { rows: result.rows, summary: result.summary };
    } catch (error) {
      settled = true;
      dialog.dismiss();
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
    settled = true;
    dialog.dismiss();
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
    const answer = await dialog.open({
      kind: rows.length === 0 || summary.truncated > 0 ? "warning" : "info",
      title: "地址导入解析摘要（浏览器）",
      body: rows.length === 0
        ? "文件里没有可用的地址行，请检查文件内容与列名。"
        : "本地摘要只做拆分与计数；接受/拒绝与规范化地址以服务端再次验证为准。",
      details,
      actions: rows.length === 0
        ? [{ label: "知道了", result: "ok", variant: "primary" }]
        : [
            { label: "取消", result: "", variant: "default" },
            { label: "提交服务端校验", result: "use", variant: "primary" },
          ],
    });
    if (answer !== "use" || rows.length === 0) return null;
    const verdict = await revalidateOnServer(rows);
    if (!verdict) return null;
    return applyServerVerdict(verdict);
  }, [applyServerVerdict, chain, dialog, revalidateOnServer]);

  const ensureAddresses = useCallback(async (): Promise<ImportedAddress[] | null> => {
    if (imported.length > 0 && importedAddressText) return imported;
    return importAddresses();
  }, [importAddresses, imported, importedAddressText]);

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
        dialog.notify("warning", "请选择 Dataset", "至少选择一个 Dataset。");
        return false;
      }
      if (datasets.length > 6) {
        dialog.notify("warning", "Dataset 数量超出上限", "单次下载最多选择 6 个 Dataset。");
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
    [chain, datasets.length, dialog, fromBlock, fromTime, rangeType, toBlock, toTime],
  );

  const previewDetails = useCallback((data: DownloadPreviewData) => {
    const details: Array<{ label: string; value: string }> = [
      { label: "复用策略", value: data.reuse.policy },
      { label: "Coverage 命中", value: data.reuse.coverage_hit ? "是" : "否" },
      { label: "将复用现有任务", value: data.reuse.will_reuse ? "是" : "否" },
      { label: "provider_work_required", value: String(data.provider_work_required) },
      { label: "整体覆盖率", value: percent(data.coverage.overall_ratio) },
      { label: "未覆盖区块", value: formatRawAmount(String(data.missing_blocks), 0) },
      { label: "预估行数", value: data.estimate.estimated_rows === null ? "—" : formatRawAmount(String(data.estimate.estimated_rows), 0) },
      { label: "预估成本 USD", value: data.estimate.estimated_cost_usd ?? "—" },
      { label: "预估耗时（秒）", value: data.estimate.estimated_time_seconds === null ? "—" : String(data.estimate.estimated_time_seconds) },
      { label: "估算依据", value: data.estimate.basis },
    ];
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
        title: data.risks.length > 0 ? "预览完成，存在风险提示" : "Coverage 与成本预览完成",
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
      dialog.notify("info", "需要先完成真实预览", "创建前必须调用 /api/v2/downloads/preview 确认 Coverage 与成本，参数变化后会重新预览。");
      await handlePreview();
      return;
    }
    const confirm = await dialog.open({
      kind: "confirm",
      title: "确认创建下载任务？",
      body:
        payload.reuse_policy === "refresh"
          ? "refresh 将新增 Provider 取数工作并产生成本，不复用本地已完成覆盖。"
          : "默认 prefer_local：优先复用本地数据或已有任务，服务端会再次校验 Coverage 与复用候选。",
      details: [
        { label: "链分组", value: payloads.map((item) => `${item.chain}:${item.addresses.length}`).join("、") },
        { label: "地址总数", value: String(payloads.reduce((sum, item) => sum + item.addresses.length, 0)) },
        { label: "Dataset", value: payload.datasets.join("、") },
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
    // 幂等键由"预览指纹 + 链 + 地址集合"派生：同一批地址重试复用同一 key，
    // 服务端幂等层会返回既有任务而不是再创建一份。
    type GroupOutcome = { chain: string; ok: boolean; data?: DownloadCreateData; error?: string };
    const outcomes: GroupOutcome[] = [];
    for (const [index, groupPayload] of payloads.entries()) {
      const idempotencyKey = await stableIdempotencyKey(`${fingerprint}|${groupPayload.chain}|${groupPayload.addresses.join(",")}`);
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
        ? (allReady ? "本地 Coverage 已完整命中，未创建任务、未调用 Provider。" : undefined)
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
  }, [buildPayload, chain, client, dialog, ensureAddresses, handlePreview, navigate, preview, previewFingerprint, previews, run, validateForm]);

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

  const toggleDataset = useCallback((id: string) => {
    datasetsTouched.current = true;
    setDatasets((prev) => (prev.includes(id) ? prev.filter((item) => item !== id) : [...prev, id]));
    setPreview(null);
    setPreviewFingerprint(null);
  }, []);

  const previewItems = preview?.coverage.items ?? [];

  return (
    <>
      <PageHeader
        title="新建数据下载"
        actions={
          <button type="button" className="btn btn--sm" onClick={() => navigate("/jobs")}>
            任务中心
          </button>
        }
      />

      <div className="workbench">
        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
          <Card title="1 · 对象（地址）">
            <div className="s4-form-grid">
              <div className="field field--wide">
                <Field label={`地址列表（已导入 ${addresses.length} 个）`} htmlFor="download-addresses">
                  <textarea
                    id="download-addresses"
                    className="textarea"
                    value={addressText}
                    placeholder="每行一个地址，或用逗号/分号/空白分隔后粘贴"
                    onChange={(event) => {
                      setAddressText(event.target.value);
                      if (event.target.value.trim() !== parsedFrom) {
                        setAddresses([]);
                        setPreview(null);
                        setPreviewFingerprint(null);
                      }
                    }}
                  />
                </Field>
              </div>
              <div className="field--wide toolbar">
                <button type="button" className="btn btn--sm" onClick={() => void importAddresses()}>
                  解析导入
                </button>
                <button type="button" className="btn btn--sm btn--ghost" onClick={() => importFileRef.current?.click()}>
                  导入 CSV / XLSX
                </button>
                <input
                  ref={importFileRef}
                  type="file"
                  accept={IMPORT_FILE_EXTENSIONS.join(",")}
                  aria-label="导入地址文件"
                  hidden
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = "";
                    if (file) void importAddressesFromFile(file);
                  }}
                />
                <button
                  type="button"
                  className="btn btn--sm btn--ghost"
                  disabled={addresses.length === 0 && addressText.trim() === ""}
                  onClick={() => {
                    setAddressText("");
                    setAddresses([]);
                    setImported([]);
                    setServerVerdict(null);
                    setParsedFrom("");
                    setPreview(null);
                    setPreviews([]);
                    setPreviewFingerprint(null);
                  }}
                >
                  清空
                </button>
              </div>
              {serverVerdict && (
                <p className="field-hint" data-testid="server-verdict-summary">
                  {`服务端复核：接受 ${serverVerdict.summary.accepted} · 拒绝 ${serverVerdict.summary.rejected} · 链 ${Object.entries(serverVerdict.summary.chains).map(([k, v]) => `${k}:${v}`).join("、") || "—"} · 来源 ${serverVerdict.source}`}
                </p>
              )}
            </div>
          </Card>

          <Card title="2 · 链与范围">
            <div className="s4-form-grid">
              <Field label="链" htmlFor="download-chain">
                <select
                  id="download-chain"
                  className="select"
                  value={chain}
                  onChange={(event) => {
                    setChain(event.target.value);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="">请选择链</option>
                  {chainItems.map((item) => (
                    <option key={item.slug} value={item.slug} disabled={!item.enabled}>
                      {item.name}（{item.slug}）{item.enabled ? "" : " · 不可用"}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="范围类型" htmlFor="download-range-type">
                <select
                  id="download-range-type"
                  className="select"
                  value={rangeType}
                  onChange={(event) => {
                    setRangeType(event.target.value as RangeType);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="full">full · 全量</option>
                  <option value="block">block · 区块区间</option>
                  <option value="time">time · 时间区间</option>
                </select>
              </Field>
              {rangeType === "block" ? (
                <>
                  <Field label="起始区块" htmlFor="download-from-block">
                    <input id="download-from-block" className="input" inputMode="numeric" value={fromBlock} onChange={(event) => { setFromBlock(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                  <Field label="结束区块" htmlFor="download-to-block">
                    <input id="download-to-block" className="input" inputMode="numeric" value={toBlock} onChange={(event) => { setToBlock(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                </>
              ) : null}
              {rangeType === "time" ? (
                <>
                  <Field label="起始时间" htmlFor="download-from-time">
                    <input id="download-from-time" className="input" type="datetime-local" value={fromTime} onChange={(event) => { setFromTime(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                  <Field label="结束时间" htmlFor="download-to-time">
                    <input id="download-to-time" className="input" type="datetime-local" value={toTime} onChange={(event) => { setToTime(event.target.value); setPreview(null); setPreviewFingerprint(null); }} />
                  </Field>
                </>
              ) : null}
              <Field label="性能模式" htmlFor="download-performance">
                <select
                  id="download-performance"
                  className="select"
                  value={performanceMode}
                  onChange={(event) => {
                    setPerformanceMode(event.target.value as PerformanceMode);
                    setPreview(null);
                    setPreviewFingerprint(null);
                  }}
                >
                  <option value="safe">safe</option>
                  <option value="fast">fast</option>
                  <option value="turbo">turbo</option>
                </select>
              </Field>
            </div>
          </Card>

          <Card title="3 · Dataset">
            {datasetsApi.loading ? (
              <SkeletonRows rows={2} columns={3} />
            ) : datasetItems.length === 0 ? (
              <EmptyState title="服务端未返回可用 Dataset" />
            ) : (
              <div className="toolbar" role="group" aria-label="Dataset 选择">
                {datasetItems.map((item) => (
                  <label key={item.id} className="btn btn--sm" style={{ cursor: item.status === "available" ? "pointer" : "not-allowed" }}>
                    <input
                      type="checkbox"
                      checked={datasets.includes(item.id)}
                      disabled={item.status !== "available"}
                      onChange={() => toggleDataset(item.id)}
                    />
                    {item.label}（{item.id}）
                  </label>
                ))}
              </div>
            )}
          </Card>

          <Card title="4 · 复用策略">
            <div className="toolbar" role="radiogroup" aria-label="复用策略">
              <label className="btn btn--sm" style={{ cursor: "pointer" }}>
                <input type="radio" name="reuse-policy" checked={reusePolicy === "prefer_local"} onChange={() => void chooseReusePolicy("prefer_local")} />
                prefer_local（默认，优先复用本地）
              </label>
              <label className="btn btn--sm" style={{ cursor: "pointer" }}>
                <input type="radio" name="reuse-policy" checked={reusePolicy === "refresh"} onChange={() => void chooseReusePolicy("refresh")} />
                refresh（不复用，新增 Provider 工作）
              </label>
            </div>
          </Card>

          <Card
            title="5 · Coverage / 成本预览"
            actions={
              <span className="toolbar">
                {preview ? (
                  <button type="button" className="btn btn--sm" onClick={() => void showPreviewDialog(preview)}>
                    {preview.provider_work_required ? `成本/风险说明（${preview.risks.length}）` : "复用说明"}
                  </button>
                ) : null}
                <button type="button" className="btn btn--sm" disabled={busy} onClick={() => void handlePreview()}>
                  {busy ? "处理中…" : "预览 Coverage 与成本"}
                </button>
              </span>
            }
          >
            {chainsApi.loading || datasetsApi.loading ? (
              <SkeletonRows rows={3} columns={4} />
            ) : chainsApi.error ? (
              <ErrorState
                title="链元数据加载失败"
                message="加载失败，请重试。"
                onRetry={() => {
                  void chainsApi.reload();
                }}
              />
            ) : !preview ? (
              <EmptyState
                title="尚未预览"
                action={
                  <button type="button" className="btn btn--sm" onClick={() => void handlePreview()}>
                    立即预览
                  </button>
                }
              />
            ) : (
              <>
                <div className="metric-grid">
                  <Metric label="整体覆盖率" value={percent(preview.coverage.overall_ratio)} tone={preview.coverage.overall_ratio >= 1 ? "ok" : "warn"} />
                  <Metric label="未覆盖区块" value={formatRawAmount(String(preview.missing_blocks), 0)} />
                  <Metric
                    label="预估行数"
                    value={preview.estimate.estimated_rows === null ? "—" : formatRawAmount(String(preview.estimate.estimated_rows), 0)}
                  />
                  <Metric label="预估成本 USD" value={preview.estimate.estimated_cost_usd ?? "—"} />
                  <Metric
                    label="预估耗时（秒）"
                    value={preview.estimate.estimated_time_seconds === null ? "—" : String(preview.estimate.estimated_time_seconds)}
                  />
                  <Metric
                    label="Provider 工作"
                    value={
                      <StatusBadge tone={preview.provider_work_required ? "warn" : "ok"}>
                        {preview.provider_work_required ? "需要新增" : "复用本地"}
                      </StatusBadge>
                    }
                  />
                </div>
                <div style={{ marginTop: "var(--sp-4)" }}>
                  <DataTable<PreviewCoverageItem>
                    rows={previewItems}
                    rowKey={(row, index) => `${row.subject}-${row.dataset}-${index}`}
                    columns={[
                      { key: "subject", header: "对象", width: "170px", render: (row) => <span className="mono" title={row.subject}>{shortValue(row.subject, 12, 8)}</span> },
                      { key: "dataset", header: "Dataset", width: "120px", render: (row) => row.dataset },
                      { key: "ratio", header: "Coverage", width: "90px", render: (row) => percent(row.ratio) },
                      {
                        key: "blocks",
                        header: "已覆盖 / 总区块",
                        width: "150px",
                        render: (row) => `${formatRawAmount(String(row.covered_blocks), 0)} / ${formatRawAmount(String(row.total_blocks), 0)}`,
                      },
                      { key: "gaps", header: "缺口", width: "90px", render: (row) => String(row.gaps.length) },
                      {
                        key: "gap-range",
                        header: "首个缺口",
                        render: (row) => (row.gaps.length > 0 ? <span className="mono">{`${row.gaps[0].from_block} → ${row.gaps[0].to_block}`}</span> : "—"),
                      },
                    ]}
                    empty={<EmptyState title="服务端未返回 Coverage 明细" />}
                  />
                </div>
              </>
            )}
          </Card>

          <div className="toolbar" style={{ justifyContent: "flex-end" }}>
            <button type="button" className="btn" onClick={() => navigate("/jobs")}>
              取消
            </button>
            <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void handleCreate()}>
              {busy ? "正在创建…" : "创建下载任务"}
            </button>
          </div>
        </div>

        <EvidenceRail
          title="预览来源与复用"
          items={[
            { label: "复用策略", value: preview?.reuse.policy ?? reusePolicy },
            { label: "Coverage 命中", value: preview ? (preview.reuse.coverage_hit ? "是" : "否") : "—" },
            { label: "将复用", value: preview ? (preview.reuse.will_reuse ? "是" : "否") : "—" },
            { label: "候选任务", value: preview?.reuse.candidate ? `${preview.reuse.candidate.job_no} · ${preview.reuse.candidate.status}` : "—" },
            { label: "refresh", value: preview ? (preview.reuse.refresh ? "是" : "否") : "—" },
            { label: "估算依据", value: preview?.estimate.basis ?? "—" },
            { label: "Coverage 来源", value: String(preview?.source?.coverage ?? "—") },
            { label: "Provider 来源", value: String(preview?.source?.provider ?? "—") },
            { label: "数据时点", value: preview ? formatTime(String(preview.source.as_of ?? "")) : "—" },
            { label: "最近 Trace", value: lastTrace ?? "—" },
          ]}
        />
      </div>
    </>
  );
}
