import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import { Braces,
  Database,
  FileArchive,
  FileSpreadsheet,
  Table2 } from "lucide-react";
import { Button as AntButton, Card as AntCard, Checkbox as AntCheckbox, Collapse, DatePicker, Form, Input, Radio, Select, Space, Table } from "antd";
import dayjs from "dayjs";
import { useSearchParams } from "react-router-dom";
import { pageStateScope } from "../app/page-state";
import { addressChainFamily, chainFamilyOf, chainForFamily, familyLabel, unanimousAddressFamily } from "../app/chain-address";
import { ProForm } from "@ant-design/pro-components";
import { datasetLabel, jobStatusLabel } from "../app/business-labels";
import { ApiError,
  canUseDirectDownload,
  directTicketUrl,
  useAuth } from "@chain/api-client";
import {
  Checkbox,
  CompactProgress,
  CopyButton,
  DatasetSelector,
  EmptyState,
  Field,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  PageSection,
  SplitPane,
  Stepper,
  StatusBadge,
  StatusBadgeForState,
  StickyActionBar,
  progressStateFor,
  useDialog,
} from "@chain/ui";
import { CsPageHeader, CsRefreshBar } from "@chain/user-ui";
import { CsProTable } from "@chain/user-ui/pro-table";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";
import { ChainIcon } from "./explorer/AssetIcon";
import "./exports.css";
import "./workspaces.css";

/* ============================================================================
 * U-EXPORT-01 /exports：导出列表 + 新建导出 + 60 秒一次性下载票据。
 *
 * 数据层与契约完全不变：POST /api/v2/exports（Idempotency-Key）、
 * POST /api/v2/exports/{id}/download-ticket、状态筛选与 cursor 分页全部照旧；
 * 表单字段 id、按钮文字与格式选项保持原样（R10 / B2RAW 端到端脚本依赖）。
 *
 * 列表为首屏，新建导出使用 Dialog，文件清单保留真实行数、大小和 SHA-256。
 * 所有说明、警告、成功与错误只通过 Dialog 呈现。
 * ==========================================================================*/

type ExportFormat = "csv" | "xlsx" | "jsonl" | "parquet";

interface ExportItem {
  id: string;
  job_id: string | null;
  dataset: string;
  format: string;
  zip: boolean;
  status: string;
  rows: number;
  sha256: string;
  error_safe: string;
  object_ref: string;
  created_at: string;
  updated_at: string;
  finished_at: string | null;
  expires_at: string | null;
  download_available: boolean;
  bytes?: number;
}

interface ExportsListData {
  items: ExportItem[];
  scope: Record<string, unknown>;
}

interface ChainMeta {
  slug: string;
  name: string;
  enabled: boolean;
}

interface DatasetMeta {
  id: string;
  label: string;
  status: string;
}

interface JobLite {
  id: string;
  job_no: string;
  status: string;
  chain: string;
  datasets: string[];
  updated_at: string;
}

interface ExportCreateData {
  id: string;
  status: string;
  format: string;
  zip: boolean;
  job_id: string | null;
  addresses: number;
  datasets: string[];
}

interface ExportDraft {
  mode: "job" | "addresses";
  jobId: string;
  chain: string;
  addressText: string;
  addresses: string[];
  datasets: string[];
  format: ExportFormat;
  includeMeta: boolean;
  forceZip: boolean;
  includeDecodedInput: boolean;
  content: "details" | "statistics" | "counterparties";
  fromTime: string;
  toTime: string;
  token: string;
  direction: string;
  counterparty: string;
  fromAddress: string;
  toAddress: string;
  hideZero: boolean;
  minAmount: string;
  maxAmount: string;
  asOf: string;
}

const FORMATS: ExportFormat[] = ["csv", "xlsx", "jsonl", "parquet"];
const EXPORTABLE_JOB_STATUSES = new Set(["COMPLETED", "PAUSED", "PAUSED_FALLBACK_TOO_EXPENSIVE"]);
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const MAX_EXPORT_ADDRESSES = 100;
const EXPORT_STEPS = [
  { key: "object", label: "导出对象" },
  { key: "data", label: "数据与格式" },
  { key: "options", label: "导出选项" },
];

const EMPTY_DRAFT: ExportDraft = {
  content: "details", fromTime: "", toTime: "", token: "", direction: "", counterparty: "", fromAddress:"", toAddress:"", hideZero:false, minAmount: "", maxAmount: "", asOf: "",
  mode: "addresses",
  jobId: "",
  chain: "",
  addressText: "",
  addresses: [],
  datasets: ["transactions"],
  format: "csv",
  includeMeta: false,
  forceZip: false,
  includeDecodedInput: false,
};

function parseAddressInput(raw: string): string[] {
  const seen = new Set<string>();
  const list: string[] = [];
  for (const token of raw.split(/[\s,;，；、|]+/)) {
    const value = token.trim();
    if (!value) continue;
    const key = value;
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(value);
  }
  return list;
}

/** 格式标签：图标 + 文字，两通道表达，不依赖颜色（蓝图 §12.20 / §14）。 */
function FormatTag({ format, zip }: { format: string; zip: boolean }) {
  const key = (format || "").toLowerCase();
  const label = key === "xlsx" ? "XLSX" : key === "jsonl" ? "JSONL" : key === "parquet" ? "Parquet" : "CSV";
  const Icon = key === "xlsx" ? FileSpreadsheet : key === "jsonl" ? Braces : key === "parquet" ? Database : Table2;
  return (
    <span className="toolbar" style={{ gap: "var(--sp-1)" }}>
      <span className="badge badge--neutral" title={key || "—"}>
        <Icon size={11} aria-hidden="true" />
        {label}
      </span>
      {zip ? (
        <span className="badge badge--neutral" title="ZIP">
          <FileArchive size={11} aria-hidden="true" />
          ZIP
        </span>
      ) : null}
    </span>
  );
}

/** 断点判定：<900px 时行点击不开右侧预览（该宽度下不渲染检查器，行也不再可激活）。 */
function useWideViewport(): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia("(min-width: 900px)").matches,
  );
  useEffect(() => {
    const list = window.matchMedia("(min-width: 900px)");
    const sync = () => setMatches(list.matches);
    sync();
    list.addEventListener("change", sync);
    return () => list.removeEventListener("change", sync);
  }, []);
  return matches;
}

function ExportDraftEditor({
  value,
  onChange,
  chains,
  datasets,
  jobOptions,
  jobsLoading,
  idPrefix,
}: {
  value: ExportDraft;
  onChange: (next: ExportDraft) => void;
  chains: ChainMeta[];
  datasets: DatasetMeta[];
  jobOptions: JobLite[];
  jobsLoading: boolean;
  /** 表单内联与 Dialog 两个实例必须使用不同的 id 前缀（同页不得出现重复 id）。 */
  idPrefix: string;
}) {
  const draft = value;
  const id = (name: string) => idPrefix + name;
  const update = (patch: Partial<ExportDraft>) => onChange({ ...draft, ...patch });

  return (
    <div className="cs-export-dialog">
      <ProForm layout="vertical" submitter={false}>
        <Form.Item label="导出内容"><Select aria-label="导出内容" value={draft.content} onChange={value => update({ content: value })} options={[{ value: "details", label: "交易明细" }, { value: "statistics", label: "统计汇总" }, { value: "counterparties", label: "对手方排行" }]} /></Form.Item>
        <Form.Item label="导出对象" htmlFor={id("export-mode")}>
          <Radio.Group id={id("export-mode")} value={draft.mode} onChange={event => update({ mode: event.target.value as ExportDraft["mode"] })}>
            <Radio value="addresses">按地址</Radio><Radio value="job">按下载任务</Radio>
          </Radio.Group>
        </Form.Item>
        {draft.mode === "job" ? <Form.Item label="下载任务" htmlFor={id(jobOptions.length ? "export-job-select" : "export-job-id")}>
          {jobOptions.length ? <Select id={id("export-job-select")} value={draft.jobId || undefined} placeholder="选择已完成或暂停的任务" showSearch optionFilterProp="label"
            options={jobOptions.map(job => ({ value: job.id, label: `${job.job_no} · ${jobStatusLabel(job.status)} · ${job.chain}` }))}
            onChange={value => update({ jobId: value })} /> : <Input id={id("export-job-id")} value={draft.jobId} placeholder={jobsLoading ? "正在读取下载任务…" : "粘贴任务 ID"} onChange={event => update({ jobId: event.target.value })} />}
        </Form.Item> : <>
          <Form.Item label="区块链" htmlFor={id("export-chain")}><Select id={id("export-chain")} value={draft.chain || undefined} placeholder="请选择链"
            options={chains.map(chain => ({ value: chain.slug, label: <Space size={8}><span aria-hidden="true"><ChainIcon chain={chain.slug} /></span>{chain.name}</Space>, disabled: !chain.enabled }))} onChange={value => update({ chain: value })} /></Form.Item>
          <Form.Item label={`地址（${draft.addresses.length} 个）`} htmlFor={id("export-addresses")}><Input.TextArea id={id("export-addresses")} autoSize={{ minRows: 3, maxRows: 8 }} value={draft.addressText} placeholder="每行一个地址，或使用逗号、空白分隔"
            onChange={event => update({ addressText: event.target.value, addresses: parseAddressInput(event.target.value) })} /></Form.Item>
          <Form.Item label="数据集"><AntCheckbox.Group value={draft.datasets} options={datasets.map(dataset => ({ value: dataset.id, label: datasetLabel(dataset.id) === dataset.id ? dataset.label : datasetLabel(dataset.id), disabled: dataset.status !== "available" }))}
            onChange={values => update({ datasets: values.map(String) })} /></Form.Item>
        </>}
        <Form.Item label="文件格式" htmlFor={id("export-format")}><Select id={id("export-format")} value={draft.format} options={FORMATS.map(format => ({ value: format, label: format.toUpperCase() }))} onChange={value => update({ format: value as ExportFormat })} /></Form.Item>
        <Collapse defaultActiveKey={draft.fromTime || draft.toTime || draft.token || draft.direction || draft.counterparty ? ["filters"] : []} ghost items={[{ key: "filters", label: "查询筛选", children: <>
          <Form.Item label="时间范围"><DatePicker.RangePicker showTime format="YYYY-MM-DD HH:mm:ss" value={[draft.fromTime ? dayjs(draft.fromTime) : null, draft.toTime ? dayjs(draft.toTime) : null]} onChange={values => update({ fromTime: values?.[0]?.toISOString() || "", toTime: values?.[1]?.toISOString() || "", asOf: "" })} /></Form.Item>
          <div className="cs-form-two-columns"><Form.Item label="币种"><Input value={draft.token} onChange={event => update({ token: event.target.value })} placeholder="代币合约 / 符号" /></Form.Item>
            <Form.Item label="方向"><Select value={draft.direction} onChange={value => update({ direction: value })} options={[{ value: "", label: "全部方向" }, { value: "in", label: "收入" }, { value: "out", label: "支出" }, { value: "self", label: "自转" }, { value: "internal", label: "组内往来" }]} /></Form.Item></div>
          <Form.Item label="对手方"><Input value={draft.counterparty} onChange={event => update({ counterparty: event.target.value })} placeholder="完整地址" /></Form.Item>
          <Form.Item label="发送方"><Input value={draft.fromAddress} onChange={event => update({ fromAddress: event.target.value })} /></Form.Item>
          <Form.Item label="接收方"><Input value={draft.toAddress} onChange={event => update({ toAddress: event.target.value })} /></Form.Item>
          <AntCheckbox checked={draft.hideZero} onChange={event => update({ hideZero:event.target.checked })}>隐藏数量为 0 的转账</AntCheckbox>
          <div className="cs-form-two-columns"><Form.Item label="最小金额"><Input value={draft.minAmount} onChange={event => update({ minAmount: event.target.value })} /></Form.Item><Form.Item label="最大金额"><Input value={draft.maxAmount} onChange={event => update({ maxAmount: event.target.value })} /></Form.Item></div>
        </> }]} />
        <Collapse ghost items={[{ key: "options", label: "更多选项", children: <Space direction="vertical">
          <AntCheckbox checked={draft.includeMeta} onChange={event => update({ includeMeta: event.target.checked })}>包含元数据</AntCheckbox>
          <AntCheckbox checked={draft.forceZip} onChange={event => update({ forceZip: event.target.checked })}>打包为 ZIP</AntCheckbox>
          <AntCheckbox checked={draft.includeDecodedInput} onChange={event => update({ includeDecodedInput: event.target.checked })}>包含解码后的调用参数</AntCheckbox>
        </Space> }]} />
      </ProForm>
    </div>
  );
}

/** Dialog 内的编辑器：本地状态保证输入即时可见，同时把每次变更写回页面草稿。 */
function DialogDraftEditor(props: {
  value: ExportDraft;
  onChange: (next: ExportDraft) => void;
  chains: ChainMeta[];
  datasets: DatasetMeta[];
  jobOptions: JobLite[];
  jobsLoading: boolean;
}) {
  const [local, setLocal] = useState<ExportDraft>(props.value);
  return (
    <ExportDraftEditor
      value={local}
      onChange={(next) => {
        const family = unanimousAddressFamily(next.addresses);
        const matched = next.mode === "addresses" && family && chainFamilyOf(next.chain) !== family ? chainForFamily(props.chains, family) : "";
        const updated = matched ? { ...next, chain: matched } : next;
        setLocal(updated);
        props.onChange(updated);
      }}
      chains={props.chains}
      datasets={props.datasets}
      jobOptions={props.jobOptions}
      jobsLoading={props.jobsLoading}
      idPrefix=""
    />
  );
}

export default function ExportsPage() {
  const { session } = useAuth();
  const [params] = useSearchParams();
  return <ExportsWorkspace key={JSON.stringify([pageStateScope(session), params.toString()])} />;
}

function ExportsWorkspace() {
  const dialog = useDialog();
  const { can } = useAuth();
  const { busy, run, client } = useS5Action();
  const [sourceParams] = useSearchParams();

  const [statusFilter, setStatusFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState("");
  const [searchDraft, setSearchDraft] = useState("");
  const [searchFilter, setSearchFilter] = useState("");
  const [page, setPage] = useState(0);
  const [cursors, setCursors] = useState<Array<string | null>>([null]);
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [draft, setDraft] = useState<ExportDraft>(() => {
    const addressText = sourceParams.get("addresses") || sourceParams.get("address") || "";
    const requestedContent = sourceParams.get("content");
    return { ...EMPTY_DRAFT, chain: sourceParams.get("chain") || "", addressText, addresses: parseAddressInput(addressText),
      datasets: sourceParams.get("datasets")?.split(",").filter(Boolean) || [], content: requestedContent === "statistics" || requestedContent === "counterparties" ? requestedContent : "details",
      fromTime: sourceParams.get("from_time") || "", toTime: sourceParams.get("to_time") || "", token: sourceParams.get("token") || "", direction: sourceParams.get("direction") || "", counterparty: sourceParams.get("counterparty") || "",
      minAmount: sourceParams.get("min_amount") || "", maxAmount: sourceParams.get("max_amount") || "", asOf: sourceParams.get("as_of") || "",
      fromAddress:sourceParams.get("from_address") || "", toAddress:sourceParams.get("to_address") || "", hideZero:sourceParams.get("hide_zero")==="1",
    };
  });
  const notifiedError = useRef<string | null>(null);
  const draftRef = useRef<ExportDraft>(draft);
  const filesDialog = useRef<(() => void) | null>(null);
  useEffect(() => () => filesDialog.current?.(), []);

  const canListJobs = can("job:list:self");
  const exportsApi = useApi<ExportsListData>("/api/v2/exports", [page, statusFilter, formatFilter, searchFilter], {
    limit: 50,
    status: statusFilter || undefined,
    format: formatFilter || undefined,
    q: searchFilter || undefined,
    cursor: cursors[page] ?? undefined,
  });
  const chainsApi = useApi<{ items: ChainMeta[] }>("/api/v2/meta/chains");
  const datasetsApi = useApi<{ items: DatasetMeta[] }>("/api/v2/meta/datasets");
  const jobsApi = useApi<{ items: JobLite[] }>(canListJobs ? "/api/v2/jobs" : null, [], { limit: 100 });

  const exportsError = exportsApi.error;
  const reloadExports = exportsApi.reload;

  useEffect(() => {
    if (exportsError && notifiedError.current !== exportsError) {
      notifiedError.current = exportsError;
      dialog.notify("error", "导出列表加载失败", exportsError);
    }
  }, [exportsError, dialog]);

  useEffect(() => {
    setPage(0);
    setCursors([null]);
  }, [statusFilter, formatFilter, searchFilter]);

  const chainItems = chainsApi.data?.items ?? [];
  const datasetItems = datasetsApi.data?.items ?? [];
  const rows = exportsApi.data?.items ?? [];
  const previewRow = previewId ? rows.find((row) => row.id === previewId) ?? null : null;

  /** 草稿的唯一写入点：ref 供提交时读取，state 供内联表单渲染。 */
  const updateDraft = useCallback((next: ExportDraft) => {
    draftRef.current = next;
    setDraft(next);
  }, []);

  useEffect(() => {
    if (draftRef.current.chain !== "" || chainItems.length === 0) return;
    const first = chainItems.find((item) => item.enabled) ?? chainItems[0];
    updateDraft({ ...draftRef.current, chain: first.slug });
  }, [chainItems, updateDraft]);

  useEffect(() => {
    if (draft.mode !== "addresses") return;
    const family = unanimousAddressFamily(draft.addresses);
    if (!family || chainFamilyOf(draft.chain) === family) return;
    const matched = chainForFamily(chainItems, family);
    if (matched) updateDraft({ ...draftRef.current, chain: matched });
  }, [draft.mode, draft.addresses, draft.chain, chainItems, updateDraft]);

  const exportOptions = useCallback((current: ExportDraft) => ({
    format: current.format, include_meta: current.includeMeta, force_zip: current.forceZip,
    include_decoded_input: current.includeDecodedInput, content: current.content, file_layout: "dataset",
    filters: current.fromTime || current.toTime || current.token || current.direction || current.counterparty || current.fromAddress || current.toAddress || current.hideZero || current.minAmount || current.maxAmount || current.asOf || current.content !== "details" ? {
      from_time: current.fromTime || undefined, to_time: current.toTime || undefined, token: current.token || undefined,
      direction: current.direction || undefined, counterparty: current.counterparty || undefined,
      min_amount: current.minAmount || undefined, max_amount: current.maxAmount || undefined, as_of: current.asOf || undefined,
      from_address: current.fromAddress || undefined, to_address: current.toAddress || undefined, hide_zero: current.hideZero,
      timezone: sourceParams.get("timezone") || "Asia/Shanghai", interval: sourceParams.get("interval") || "day",
    } : undefined,
  }), [sourceParams]);

  useEffect(() => {
    if (draft.mode !== "addresses" || draft.addresses.length !== 1 || !draft.chain || !draft.datasets.length) return;
    const family = unanimousAddressFamily(draft.addresses);
    if (family && chainFamilyOf(draft.chain) !== family) return;
    const body = { chain: draft.chain, addresses: draft.addresses, datasets: draft.datasets, ...exportOptions(draft) };
    const timer = window.setTimeout(() => {
      void client.post("/api/v2/exports/prefetch", body).catch(() => {});
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [client, draft, exportOptions]);

  const jobOptions = useMemo(() => (jobsApi.data?.items ?? []).filter((job) => EXPORTABLE_JOB_STATUSES.has(job.status)), [jobsApi.data]);
  const activeStep = draft.mode === "job"
    ? UUID_RE.test(draft.jobId.trim()) ? "options" : "object"
    : !draft.chain || draft.addresses.length === 0 ? "object" : draft.datasets.length === 0 ? "data" : "options";
  const jumpToStep = (key: string) => {
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    document.getElementById(`inline-export-step-${key}`)?.scrollIntoView({
      block: "start",
      behavior: reduceMotion ? "instant" : "smooth",
    });
  };

  const createExport = useCallback(async () => {
    const current = draftRef.current;
    let body: Record<string, unknown>;
    if (current.mode === "job") {
      const jobId = current.jobId.trim();
      if (!UUID_RE.test(jobId)) {
        dialog.notify("warning", "请选择或填写任务 ID", "job_id 必须是服务端任务 UUID。");
        return;
      }
      body = {
        job_id: jobId,
        format: current.format,
        include_meta: current.includeMeta,
        force_zip: current.forceZip,
        include_decoded_input: current.includeDecodedInput,
      };
    } else {
      if (!current.chain) {
        dialog.notify("warning", "请选择链", "按地址导出时必须指定链。");
        return;
      }
      const addresses = current.addresses.length > 0 ? current.addresses : parseAddressInput(current.addressText);
      if (addresses.length === 0) {
        dialog.notify("warning", "请填写地址", "按地址导出时至少需要一个地址。");
        return;
      }
      if (addresses.length > MAX_EXPORT_ADDRESSES) {
        dialog.notify("warning", "地址数量超出上限", `单次导出最多 ${MAX_EXPORT_ADDRESSES} 个地址。`);
        return;
      }
      const families = new Set(addresses.map(addressChainFamily).filter(Boolean));
      if (families.size > 1) {
        dialog.notify("warning", "一次导出只能对应一条链", "请按链拆分地址后分别创建导出。");
        return;
      }
      const family = families.values().next().value;
      if (family && chainFamilyOf(current.chain) !== family) {
        dialog.notify("warning", "链与地址不匹配", `当前选择的是 ${current.chain}，地址属于 ${familyLabel(family)}。`);
        return;
      }
      body = {
        chain: current.chain,
        addresses,
        datasets: current.datasets,
        format: current.format,
        include_meta: current.includeMeta,
        force_zip: current.forceZip,
        include_decoded_input: current.includeDecodedInput,
      };
    }

    Object.assign(body, exportOptions(current));

    const response = await run(async () => {
      try {
        return await client.post<ExportCreateData>("/api/v2/exports", body, { idempotencyKey: crypto.randomUUID() });
      } catch (error) {
        if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
        throw error;
      }
    });
    if (!response) return;
    const data = response.data;
    await dialog.open({
      kind: "success",
      title: "导出任务已创建",
      details: [
        { label: "export_id", value: data.id },
        { label: "status", value: data.status },
        { label: "format", value: data.format },
        { label: "zip", value: String(data.zip) },
        { label: "job_id", value: data.job_id ?? "—" },
        { label: "地址数", value: String(data.addresses) },
        { label: "Dataset", value: (data.datasets ?? []).join("、") || "—" },
      ],
      actions: [{ label: "知道了", result: "ok", variant: "primary" }],
    });
    void reloadExports();
  }, [client, dialog, reloadExports, run, exportOptions]);

  const openCreate = useCallback(async () => {
    const answer = await dialog.open({
      kind: "info",
      title: "新建导出",
      body: (
        <DialogDraftEditor
          value={draftRef.current}
          onChange={updateDraft}
          chains={chainItems}
          datasets={datasetItems}
          jobOptions={jobOptions}
          jobsLoading={jobsApi.loading}
        />
      ),
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "创建导出", result: "create", variant: "primary" },
      ],
    });
    if (answer !== "create") return;
    await createExport();
  }, [chainItems, createExport, datasetItems, dialog, jobOptions, jobsApi.loading, updateDraft]);

  const openedSource = useRef(false);
  useEffect(() => {
    if (openedSource.current || !sourceParams.get("address") || chainsApi.loading || datasetsApi.loading) return;
    openedSource.current = true;
    if (can("export:create")) void openCreate();
  }, [sourceParams, chainsApi.loading, datasetsApi.loading, openCreate, can]);

  const downloadItem = useCallback(
    async (item: ExportItem) => {
      const answer = await dialog.open({
        kind: "confirm",
        title: "下载导出文件？",
        body: "下载票据 60 秒内有效且仅可使用一次；过期或重复使用会返回 410，需要重新创建票据。",
        details: [
          { label: "export_id", value: item.id },
          { label: "格式", value: `${item.format}${item.zip ? " · zip" : ""}` },
          { label: "行数", value: formatRawAmount(String(item.rows), 0) },
          { label: "SHA-256", value: item.sha256 || "—" },
          { label: "过期时间", value: item.expires_at ? formatTime(item.expires_at) : "—" },
        ],
        actions: [
          { label: "取消", result: "", variant: "default" },
          { label: "创建并下载", result: "download", variant: "primary" },
        ],
      });
      if (answer !== "download") return;
      const response = await run(async () => {
        try {
          return await client.post<{ ticket_id: string; url: string; expires_in_seconds: number; single_use: boolean }>(
            `/api/v2/exports/${encodeURIComponent(item.id)}/download-ticket`,
            undefined,
            { idempotencyKey: crypto.randomUUID() },
          );
        } catch (error) {
          if (error instanceof ApiError && error.traceId) setLastTrace(error.traceId);
          throw error;
        }
      });
      if (!response) return;
      // 直连通道优先（更快，不占用主站隧道带宽）；探测不可达时回退主站隧道 URL。
      const directUrl =
        response.data.ticket_id && (await canUseDirectDownload()) ? directTicketUrl(response.data.ticket_id) : "";
      window.location.assign(directUrl || response.data.url);
    },
    [client, dialog, run],
  );

  const showDetails = async (row: ExportItem) => {
    await dialog.open({ kind: "info", title: "导出详情", details: [
      { label: "导出 ID", value: row.id }, { label: "关联任务", value: row.job_id ?? "—" },
      { label: "数据集", value: row.dataset.split(/[+,]/).map(datasetLabel).join("、") },
      { label: "格式", value: row.format.toUpperCase() }, { label: "状态", value: jobStatusLabel(row.status) },
      { label: "行数", value: String(row.rows) }, { label: "SHA-256", value: row.sha256 || "—" },
      { label: "创建时间", value: formatTime(row.created_at) }, { label: "完成时间", value: formatTime(row.finished_at) },
      { label: "有效期", value: formatTime(row.expires_at) },
      ...(row.error_safe ? [{ label: "错误详情", value: row.error_safe }] : []),
    ], actions: [{ label: "关闭", result: "close", variant: "default" }] });
  };

  const explainCaliber = () => {
    dialog.notify(
      "info",
      "导出数据说明",
      "列表字段直接来自服务端 /api/v2/exports；界面不做推算，缺失字段显示 —。",
      [
        { label: "数据源", value: "/api/v2/exports（status 筛选 + cursor 分页）" },
        { label: "状态", value: "原样显示服务端 status（QUEUED / RUNNING / COMPLETED / FAILED / EXPIRED…）" },
        { label: "进度", value: "该契约不返回百分比，RUNNING / QUEUED 只显示不确定进度，不伪造百分比" },
        { label: "大小", value: "服务端读取已生成文件的实际字节数；文件未生成时显示 —" },
        { label: "SHA-256", value: "服务端产出的对象摘要，复制与悬停提示均为完整值" },
        { label: "过期时间", value: "expires_at 早于当前时间时标记为已过期，下载入口由 download_available 决定" },
        { label: "下载", value: "COMPLETED 且未过期时创建 60 秒单次票据后由浏览器直接下载" },
        { label: "地址上限", value: `单次导出最多 ${MAX_EXPORT_ADDRESSES} 个地址（服务端同样限制）` },
        { label: "Dataset", value: "只有状态为 available 的 Dataset 可选；未选择时服务端默认 transactions" },
        { label: "导出选项", value: "include_meta / force_zip / include_decoded_input 会进入服务端查询快照，影响导出内容与复用判定" },
      ],
    );
  };

  const pager = (
    <span className="toolbar">
      <span className="cs-section__count">本页 {rows.length} 条</span>
      <button type="button" className="btn btn--sm" disabled={page === 0} onClick={() => setPage((value) => Math.max(0, value - 1))}>
        上一页
      </button>
      <button
        type="button"
        className="btn btn--sm"
        disabled={!exportsApi.meta.next_cursor}
        onClick={() => {
          const next = exportsApi.meta.next_cursor;
          if (!next) return;
          setCursors((prev) => {
            const copy = [...prev];
            copy[page + 1] = next;
            return copy;
          });
          setPage((value) => value + 1);
        }}
      >
        下一页
      </button>
    </span>
  );

  const wide = useWideViewport();

  /*
   * L1 普通业务表格用 ProTable（§17.1 把 Exports 列在 L1）。
   *
   * 迁移前这里是 DataTable（L2 的高密度详情表控件）：行本身能画出表格，
   * 但少了 §17.2 要求的列状态、密度切换与**冻结主标识列**——
   * 导出列表横向滚动时，最左边的 export_id 必须一直看得见。
   */
  const table = (
    <div style={{ position: "relative", minWidth: 0 }}>
      <CsRefreshBar active={exportsApi.loading && rows.length > 0} />
      <CsProTable<ExportItem>
      loading={exportsApi.loading}
      /* §17.5：刷新保留旧行与表头，只在顶部显示进度，不清屏。 */
      keepPreviousData

      onRetry={() => void reloadExports()}
      rows={rows}
      pagination={false}
      expandedRowRender={can("export:read") ? row => <ExportFilesPanel id={row.id} available={row.download_available} client={client} run={run} busy={busy} canDownload={can("export:download")} /> : undefined}
      rowKey={(row) => row.id}
      onRowActivate={row => void showDetails(row)}
      rowActivateLabel={(row) => `预览导出 ${shortValue(row.id, 10, 6)}`}
      empty={
        <EmptyState
          title="没有符合筛选条件的导出"
          action={
            statusFilter ? (
              <button type="button" className="btn btn--sm" onClick={() => setStatusFilter("")}>
                清除筛选
              </button>
            ) : undefined
          }
        />
      }
      mobileCard={(row) => (
        <div className="cs-rowcard" aria-label={`导出 ${row.id}`}>
          <div className="cs-rowcard__head">
            <span className="cs-rowcard__title mono" title={row.id}>{shortValue(row.id, 10, 6)}</span>
            <StatusBadgeForState value={row.status} />
          </div>
          <div className="cs-rowcard__meta">
            <FormatTag format={row.format} zip={row.zip} />
            <span>{row.dataset || "—"}</span>
            <span>{formatRawAmount(String(row.rows), 0)} 行</span>
          </div>
          {row.job_id ? <div className="cs-rowcard__meta mono" title={row.job_id}>任务 {shortValue(row.job_id, 10, 6)}</div> : null}
          <div className="cs-rowcard__meta">创建 {formatTime(row.created_at)}</div>
          {row.expires_at ? <div className="cs-rowcard__meta">有效期 {formatTime(row.expires_at)}</div> : null}
          {row.status === "RUNNING" || row.status === "QUEUED" ? (
            <CompactProgress percent={null} state={progressStateFor(row.status)} label={`导出进度 ${row.id}`} />
          ) : null}
          <div className="cs-rowcard__actions">
            <button type="button" className="btn btn--sm" disabled={!row.download_available || !can("export:download") || busy} onClick={() => void downloadItem(row)}>
              下载文件
            </button>
            <HashValue value={row.id} label="导出 ID" />
            {row.sha256 ? <HashValue value={row.sha256} label="导出 SHA-256" /> : null}
            <AntButton type="link" onClick={() => void showDetails(row)}>详情</AntButton>
            {can("export:read") && <AntButton type="link" onClick={() => {
              let closeFiles = () => {};
              const view = dialog.openSession({kind:"info",title:"数据集文件",body:<ExportFilesPanel id={row.id} available={row.download_available} client={client} run={run} busy={busy} canDownload={can("export:download")} onBeforeDownload={() => closeFiles()} />});
              closeFiles = () => { view.dismiss(); filesDialog.current = null; };
              filesDialog.current = view.dismiss;
            }}>文件清单</AntButton>}
            {row.error_safe ? <button type="button" className="btn btn--sm btn--ghost" onClick={() => dialog.notify("error", "导出错误", row.error_safe)}>错误详情</button> : null}
          </div>
        </div>
      )}
      columns={[
        {
          key: "export",
          header: "导出 / 任务",
          width: "210px",
          /* §17.2：主标识列冻结，横向滚动时不跟着走。 */
          frozen: true,
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <span className="toolbar" style={{ gap: "var(--sp-1)" }}>
                {/* title 恒为完整 export_id：端到端脚本按它定位行（B2RAW-02）。 */}
                <span className="mono" title={row.id}>
                  {shortValue(row.id, 10, 6)}
                </span>
                <CopyButton value={row.id} label="导出 ID" />
              </span>
              <span className="mono" style={{ color: "var(--text-secondary)", fontSize: "var(--fs-12)" }} title={row.job_id ?? undefined}>
                {row.job_id ? shortValue(row.job_id, 10, 6) : "无关联任务"}
              </span>
            </div>
          ),
        },
        {
          key: "source",
          header: "来源",
          width: "150px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
              <span className="mono" style={{ overflowWrap: "anywhere" }}>
                {row.dataset || "—"}
              </span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-12)" }}>
                {row.object_ref ? "对象已生成" : "对象待生成"}
              </span>
            </div>
          ),
        },
        {
          key: "format",
          header: "格式",
          width: "130px",
          render: (row) => <FormatTag format={row.format} zip={row.zip} />,
        },
        {
          key: "status",
          header: "状态",
          width: "170px",
          render: (row) => {
            const expired = row.expires_at ? Date.parse(row.expires_at) <= Date.now() : false;
            return (
              <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                <StatusBadgeForState value={row.status} />
                {expired ? <StatusBadge tone="danger">已过期</StatusBadge> : null}
                <AntButton type="link" onClick={() => void showDetails(row)}>详情</AntButton>
            {row.error_safe ? (
                  <button
                    type="button"
                    className="btn btn--sm"
                    onClick={() =>
                      dialog.notify("error", "导出错误", row.error_safe, [
                        { label: "export_id", value: row.id },
                        { label: "status", value: row.status },
                        { label: "Trace", value: lastTrace ?? "—" },
                      ])
                    }
                  >
                    错误详情
                  </button>
                ) : null}
              </div>
            );
          },
        },
        {
          key: "progress",
          header: "进度",
          width: "170px",
          render: (row) => {
            const state = progressStateFor(row.status);
            if (state === "completed" || state === "failed" || state === "cancelled") {
              return <span style={{ color: "var(--text-secondary)" }}>—</span>;
            }
            /* 该契约不返回百分比：只用 indeterminate，不伪造进度。 */
            return <CompactProgress percent={null} state={state} label={`导出进度 ${shortValue(row.id, 10, 6)}`} />;
          },
        },
        {
          key: "rows",
          header: "行数",
          width: "110px",
          render: (row) => <span className="mono">{formatRawAmount(String(row.rows), 0)}</span>,
        },
        {
          key: "size",
          header: "大小",
          width: "90px",
          render: row => <span className="mono">{row.bytes == null ? "—" : formatBytes(row.bytes)}</span>,
        },
        {
          key: "sha",
          header: "SHA-256",
          width: "200px",
          render: (row) =>
            row.sha256 ? <HashValue value={row.sha256} label="导出 SHA-256" /> : <span style={{ color: "var(--text-secondary)" }}>—</span>,
        },
        {
          key: "time",
          header: "创建 / 完成",
          width: "170px",
          render: (row) => (
            <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
              <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
                {formatTime(row.created_at)}
              </span>
              <span style={{ color: "var(--text-secondary)", fontSize: "var(--fs-12)" }}>{formatTime(row.finished_at)}</span>
            </div>
          ),
        },
        {
          key: "expires",
          header: "有效期",
          width: "160px",
          render: (row) => <span className="mono">{row.expires_at ? formatTime(row.expires_at) : "—"}</span>,
        },
        {
          key: "actions",
          header: "操作",
          width: "130px",
          render: row => <Space><AntButton type="link" size="small" disabled={!row.download_available || !can("export:download") || busy} onClick={() => void downloadItem(row)}>下载</AntButton><AntButton type="link" size="small" onClick={() => void showDetails(row)}>详情</AntButton></Space>,
        },
      ]}
      />
    </div>
  );

  return (
    <div className="cs-workspace-focus cs-workspace-focus--exports">
      <CsPageHeader title="导出中心" breadcrumb={[{ label: "数据工具" }, { label: "导出中心" }]}
        primaryAction={can("export:create") ? <AntButton type="primary" disabled={busy} onClick={() => void openCreate()}>新建导出</AntButton> : undefined} />
      <AntCard>
        <Form layout="inline" className="cs-export-filter-form">
          <Form.Item label="导出 / 任务"><Input.Search aria-label="搜索导出或任务" placeholder="输入导出 ID、任务编号" value={searchDraft} style={{ width: 280 }}
            onChange={event => setSearchDraft(event.target.value)} onSearch={value => { setPage(0); setCursors([null]); setSearchFilter(value.trim()); }} allowClear /></Form.Item>
          <Form.Item label="状态"><Select aria-label="状态筛选" value={statusFilter} onChange={value => { setPage(0); setCursors([null]); setStatusFilter(value); }} options={[
            { value: "", label: "全部状态" }, ...["QUEUED", "RUNNING", "COMPLETED", "FAILED", "EXPIRED"].map(value => ({ value, label: jobStatusLabel(value) })),
          ]} /></Form.Item>
          <Form.Item label="格式"><Select aria-label="格式筛选" value={formatFilter} onChange={value => { setPage(0); setCursors([null]); setFormatFilter(value); }} options={[
            { value: "", label: "全部格式" }, ...FORMATS.map(value => ({ value, label: value.toUpperCase() })),
          ]} /></Form.Item>
          <AntButton type="link" onClick={() => { setSearchDraft(""); setSearchFilter(""); setStatusFilter(""); setFormatFilter(""); setPage(0); setCursors([null]); }}>重置</AntButton>
        </Form>
      </AntCard>
      <AntCard className="cs-export-list-card">
        <div className="cs-export-list-toolbar"><h2>导出列表</h2><Space><AntButton onClick={() => void reloadExports()} loading={exportsApi.loading}>刷新</AntButton>
          <AntButton type="text" aria-label="导出数据说明" onClick={explainCaliber}>数据说明</AntButton></Space></div>
        {table}
        <div className="cs-export-list-toolbar">{pager}</div>
      </AntCard>
    </div>
  );
}

interface DatasetFile { dataset: string; name: string; rows: number; bytes: number; sha256: string; download_available: boolean }
function ExportFilesPanel({ id, available, client, run, busy, canDownload, onBeforeDownload }: { id: string; available: boolean; client: ReturnType<typeof useS5Action>["client"]; run: ReturnType<typeof useS5Action>["run"]; busy: boolean; canDownload: boolean; onBeforeDownload?: () => void }) {
  const dialog = useDialog();
  const [files, setFiles] = useState<DatasetFile[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    setFiles([]); setLoading(true);
    void client.get<{ files: DatasetFile[] }>(`/api/v2/exports/${encodeURIComponent(id)}`, { signal: controller.signal }).then(response => {
      if (!controller.signal.aborted) setFiles(response.data.files || []);
    }).catch(error => {
      if (!controller.signal.aborted) dialog.notify("error", "文件清单加载失败", error instanceof ApiError ? error.userMessage : "加载失败");
    }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [client, id, dialog]);
  const download = async (file: DatasetFile) => {
    onBeforeDownload?.();
    const answer = await dialog.confirm({ title: "下载数据集文件？", details: [{ label: "文件", value: file.name }, { label: "行数", value: String(file.rows) }, { label: "SHA-256", value: file.sha256 }], actions: [{ label: "取消", result: "", variant: "default" }, { label: "下载", result: "download", variant: "primary" }] });
    if (answer !== "download") return;
    const response = await run(() => client.post<{ url: string }>(`/api/v2/exports/${encodeURIComponent(id)}/files/${encodeURIComponent(file.dataset)}/download-ticket`, undefined, { idempotencyKey: crypto.randomUUID() }));
    if (response) window.location.assign(response.data.url);
  };
  return <Table<DatasetFile> rowKey="dataset" size="small" dataSource={files} loading={loading} pagination={false} scroll={{ x: 680 }} locale={{ emptyText: available ? "该导出未记录文件清单" : "文件生成中" }} columns={[
    { title: "数据集 / 文件", key: "file", render: (_, row) => <Space direction="vertical" size={0}><span>{datasetLabel(row.dataset)}</span><span>{row.name}</span></Space> },
    { title: "行数", dataIndex: "rows", render: value => formatRawAmount(String(value), 0) },
    { title: "大小", dataIndex: "bytes", render: value => formatBytes(value) },
    { title: "SHA-256", key: "sha", render: (_, row) => <HashValue value={row.sha256} label="数据集 SHA-256" /> },
    { title: "操作", key: "actions", render: (_, row) => <AntButton type="link" disabled={!available || !row.download_available || !canDownload || busy} onClick={() => void download(row)}>下载文件</AntButton> },
  ]} />;
}
