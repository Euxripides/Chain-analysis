import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ApiError, useAuth } from "@chain/api-client";
import { Card, DataTable, EmptyState, Field, PageHeader, StatusBadge, statusToneFor, useDialog } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/* ============================================================================
 * U-EXPORT-01 /exports：导出列表 + Dialog 新建导出 + 60 秒一次性下载票据。
 * 所有说明/警告/成功/错误只通过 Dialog；页面只展示真实 API 数据。
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
}

interface DraftRef {
  current: ExportDraft;
}

const FORMATS: ExportFormat[] = ["csv", "xlsx", "jsonl", "parquet"];
const EXPORTABLE_JOB_STATUSES = new Set(["COMPLETED", "PAUSED", "PAUSED_FALLBACK_TOO_EXPENSIVE"]);
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

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
};

function parseAddressInput(raw: string): string[] {
  const seen = new Set<string>();
  const list: string[] = [];
  for (const token of raw.split(/[\s,;，；|]+/)) {
    const value = token.trim();
    if (!value) continue;
    const key = value.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    list.push(value);
  }
  return list;
}

function ExportDraftEditor({
  draftRef,
  chains,
  datasets,
  jobOptions,
  jobsLoading,
}: {
  draftRef: DraftRef;
  chains: ChainMeta[];
  datasets: DatasetMeta[];
  jobOptions: JobLite[];
  jobsLoading: boolean;
}) {
  const [draft, setDraft] = useState<ExportDraft>(() => ({ ...draftRef.current }));

  const update = (patch: Partial<ExportDraft>) => {
    const next = { ...draft, ...patch };
    draftRef.current = next;
    setDraft(next);
  };

  const toggleDataset = (id: string) => {
    update({ datasets: draft.datasets.includes(id) ? draft.datasets.filter((item) => item !== id) : [...draft.datasets, id] });
  };

  return (
    <div className="s4-form-grid">
      <Field label="导出对象" htmlFor="export-mode">
        <select id="export-mode" className="select" value={draft.mode} onChange={(event) => update({ mode: event.target.value as ExportDraft["mode"] })}>
          <option value="addresses">按地址 + 链</option>
          <option value="job">按任务 job_id</option>
        </select>
      </Field>

      {draft.mode === "job" ? (
        jobOptions.length > 0 ? (
          <Field label="选择已完成/暂停任务" htmlFor="export-job-select">
            <select id="export-job-select" className="select" value={draft.jobId} onChange={(event) => update({ jobId: event.target.value })}>
              <option value="">请选择任务</option>
              {jobOptions.map((job) => (
                <option key={job.id} value={job.id}>
                  {job.job_no} · {job.status} · {job.chain}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field label="任务 ID（job_id）" htmlFor="export-job-id">
            <input
              id="export-job-id"
              className="input"
              placeholder={jobsLoading ? "正在读取我的任务…" : "粘贴任务 UUID"}
              value={draft.jobId}
              onChange={(event) => update({ jobId: event.target.value })}
            />
          </Field>
        )
      ) : (
        <>
          <Field label="链" htmlFor="export-chain">
            <select id="export-chain" className="select" value={draft.chain} onChange={(event) => update({ chain: event.target.value })}>
              <option value="">请选择链</option>
              {chains.map((chain) => (
                <option key={chain.slug} value={chain.slug} disabled={!chain.enabled}>
                  {chain.name}（{chain.slug}）
                </option>
              ))}
            </select>
          </Field>
          <div className="field field--wide">
            <Field label={`地址（已解析 ${draft.addresses.length} 个）`} htmlFor="export-addresses">
              <textarea
                id="export-addresses"
                className="textarea"
                placeholder="每行一个地址，或使用逗号/空白分隔"
                value={draft.addressText}
                onChange={(event) => update({ addressText: event.target.value, addresses: parseAddressInput(event.target.value) })}
              />
            </Field>
          </div>
        </>
      )}

      <Field label="格式" htmlFor="export-format">
        <select id="export-format" className="select" value={draft.format} onChange={(event) => update({ format: event.target.value as ExportFormat })}>
          {FORMATS.map((format) => (
            <option key={format} value={format}>
              {format}
            </option>
          ))}
        </select>
      </Field>
      <div className="field field--wide">
        <span className="field__label">Dataset</span>
        {datasets.length === 0 ? (
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>Dataset 元数据不可用；未选择时服务端默认 transactions。</span>
        ) : (
          <div className="toolbar">
            {datasets.map((dataset) => (
              <label key={dataset.id} className="btn btn--sm" style={{ cursor: dataset.status === "available" ? "pointer" : "not-allowed" }}>
                <input
                  type="checkbox"
                  checked={draft.datasets.includes(dataset.id)}
                  disabled={dataset.status !== "available"}
                  onChange={() => toggleDataset(dataset.id)}
                />
                {dataset.label}（{dataset.id}）
              </label>
            ))}
          </div>
        )}
      </div>
      <div className="field field--wide toolbar">
        <label className="btn btn--sm" style={{ cursor: "pointer" }}>
          <input type="checkbox" checked={draft.includeMeta} onChange={(event) => update({ includeMeta: event.target.checked })} />
          include_meta
        </label>
        <label className="btn btn--sm" style={{ cursor: "pointer" }}>
          <input type="checkbox" checked={draft.forceZip} onChange={(event) => update({ forceZip: event.target.checked })} />
          force_zip
        </label>
        <label className="btn btn--sm" style={{ cursor: "pointer" }}>
          <input type="checkbox" checked={draft.includeDecodedInput} onChange={(event) => update({ includeDecodedInput: event.target.checked })} />
          include_decoded_input
        </label>
      </div>
    </div>
  );
}
export default function ExportsPage() {
  const dialog = useDialog();
  const { can } = useAuth();
  const { busy, run, client } = useS5Action();

  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState(0);
  const [cursors, setCursors] = useState<Array<string | null>>([null]);
  const [lastTrace, setLastTrace] = useState<string | null>(null);
  const notifiedError = useRef<string | null>(null);
  const draftRef = useRef<ExportDraft>({ ...EMPTY_DRAFT });

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

  useEffect(() => {
    if (draftRef.current.chain !== "" || chainItems.length === 0) return;
    const first = chainItems.find((item) => item.enabled) ?? chainItems[0];
    draftRef.current = { ...draftRef.current, chain: first.slug };
  }, [chainItems]);

  const jobOptions = useMemo(() => (jobsApi.data?.items ?? []).filter((job) => EXPORTABLE_JOB_STATUSES.has(job.status)), [jobsApi.data]);

  const openCreate = useCallback(async () => {
    const answer = await dialog.open({
      kind: "info",
      title: "新建导出",
      body: (
        <ExportDraftEditor draftRef={draftRef} chains={chainItems} datasets={datasetItems} jobOptions={jobOptions} jobsLoading={jobsApi.loading} />
      ),
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "创建导出", result: "create", variant: "primary" },
      ],
    });
    if (answer !== "create") return;

    const draft = draftRef.current;
    let body: Record<string, unknown>;
    if (draft.mode === "job") {
      const jobId = draft.jobId.trim();
      if (!UUID_RE.test(jobId)) {
        dialog.notify("warning", "请选择或填写任务 ID", "job_id 必须是服务端任务 UUID。");
        return;
      }
      body = {
        job_id: jobId,
        format: draft.format,
        include_meta: draft.includeMeta,
        force_zip: draft.forceZip,
        include_decoded_input: draft.includeDecodedInput,
      };
    } else {
      if (!draft.chain) {
        dialog.notify("warning", "请选择链", "按地址导出时必须指定链。");
        return;
      }
      const addresses = draft.addresses.length > 0 ? draft.addresses : parseAddressInput(draft.addressText);
      if (addresses.length === 0) {
        dialog.notify("warning", "请填写地址", "按地址导出时至少需要一个地址。");
        return;
      }
      if (addresses.length > 100) {
        dialog.notify("warning", "地址数量超出上限", "单次导出最多 100 个地址。");
        return;
      }
      body = {
        chain: draft.chain,
        addresses,
        datasets: draft.datasets,
        format: draft.format,
        include_meta: draft.includeMeta,
        force_zip: draft.forceZip,
        include_decoded_input: draft.includeDecodedInput,
      };
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
  }, [chainItems, client, datasetItems, dialog, jobOptions, jobsApi.loading, reloadExports, run]);

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
      window.location.assign(response.data.url);
    },
    [client, dialog, run],
  );

  return (
    <>
      <PageHeader
        title="导出中心"
        actions={
          <span className="toolbar">
            <select className="select" aria-label="状态筛选" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}>
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
            <button type="button" className="btn btn--sm btn--primary" disabled={busy} onClick={() => void openCreate()}>
              新建导出
            </button>
          </span>
        }
      />
      <Card
        flush
        actions={
          <span className="toolbar">
            <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>本页 {rows.length} 条</span>
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
            {lastTrace ? (
              <button
                type="button"
                className="btn btn--sm"
                onClick={() => dialog.notify("info", "最近一次操作 Trace", null, [{ label: "Trace", value: lastTrace }])}
              >
                Trace
              </button>
            ) : null}
          </span>
        }
      >
        <DataTable<ExportItem>
          loading={exportsApi.loading}
          error={exportsApi.error ? "导出列表加载失败，请重试。" : null}
          onRetry={() => void reloadExports()}
          rows={rows}
          rowKey={(row) => row.id}
          empty={<EmptyState title="没有符合筛选条件的导出" />}
          columns={[
            {
              key: "export",
              header: "导出 / 任务",
              width: "180px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2, minWidth: 0 }}>
                  <span className="mono" title={row.id}>
                    {shortValue(row.id, 10, 6)}
                  </span>
                  <span className="mono" style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>
                    {row.job_id ? shortValue(row.job_id, 10, 6) : "无关联任务"}
                  </span>
                </div>
              ),
            },
            {
              key: "format",
              header: "格式",
              width: "100px",
              render: (row) => (
                <span className="mono">
                  {row.format}
                  {row.zip ? " · zip" : ""}
                </span>
              ),
            },
            {
              key: "status",
              header: "状态",
              width: "150px",
              render: (row) => {
                const expired = row.expires_at ? Date.parse(row.expires_at) <= Date.now() : false;
                return (
                  <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
                    <StatusBadge tone={statusToneFor(row.status)}>{row.status}</StatusBadge>
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
              key: "rows",
              header: "行数",
              width: "110px",
              render: (row) => <span className="mono">{formatRawAmount(String(row.rows), 0)}</span>,
            },
            {
              key: "sha",
              header: "SHA-256",
              width: "190px",
              render: (row) =>
                row.sha256 ? (
                  <span className="mono" title={row.sha256}>
                    {shortValue(row.sha256, 14, 10)}
                  </span>
                ) : (
                  "—"
                ),
            },
            {
              key: "size",
              header: "大小",
              width: "90px",
              render: () => <span className="mono">{formatBytes(undefined)}</span>,
            },
            {
              key: "time",
              header: "创建 / 完成",
              width: "170px",
              render: (row) => (
                <div style={{ display: "flex", flexDirection: "column", gap: 2 }}>
                  <span className="mono" style={{ fontSize: "var(--fs-11)" }}>{formatTime(row.created_at)}</span>
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-11)" }}>{formatTime(row.finished_at)}</span>
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
                <button type="button" className="btn btn--sm" disabled={!row.download_available || busy} onClick={() => void downloadItem(row)}>
                  下载票据
                </button>
              ),
            },
          ]}
        />
      </Card>
    </>
  );
}
