import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useParams } from "react-router-dom";
import { ApiError, newIdempotencyKey, useAuth } from "@chain/api-client";
import {
  BackLink,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  dialogInputValue,
  useDialog,
} from "@chain/ui";

/**
 * A-JOB-02 / S5-ADMIN-01：任务诊断详情（路由 /jobs/:jobId）。
 *
 * GET /api/admin/v1/jobs/{id}/diagnostics 返回固定诊断链 fixed_chain；
 * 本页严格按 fixed_chain 的顺序渲染各层，不按后端数组返回顺序重排。
 * control 命令必须通过 Dialog 填写原因后提交：
 * POST /api/admin/v1/jobs/{id}/commands/{pause|resume|cancel|retry} {reason}
 * ProviderUsage.error 只展示后端已脱敏（maskProviderError）的值。
 */

type Row = Record<string, unknown>;

interface LayerRecord extends Row {
  layer?: string;
  items?: unknown[];
}

interface DiagnosticsResponse {
  job_id?: string;
  chain?: LayerRecord[];
  fixed_chain?: string[];
  as_of?: string;
}

type Command = "pause" | "resume" | "cancel" | "retry";

interface Column {
  key: string;
  header: ReactNode;
  width?: string;
  align?: "left" | "right";
  render: (row: Row) => ReactNode;
}

const CANONICAL_CHAIN = [
  "DownloadJob",
  "SubjectJob",
  "DatasetJob",
  "ShardCheckpoint",
  "ProviderUsage",
  "RawObject",
  "ClickHouseCoverage",
];

const TERMINAL_STATUS = ["COMPLETED", "CANCELLED", "FAILED", "PARTIAL_FAILED", "ABORTED_BY_USER"];

const COMMANDS: Record<
  Command,
  {
    label: string;
    permission: string;
    confirmTitle: string;
    body: string;
    actionLabel: string;
    variant: "primary" | "danger";
    doneTitle: string;
  }
> = {
  pause: {
    label: "暂停",
    permission: "admin:job:control",
    confirmTitle: "暂停任务？",
    body: "暂停后不再领取新分片，运行中的分片在当前页面完成后停止。",
    actionLabel: "暂停任务",
    variant: "primary",
    doneTitle: "任务已请求暂停",
  },
  resume: {
    label: "恢复",
    permission: "admin:job:control",
    confirmTitle: "恢复任务？",
    body: "任务将重新领取未完成分片。",
    actionLabel: "恢复任务",
    variant: "primary",
    doneTitle: "任务已请求恢复",
  },
  cancel: {
    label: "取消",
    permission: "admin:job:control",
    confirmTitle: "取消任务？",
    body: "取消会终止未完成分片；已完成分片与成功游标保留。",
    actionLabel: "取消任务",
    variant: "danger",
    doneTitle: "任务已请求取消",
  },
  retry: {
    label: "重试失败分片",
    permission: "admin:job:retry",
    confirmTitle: "重试失败分片？",
    body: "只重置失败分片，已完成分片与成功游标保留。",
    actionLabel: "重试",
    variant: "primary",
    doneTitle: "失败分片已重试",
  },
};

function isRecord(value: unknown): value is Row {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function asText(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") return Number.isFinite(value) ? value.toLocaleString("zh-CN") : String(value);
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function asNumber(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "" && !Number.isNaN(Number(value))) return Number(value);
  return null;
}

function pick(row: Row, ...keys: string[]): unknown {
  for (const key of keys) {
    if (row[key] !== undefined) return row[key];
  }
  return undefined;
}

function nested(value: unknown, key: string): unknown {
  return isRecord(value) ? value[key] : undefined;
}

function shortValue(value: string | null | undefined, head = 10, tail = 6): string {
  if (!value) return "—";
  if (value.length <= head + tail + 3) return value;
  return `${value.slice(0, head)}…${value.slice(-tail)}`;
}

function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let size = value / 1024;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}`;
}

function formatTime(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  const date = new Date(String(value));
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString("zh-CN", { hour12: false });
}

function cellStatus(value: unknown): ReactNode {
  const text = asText(value);
  if (text === "—") return text;
  return <StatusBadge tone={statusToneFor(text)}>{text}</StatusBadge>;
}

function cellTime(value: unknown): ReactNode {
  return <span>{formatTime(value)}</span>;
}

function cellNumber(value: unknown): ReactNode {
  const number = asNumber(value);
  return <span className="num">{number === null ? "—" : number.toLocaleString("zh-CN")}</span>;
}

function cellBytes(value: unknown): ReactNode {
  const number = asNumber(value);
  return <span className="mono">{number === null ? "—" : formatBytes(number)}</span>;
}

function cellProgress(value: unknown): ReactNode {
  const number = asNumber(value);
  if (number === null) return "—";
  const ratio = number > 1 ? Math.min(1, number / 100) : Math.min(1, Math.max(0, number));
  return <span className="num">{`${Math.round(ratio * 100)}%`}</span>;
}

function cellTrace(value: unknown): ReactNode {
  const text = asText(value);
  if (text === "—") return text;
  return (
    <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={text}>
      {shortValue(text, 10, 6)}
    </span>
  );
}

function cellBoolean(value: unknown): ReactNode {
  if (value === null || value === undefined) return "—";
  return value ? <StatusBadge tone="ok">true</StatusBadge> : <StatusBadge tone="neutral">false</StatusBadge>;
}

function coverageRows(value: unknown): ReactNode {
  const number = asNumber(value);
  if (number === null || number < 0) return "—";
  return <span className="num">{number.toLocaleString("zh-CN")}</span>;
}

function commonColumns(layerName: string): Column[] {
  const errorHeader = layerName === "ProviderUsage" ? "Error（后端已脱敏）" : "Error";
  return [
    { key: "id", header: "ID", width: "230px", render: (row) => cellTrace(pick(row, "id", "shard_id")) },
    { key: "status", header: "状态", width: "150px", render: (row) => cellStatus(row.status) },
    {
      key: "updated",
      header: "更新时间",
      width: "170px",
      render: (row) => cellTime(pick(row, "updated_at", "request_time", "coverage_updated_at", "created_at")),
    },
    {
      key: "rows",
      header: "行数",
      width: "100px",
      align: "right",
      render: (row) => {
        const value = pick(row, "downloaded_rows", "rows", "clickhouse_rows");
        return layerName === "ClickHouseCoverage" ? coverageRows(value) : cellNumber(value);
      },
    },
    {
      key: "bytes",
      header: "Bytes",
      width: "100px",
      align: "right",
      render: (row) => cellBytes(pick(row, "downloaded_bytes", "bytes", "size_bytes")),
    },
    {
      key: "error",
      header: errorHeader,
      width: "220px",
      render: (row) => {
        const failureMessage = nested(row.failure, "message_safe");
        const failureKind = nested(row.failure, "kind");
        const value = row.error ?? failureMessage ?? failureKind;
        return (
          <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={asText(value)}>
            {asText(value)}
          </span>
        );
      },
    },
    {
      key: "trace",
      header: "Trace",
      width: "160px",
      render: (row) => cellTrace(pick(row, "trace_id", "request_id")),
    },
  ];
}

function columnsFor(layerName: string): Column[] {
  const common = commonColumns(layerName);
  switch (layerName) {
    case "DownloadJob":
      return [
        ...common,
        { key: "progress", header: "进度", width: "90px", align: "right", render: (row) => cellProgress(row.progress) },
      ];
    case "SubjectJob":
      return [
        ...common,
        { key: "subject", header: "Subject", width: "240px", render: (row) => cellTrace(row.subject) },
        { key: "progress", header: "进度", width: "90px", align: "right", render: (row) => cellProgress(row.progress) },
        {
          key: "advance",
          header: "链路推进时间",
          width: "260px",
          render: (row) => (
            <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
              {asText(nested(row.advance_to_chain_time, "tron_last_timestamp_ms"))} /{" "}
              {formatTime(nested(row.advance_to_chain_time, "checkpoint_at"))}
            </span>
          ),
        },
      ];
    case "DatasetJob":
      return [
        ...common,
        { key: "subject", header: "Subject ID", width: "210px", render: (row) => cellTrace(row.subject_id) },
        { key: "dataset", header: "Dataset", width: "150px", render: (row) => asText(row.dataset) },
        { key: "progress", header: "进度", width: "90px", align: "right", render: (row) => cellProgress(row.progress) },
      ];
    case "ShardCheckpoint":
      return [
        ...common,
        { key: "dataset", header: "Dataset", width: "150px", render: (row) => asText(row.dataset) },
        { key: "retries", header: "重试", width: "80px", align: "right", render: (row) => cellNumber(row.retries) },
        {
          key: "checkpoint",
          header: "Checkpoint",
          width: "260px",
          render: (row) => (
            <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
              page {asText(nested(row.checkpoint, "page_no"))} · next{" "}
              {shortValue(asText(nested(row.checkpoint, "next_page_token")), 8, 4)} ·{" "}
              {formatTime(nested(row.checkpoint, "last_success_at"))}
            </span>
          ),
        },
      ];
    case "ProviderUsage":
      return [
        ...common,
        { key: "provider", header: "Provider", width: "110px", render: (row) => asText(row.provider) },
        { key: "method", header: "Method", width: "130px", render: (row) => asText(row.method) },
        { key: "dataset", header: "Dataset", width: "140px", render: (row) => asText(row.dataset) },
        { key: "duration", header: "ms", width: "80px", align: "right", render: (row) => cellNumber(row.duration_ms) },
        { key: "http", header: "HTTP", width: "80px", align: "right", render: (row) => cellNumber(row.http_status) },
        { key: "credits", header: "Credits", width: "90px", align: "right", render: (row) => cellNumber(row.credits) },
        { key: "cost", header: "USD", width: "100px", align: "right", render: (row) => asText(row.estimated_cost_usd) },
        { key: "requestid", header: "Request", width: "150px", render: (row) => cellTrace(row.request_id) },
        { key: "retry", header: "Retry", width: "80px", align: "right", render: (row) => cellNumber(row.retry_attempt) },
        { key: "cache", header: "Cache", width: "90px", render: (row) => cellBoolean(row.cache_hit) },
        { key: "singleflight", header: "Singleflight", width: "110px", render: (row) => cellBoolean(row.singleflight_hit) },
      ];
    case "RawObject":
      return [
        ...common,
        { key: "object", header: "Object Ref", width: "260px", render: (row) => cellTrace(row.object_ref) },
        {
          key: "exists",
          header: "Exists",
          width: "100px",
          render: (row) =>
            row.exists === null || row.exists === undefined ? (
              "—"
            ) : row.exists ? (
              <StatusBadge tone="ok">存在</StatusBadge>
            ) : (
              <StatusBadge tone="danger">不存在</StatusBadge>
            ),
        },
        {
          key: "sha",
          header: "SHA-256",
          width: "220px",
          render: (row) => {
            const digest = asText(row.sha256);
            if (digest !== "—") {
              return (
                <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={digest}>
                  {shortValue(digest, 12, 8)}
                </span>
              );
            }
            return (
              <span className="mono" style={{ fontSize: "var(--fs-11)" }} title={asText(row.sha256_note)}>
                {asText(row.sha256_note)}
              </span>
            );
          },
        },
      ];
    case "ClickHouseCoverage":
      return [
        ...common,
        { key: "subject", header: "Subject", width: "260px", render: (row) => cellTrace(row.subject) },
        { key: "dataset", header: "Dataset", width: "150px", render: (row) => asText(row.dataset) },
        {
          key: "coverage",
          header: "Coverage 区间",
          width: "230px",
          render: (row) => (
            <span className="mono" style={{ fontSize: "var(--fs-11)" }}>
              {asText(row.coverage_from_block)} – {asText(row.coverage_to_block)}
            </span>
          ),
        },
        {
          key: "consistent",
          header: "一致性",
          width: "100px",
          render: (row) => (row.coverage_consistent === undefined ? "—" : cellBoolean(row.coverage_consistent)),
        },
      ];
    default:
      return common;
  }
}

export default function JobDetailPage() {
  const { jobId = "" } = useParams();
  const dialog = useDialog();
  const { client, can } = useAuth();

  const [data, setData] = useState<DiagnosticsResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [commandBusy, setCommandBusy] = useState(false);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      if (!jobId) {
        setData(null);
        setLoading(false);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await client.get<DiagnosticsResponse>(
          `/api/admin/v1/jobs/${encodeURIComponent(jobId)}/diagnostics`,
          { signal },
        );
        if (signal?.aborted) return;
        setData(response.data);
      } catch (loadError) {
        if (signal?.aborted) return;
        if (loadError instanceof DOMException && loadError.name === "AbortError") return;
        setError(loadError instanceof ApiError ? loadError.userMessage : "读取任务诊断失败");
      } finally {
        if (!signal?.aborted) setLoading(false);
      }
    },
    [client, jobId],
  );

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const fixedChain = useMemo(
    () => (data?.fixed_chain && data.fixed_chain.length > 0 ? data.fixed_chain : CANONICAL_CHAIN),
    [data],
  );

  const layerMap = useMemo(() => {
    const map = new Map<string, LayerRecord>();
    for (const entry of data?.chain ?? []) {
      if (!isRecord(entry)) continue;
      const name = typeof entry.layer === "string" ? entry.layer : "";
      if (name) map.set(name, entry);
    }
    return map;
  }, [data]);

  const downloadLayer = layerMap.get("DownloadJob");
  const jobStatus = downloadLayer ? asText(downloadLayer.status) : "—";

  const runCommand = async (command: Command) => {
    const meta = COMMANDS[command];
    if (!jobId) return;
    if (!can(meta.permission)) {
      dialog.notify("warning", "缺少控制权限", `该命令需要 ${meta.permission}。`);
      return;
    }
    const confirmed = await dialog.confirm({
      kind: command === "cancel" ? "warning" : "confirm",
      title: meta.confirmTitle,
      body: meta.body,
      details: [
        { label: "任务 ID", value: <span className="mono">{jobId}</span> },
        { label: "当前状态", value: jobStatus },
        { label: "所需权限", value: meta.permission },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: meta.actionLabel, result: "ok", variant: meta.variant },
      ],
    });
    if (confirmed !== "ok") return;
    const reason = dialogInputValue(
      await dialog.prompt({
        kind: "warning",
        title: `提交${meta.label}命令`,
        body: "命令原因会写入审计日志，无法为空。",
        input: {
          label: "原因",
          placeholder: "例如：上游返回异常，暂停后核查",
          pattern: /^.{1,500}$/,
          patternHint: "1-500 个字符",
        },
        submitLabel: "提交命令",
      }),
    );
    if (!reason) return;
    setCommandBusy(true);
    try {
      const response = await client.post<{ job_id?: string; status?: string; command?: string; reset_shards?: number }>(
        `/api/admin/v1/jobs/${encodeURIComponent(jobId)}/commands/${command}`,
        { reason },
        { idempotencyKey: newIdempotencyKey() },
      );
      dialog.notify("success", meta.doneTitle, null, [
        { label: "任务 ID", value: <span className="mono">{response.data?.job_id ?? jobId}</span> },
        { label: "新状态", value: response.data?.status ?? "—" },
        ...(typeof response.data?.reset_shards === "number"
          ? [{ label: "重置分片", value: String(response.data.reset_shards) }]
          : []),
        { label: "原因", value: reason },
      ]);
      await load();
    } catch (commandError) {
      if (commandError instanceof ApiError) {
        dialog.notify("error", "命令提交失败", commandError.userMessage, [
          { label: "HTTP", value: String(commandError.status) },
          ...(commandError.traceId ? [{ label: "Trace", value: commandError.traceId }] : []),
        ]);
      } else {
        dialog.notify("error", "命令提交失败", commandError instanceof Error ? commandError.message : "请稍后重试");
      }
    } finally {
      setCommandBusy(false);
    }
  };

  const explainFixedChain = () => {
    dialog.notify(
      "info",
      "固定诊断链顺序",
      "页面按服务端 fixed_chain 渲染；缺失层也会保留位置以便定位断点。",
      fixedChain.map((layerName, index) => ({ label: `第 ${index + 1} 层`, value: layerName })),
    );
  };

  const canPause = can("admin:job:control") && ["RUNNING", "QUEUED", "PAUSE_REQUESTED"].includes(jobStatus);
  const canResume =
    can("admin:job:control") && ["PAUSED", "PAUSED_FALLBACK_TOO_EXPENSIVE"].includes(jobStatus);
  const canCancel = can("admin:job:control") && jobStatus !== "—" && !TERMINAL_STATUS.includes(jobStatus);
  const canRetry = can("admin:job:retry");

  if (!jobId) {
    return (
      <>
        <BackLink to="/jobs" label="返回全局任务" />
        <Card>
          <EmptyState title="缺少任务 ID" />
        </Card>
      </>
    );
  }

  if (loading && !data) {
    return (
      <>
        <BackLink to="/jobs" label="返回全局任务" />
        <SkeletonRows rows={7} columns={5} />
      </>
    );
  }

  if (error && !data) {
    return (
      <>
        <BackLink to="/jobs" label="返回全局任务" />
        <Card>
          <ErrorState title="任务诊断加载失败" message={error} onRetry={() => void load()} />
        </Card>
      </>
    );
  }

  return (
    <>
      <BackLink to="/jobs" label="返回全局任务" />
      <PageHeader
        title={`任务诊断 · ${jobStatus}`}
        subtitle={`job_id ${jobId} · as_of ${formatTime(data?.as_of)} · 来源 /api/admin/v1/jobs/${jobId}/diagnostics`}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={explainFixedChain}>
              固定链口径
            </button>
            <button type="button" className="btn btn--sm" onClick={() => void load()} disabled={loading}>
              刷新
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => void runCommand("pause")}
              disabled={!canPause || commandBusy}
            >
              暂停
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => void runCommand("resume")}
              disabled={!canResume || commandBusy}
            >
              恢复
            </button>
            <button
              type="button"
              className="btn btn--danger btn--sm"
              onClick={() => void runCommand("cancel")}
              disabled={!canCancel || commandBusy}
            >
              取消
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => void runCommand("retry")}
              disabled={!canRetry || commandBusy}
            >
              重试失败分片
            </button>
          </>
        }
      />

      <Card title="任务上下文">
        <div className="toolbar">
          <StatusBadge tone={statusToneFor(jobStatus)}>{jobStatus}</StatusBadge>
          <span className="mono" style={{ fontSize: "var(--fs-12)" }}>
            job_id {jobId}
          </span>
          <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>
            固定链 {fixedChain.length} 层 · 数据截止 {formatTime(data?.as_of)}
          </span>
        </div>
      </Card>

      {fixedChain.map((layerName, index) => {
        const layer = layerMap.get(layerName);
        const items: Row[] =
          layerName === "DownloadJob"
            ? layer
              ? [layer]
              : []
            : Array.isArray(layer?.items)
              ? layer.items.filter(isRecord)
              : [];
        return (
          <Card
            key={layerName}
            title={
              <span>
                {index + 1}. {layerName}
                <span style={{ color: "var(--c-slate)", fontWeight: 400 }}> · {items.length} 条</span>
              </span>
            }
            flush
            actions={
              layerName === "ProviderUsage" ? (
                <button
                  type="button"
                  className="btn btn--sm"
                  onClick={() =>
                    dialog.notify(
                      "info",
                      "ProviderUsage 错误脱敏口径",
                      "error 字段只展示后端 maskProviderError 处理后的值；前端不拼接、不还原 Provider 原始错误与凭证。",
                    )
                  }
                >
                  脱敏口径
                </button>
              ) : null
            }
          >
            {layer ? (
              <DataTable<Row>
                rows={items}
                rowKey={(row, rowIndex) => `${layerName}-${String(row.id ?? rowIndex)}`}
                columns={columnsFor(layerName)}
                empty={<EmptyState title="该层暂无记录" />}
              />
            ) : (
              <EmptyState title="固定诊断链缺少该层" />
            )}
          </Card>
        );
      })}
    </>
  );
}
