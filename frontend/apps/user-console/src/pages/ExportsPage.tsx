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
  MetaLine,
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
import { formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";
import {
  addressChainFamily,
  chainFamilyOf,
  chainForFamily,
  familyLabel,
  unanimousAddressFamily,
  type AddressChainFamily,
} from "../app/chain-address";
import "./exports.css";
import "./workspaces.css";

/* ============================================================================
 * U-EXPORT-01 /exports：导出列表 + 新建导出 + 60 秒一次性下载票据。
 *
 * 数据层与契约完全不变：POST /api/v2/exports（Idempotency-Key）、
 * POST /api/v2/exports/{id}/download-ticket、状态筛选与 cursor 分页全部照旧；
 * 表单字段 id、按钮文字与格式选项保持原样（R10 / B2RAW 端到端脚本依赖）。
 *
 * 表现层重构：上半区为内联创建导出，下半区为导出列表（状态 / 来源 / 格式 / 行数 /
 * 大小 / hash / 过期时间 / 操作），RUNNING 用 indeterminate 进度，格式用图标 + 文字标签。
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
  /** 时间范围（可选，datetime-local 的本地时间字符串）。留空即导出全部。 */
  rangeStart: string;
  rangeEnd: string;
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
  rangeStart: "",
  rangeEnd: "",
};

function parseAddressInput(raw: string): string[] {
  const seen = new Set<string>();
  const list: string[] = [];
  for (const token of raw.split(/[\s,;，；、|]+/)) {
    const value = token.trim();
    if (!value) continue;
    const key = value.toLowerCase();
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
    <div className="cs-export-editor">
      <section className="cs-export-editor__section" id={id("export-step-object")}>
        <h3>① 导出对象</h3>
        <div className="s4-form-grid">
      <Field label="导出对象" htmlFor={id("export-mode")}>
        <select
          id={id("export-mode")}
          className="select"
          value={draft.mode}
          onChange={(event) => update({ mode: event.target.value as ExportDraft["mode"] })}
        >
          <option value="addresses">按地址 + 链</option>
          <option value="job">按任务 job_id</option>
        </select>
      </Field>

      {draft.mode === "job" ? (
        jobOptions.length > 0 ? (
          <Field label="选择已完成/暂停任务" htmlFor={id("export-job-select")}>
            <select
              id={id("export-job-select")}
              className="select"
              value={draft.jobId}
              onChange={(event) => update({ jobId: event.target.value })}
            >
              <option value="">请选择任务</option>
              {jobOptions.map((job) => (
                <option key={job.id} value={job.id}>
                  {job.job_no} · {job.status} · {job.chain}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field label="任务 ID（job_id）" htmlFor={id("export-job-id")}>
            <input
              id={id("export-job-id")}
              className="input"
              placeholder={jobsLoading ? "正在读取我的任务…" : "粘贴任务 UUID"}
              value={draft.jobId}
              onChange={(event) => update({ jobId: event.target.value })}
            />
          </Field>
        )
      ) : (
        <>
          <Field label="链" htmlFor={id("export-chain")}>
            <select
              id={id("export-chain")}
              className="select"
              value={draft.chain}
              onChange={(event) => update({ chain: event.target.value })}
            >
              <option value="">请选择链</option>
              {chains.map((chain) => (
                <option key={chain.slug} value={chain.slug} disabled={!chain.enabled}>
                  {chain.name}（{chain.slug}）
                </option>
              ))}
            </select>
          </Field>
          <div className="field--wide">
            <Field label={`地址（已解析 ${draft.addresses.length} 个）`} htmlFor={id("export-addresses")}>
              <textarea
                id={id("export-addresses")}
                className="textarea"
                placeholder="每行一个地址，或使用逗号/空白分隔"
                value={draft.addressText}
                onChange={(event) => update({ addressText: event.target.value, addresses: parseAddressInput(event.target.value) })}
              />
            </Field>
          </div>
        </>
      )}
        </div>
      </section>

      <section className="cs-export-editor__section" id={id("export-step-data")}>
        <h3>② 数据与格式</h3>
        <div className="s4-form-grid">
      <Field label="格式" htmlFor={id("export-format")}>
        <select
          id={id("export-format")}
          className="select"
          value={draft.format}
          onChange={(event) => update({ format: event.target.value as ExportFormat })}
        >
          {FORMATS.map((format) => (
            <option key={format} value={format}>
              {format}
            </option>
          ))}
        </select>
      </Field>

      <div className="field--wide">
        <DatasetSelector
          datasets={datasets.map((dataset) => ({
            value: dataset.id,
            label: dataset.label,
            hint: `（${dataset.id}）`,
            disabled: dataset.status !== "available",
          }))}
          values={draft.datasets}
          onChange={(next) => update({ datasets: next })}
          label="Dataset"
        />
      </div>

      {/* 时间范围（可选）：留空即导出全部。两个控件都是 datetime-local，
          走本地时间输入、由 buildExportRange 转成带时区的 ISO 串。 */}
      <Field label="开始时间（可选）" htmlFor={id("export-range-start")}>
        <input
          id={id("export-range-start")}
          className="input"
          type="datetime-local"
          value={draft.rangeStart}
          onChange={(event) => update({ rangeStart: event.target.value })}
        />
      </Field>
      <Field label="结束时间（可选）" htmlFor={id("export-range-end")}>
        <input
          id={id("export-range-end")}
          className="input"
          type="datetime-local"
          value={draft.rangeEnd}
          onChange={(event) => update({ rangeEnd: event.target.value })}
        />
      </Field>
        </div>
      </section>

      <section className="cs-export-editor__section" id={id("export-step-options")}>
        <h3>③ 导出选项</h3>
        <div className="cs-export-editor__options">
        <Checkbox checked={draft.includeMeta} onChange={(next) => update({ includeMeta: next })} label="include_meta" />
        <Checkbox checked={draft.forceZip} onChange={(next) => update({ forceZip: next })} label="force_zip" />
        <Checkbox
          checked={draft.includeDecodedInput}
          onChange={(next) => update({ includeDecodedInput: next })}
          label="include_decoded_input"
        />
        </div>
      </section>

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
        setLocal(next);
        props.onChange(next);
      }}
      chains={props.chains}
      datasets={props.datasets}
      jobOptions={props.jobOptions}
      jobsLoading={props.jobsLoading}
      idPrefix=""
    />
  );
}

/*
 * buildAddressExportBody 把"按地址导出"的草稿变成请求体。
 *
 * 预生成（prefetch）与真正导出必须发出**完全一致的请求体**：服务端按请求体算规格
 * 哈希来判定命中，任何一个字段的有无或取值不同都会让命中率归零（而且不会报错，
 * 只是每次都白算一遍）。因此这里是唯一的构造点，两条路径都走它。
 *
 * 返回 { error } 表示草稿还不完整或自相矛盾，调用方按原有方式提示用户。
 */
function buildAddressExportBody(current: ExportDraft): { body?: Record<string, unknown>; error?: { title: string; detail: string } } {
  if (!current.chain) {
    return { error: { title: "请选择链", detail: "按地址导出时必须指定链。" } };
  }
  const addresses = current.addresses.length > 0 ? current.addresses : parseAddressInput(current.addressText);
  if (addresses.length === 0) {
    return { error: { title: "请填写地址", detail: "按地址导出时至少需要一个地址。" } };
  }
  if (addresses.length > MAX_EXPORT_ADDRESSES) {
    return { error: { title: "地址数量超出上限", detail: `单次导出最多 ${MAX_EXPORT_ADDRESSES} 个地址。` } };
  }
  const families = new Set<AddressChainFamily>();
  for (const address of addresses) {
    const family = addressChainFamily(address);
    if (family !== "") families.add(family);
  }
  if (families.size > 1) {
    const names = [...families].map((family) => familyLabel(family)).join(" 与 ");
    return { error: { title: "一次导出只能对应一条链", detail: `地址里同时出现了 ${names}。请按链拆分后分别创建导出。` } };
  }
  const family = families.values().next().value;
  if (family && chainFamilyOf(current.chain) !== family) {
    return { error: { title: "链与地址不匹配", detail: `当前选择的是 ${current.chain}，但地址属于 ${familyLabel(family)}。请切换链后再创建导出。` } };
  }
  const range = buildExportRange(current);
  if (range.error) return { error: range.error };
  return {
    body: {
      chain: current.chain,
      addresses,
      datasets: current.datasets,
      format: current.format,
      include_meta: current.includeMeta,
      force_zip: current.forceZip,
      include_decoded_input: current.includeDecodedInput,
      ...range.body,
    },
  };
}

/**
 * 把草稿里的时间范围转成请求字段。
 *
 * 语义（与服务端一致）：闭区间；两端都可省略，都省略就是**全量导出**。
 * 服务端按 start_time/end_time 生成键集分页查询，因此"全量"不再有行数上限，
 * 也不会出现"状态成功但少了行"的情况。
 *
 * 输入控件是 datetime-local：它给出的是**本地时间**（例如 2026-01-02T03:04），
 * 必须转成带时区的 ISO 串再发出去，否则服务端会按 UTC 解析，用户选的范围会整体偏移。
 */
function buildExportRange(current: ExportDraft): { body?: Record<string, unknown>; error?: { title: string; detail: string } } {
  const toIso = (raw: string): string | null | undefined => {
    const value = raw.trim();
    if (!value) return undefined;
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return null;
    return parsed.toISOString();
  };
  const start = toIso(current.rangeStart);
  const end = toIso(current.rangeEnd);
  if (start === null || end === null) {
    return { error: { title: "时间范围格式不正确", detail: "请重新选择开始与结束时间。" } };
  }
  if (start && end && new Date(start) > new Date(end)) {
    return { error: { title: "时间范围不合法", detail: "开始时间不能晚于结束时间。" } };
  }
  const body: Record<string, unknown> = {};
  if (start) body.start_time = start;
  if (end) body.end_time = end;
  return { body };
}

export default function ExportsPage() {
  const dialog = useDialog();
  const { can } = useAuth();
  const { busy, run, client } = useS5Action();

  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(0);
  const [cursors, setCursors] = useState<Array<string | null>>([null]);
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  const [previewId, setPreviewId] = useState<string | null>(null);
  const [draft, setDraft] = useState<ExportDraft>({ ...EMPTY_DRAFT });
  const notifiedError = useRef<string | null>(null);
  const draftRef = useRef<ExportDraft>(draft);

  const canListJobs = can("job:list:self");
  const exportsApi = useApi<ExportsListData>("/api/v2/exports", [page, statusFilter], {
    limit: 50,
    status: statusFilter || undefined,
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
  }, [statusFilter]);

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

  /*
   * 地址形状决定链：链选择的默认值是注册表第一条（arbitrum），而地址框接受
   * EVM / TRON / Solana 三种形状。形状与当前链不同族时**在输入时就切换**，
   * 否则导出会用 arbitrum 去查 TRON 地址，结果为空。
   * 多族混合不在这里猜——createExport 会拦截并提示按链拆分。
   */
  useEffect(() => {
    const current = draftRef.current;
    if (current.mode !== "addresses") return;
    const list = current.addresses.length > 0 ? current.addresses : parseAddressInput(current.addressText);
    const family = unanimousAddressFamily(list);
    if (family === "" || chainFamilyOf(current.chain) === family) return;
    const matched = chainForFamily(chainItems, family);
    if (matched && matched !== current.chain) updateDraft({ ...current, chain: matched });
  }, [draft.addressText, draft.addresses, draft.mode, draft.chain, chainItems, updateDraft]);

  /*
   * 预生成：草稿（防抖后）一稳定就请服务端先生成产物，用户真正点"导出"时命中即返回。
   *
   * 为什么值得做：一份 173 万行的 csv+zip 实测要 17 秒生成，而用户在表单上通常要花
   * 30-60 秒——这段时间足够把产物备好，命中时服务端只做一次数据库提升（实测 5 ms）。
   *
   * 刻意的取舍：
   *   - **只对"单地址"触发**。数据集个数不限：多数据集服务端会强制 ZIP 并组装多条目，
   *     实测同样能命中（173 万行 zip 命中 7 ms）。多地址要按地址扇出文件、产物结构不同，
   *     服务端会回 200 但 prefetched=false，因此这里不必发。
   *   - **静默**：预生成失败、或服务端不支持，都不打扰用户——它只是省时间的投机行为，
   *     点导出时照常走正常路径。因此这里不弹任何提示。
   *   - 请求体必须与 createExport 完全一致，所以两条路径共用 buildAddressExportBody。
   */
  useEffect(() => {
    if (draft.mode !== "addresses") return;
    if (draft.addresses.length !== 1) return;
    const built = buildAddressExportBody(draftRef.current);
    if (!built.body) return;
    const timer = window.setTimeout(() => {
      void client.post("/api/v2/exports/prefetch", built.body).catch(() => {
        /* 投机行为，失败静默 */
      });
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [
    client,
    draft.mode,
    draft.chain,
    draft.addressText,
    draft.addresses,
    draft.datasets,
    draft.format,
    draft.includeMeta,
    draft.forceZip,
    draft.includeDecodedInput,
    draft.rangeStart,
    draft.rangeEnd,
  ]);

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
      const built = buildAddressExportBody(current);
      if (built.error) {
        dialog.notify("warning", built.error.title, built.error.detail);
        return;
      }
      body = built.body!;
    }

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
  }, [client, dialog, reloadExports, run]);

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

  const downloadItem = useCallback(
    async (item: ExportItem) => {
      const answer = await dialog.open({
        kind: "confirm",
        title: "创建下载票据？",
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

  const explainCaliber = () => {
    dialog.notify(
      "info",
      "导出列表口径",
      "列表字段直接来自服务端 /api/v2/exports；界面不做推算，缺失字段显示 —。",
      [
        { label: "数据源", value: "/api/v2/exports（status 筛选 + cursor 分页）" },
        { label: "状态", value: "原样显示服务端 status（QUEUED / RUNNING / COMPLETED / FAILED / EXPIRED…）" },
        { label: "进度", value: "该契约不返回百分比，RUNNING / QUEUED 只显示不确定进度，不伪造百分比" },
        { label: "大小", value: "该契约不返回文件字节数，列为 —；实际大小在下载后由文件本身给出" },
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
      error={exportsApi.error ? "导出列表加载失败，请重试。" : null}
      onRetry={() => void reloadExports()}
      rows={rows}
      rowKey={(row) => row.id}
      onRowActivate={wide ? (row) => setPreviewId(row.id) : undefined}
      rowActivateLabel={(row) => `预览导出 ${shortValue(row.id, 10, 6)}`}
      empty={
        <EmptyState
          title="没有符合筛选条件的导出"
          body="调整状态筛选后重试；筛选条件会保留。"
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
            <button type="button" className="btn btn--sm" disabled={!row.download_available || busy} onClick={() => void downloadItem(row)}>
              下载票据
            </button>
            <HashValue value={row.id} label="导出 ID" />
            {row.sha256 ? <HashValue value={row.sha256} label="导出 SHA-256" /> : null}
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
          render: () => (
            <span className="mono" title="该契约不返回文件字节数" style={{ color: "var(--text-secondary)" }}>
              —
            </span>
          ),
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
          render: (row) => (
            <button
              type="button"
              className="btn btn--sm"
              disabled={!row.download_available || busy}
              onClick={() => void downloadItem(row)}
            >
              下载票据
            </button>
          ),
        },
      ]}
      />
    </div>
  );

  const aside =
    wide && previewRow ? (
      <InspectorDrawer
        title={shortValue(previewRow.id, 10, 6)}
        subtitle={<StatusBadgeForState value={previewRow.status} />}
        ariaLabel="导出快速预览"
        onClose={() => setPreviewId(null)}
        actions={
          <button
            type="button"
            className="btn btn--primary btn--sm"
            disabled={!previewRow.download_available || busy}
            onClick={() => void downloadItem(previewRow)}
          >
            下载票据
          </button>
        }
      >
        <KeyValueList
          items={[
            { label: "export_id", value: <HashValue value={previewRow.id} wrap label="导出 ID" />, wide: true },
            { label: "关联任务", value: previewRow.job_id ?? "无关联任务", wide: true },
            { label: "Dataset", value: previewRow.dataset || "—" },
            { label: "格式", value: `${previewRow.format}${previewRow.zip ? " · zip" : ""}` },
            { label: "行数", value: formatRawAmount(String(previewRow.rows), 0) },
            { label: "大小", value: "—（契约未返回字节数）" },
            {
              label: "SHA-256",
              value: previewRow.sha256 ? <HashValue value={previewRow.sha256} wrap label="导出 SHA-256" /> : "—",
              wide: true,
            },
            { label: "对象引用", value: previewRow.object_ref || "—", wide: true },
            { label: "创建时间", value: formatTime(previewRow.created_at) },
            { label: "更新时间", value: formatTime(previewRow.updated_at) },
            { label: "完成时间", value: formatTime(previewRow.finished_at) },
            { label: "过期时间", value: previewRow.expires_at ? formatTime(previewRow.expires_at) : "—" },
            { label: "可下载", value: previewRow.download_available ? "是" : "否" },
            { label: "错误摘要", value: previewRow.error_safe || "—", wide: true },
          ]}
        />
      </InspectorDrawer>
    ) : undefined;

  return (
    <div className="cs-workspace-focus cs-workspace-focus--exports">
      <CsPageHeader
        title="导出中心"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "导出中心" }]}
        meta={
          <MetaLine
            items={[
              { label: "状态筛选", value: statusFilter || "全部状态" },
              { label: "本页", value: `${rows.length} 条` },
              { label: "数据源", value: "/api/v2/exports" },
            ]}
          />
        }
        secondaryAction={
          <span className="toolbar">
            <select
              className="select"
              aria-label="状态筛选"
              value={statusFilter}
              onChange={(event) => setStatusFilter(event.target.value)}
            >
              <option value="">全部状态</option>
              <option value="QUEUED">QUEUED</option>
              <option value="RUNNING">RUNNING</option>
              <option value="COMPLETED">COMPLETED</option>
              <option value="FAILED">FAILED</option>
              <option value="EXPIRED">EXPIRED</option>
            </select>
            <button type="button" className="btn btn--sm" onClick={() => void reloadExports()}>
              刷新
            </button>
            {lastTrace ? (
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => dialog.notify("info", "最近一次操作 Trace", null, [{ label: "Trace", value: lastTrace }])}
              >
                Trace
              </button>
            ) : null}
            <button type="button" className="btn btn--sm" onClick={explainCaliber}>
              口径说明
            </button>
          </span>
        }
        primaryAction={
          <button type="button" className="btn btn--primary btn--sm" disabled={busy} onClick={() => void openCreate()}>
            新建导出
          </button>
        }
      />

      <PageSection title="创建导出" count={draft.mode === "job" ? "按任务 job_id" : "按地址 + 链"}>
        <Stepper steps={EXPORT_STEPS} current={activeStep} onJump={jumpToStep} ariaLabel="新建导出步骤" />
        <SplitPane
          main={
            <div className="cs-export-main">
              <ExportDraftEditor
                value={draft}
                onChange={updateDraft}
                chains={chainItems}
                datasets={datasetItems}
                jobOptions={jobOptions}
                jobsLoading={jobsApi.loading}
                idPrefix="inline-"
              />
            </div>
          }
          aside={
            <div className="cs-export-summary">
              <InspectorDrawer title="导出摘要" ariaLabel="导出摘要">
                <KeyValueList items={[
                  { label: "导出对象", value: draft.mode === "job" ? "任务" : "地址 + 链" },
                  { label: "链", value: draft.mode === "job" ? "由任务确定" : draft.chain || "—" },
                  { label: "任务 ID", value: draft.mode === "job" ? draft.jobId || "—" : "—", wide: true },
                  { label: "地址数", value: draft.mode === "addresses" ? `${draft.addresses.length} / ${MAX_EXPORT_ADDRESSES}` : "—" },
                  { label: "Dataset", value: draft.mode === "job" ? "由任务确定" : draft.datasets.join("、") || "—", wide: true },
                  { label: "格式", value: draft.format.toUpperCase() },
                  { label: "包含元数据", value: draft.includeMeta ? "是" : "否" },
                  { label: "ZIP", value: draft.forceZip ? "强制" : "按服务端规则" },
                  { label: "解码输入", value: draft.includeDecodedInput ? "是" : "否" },
                ]} />
              </InspectorDrawer>
            </div>
          }
          storageKey="cs-export-new-aside"
          defaultAsideWidth={340}
          minAsideWidth={300}
          maxAsideWidth={460}
          asideLabel="导出摘要"
        />
        <StickyActionBar leading={<span className="cs-section__count">{draft.mode === "job" ? "按任务导出" : `已解析 ${draft.addresses.length} 个地址`}</span>}>
          <button type="button" className="btn btn--primary" disabled={busy} onClick={() => void createExport()}>
            创建导出
          </button>
        </StickyActionBar>
      </PageSection>

      <SplitPane
        main={
          <PageSection title="导出列表" count={`${rows.length} 条`} actions={pager}>
            {table}
          </PageSection>
        }
        aside={aside}
        storageKey="cs.exports.preview.width"
        defaultAsideWidth={380}
        asideLabel="导出快速预览"
      />
    </div>
  );
}
