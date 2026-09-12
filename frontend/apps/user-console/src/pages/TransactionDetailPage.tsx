import { useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { ApiError, useAuth } from "@chain/api-client";
import {
  BackLink,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";
import type { StatusTone } from "@chain/ui";

/**
 * U-EXP-03 交易详情。
 * - 概览 / Logs / Trace 三个 Tab，Tab 写入 URL。
 * - Trace 首次只请求 local_only；缺失时展示“本地无 Trace”，用户确认成本后才发 auto。
 * - 概览默认 local_only 解码，不产生 Provider 请求；错误/确认/说明全部走 Dialog。
 */

interface MethodInfo {
  id?: string;
  name?: string;
  signature?: string;
  source?: string;
  verified?: boolean;
  confidence?: string;
  method_type?: string;
  category?: string;
  risk?: boolean;
}

interface DecodedParam {
  name?: string;
  type?: string;
  value?: unknown;
}

interface LogRow {
  log_index?: number;
  address?: string;
  topics?: string[];
  data?: string;
  event_name?: string;
  event_signature?: string;
  event_source?: string;
  event_verified?: number | boolean;
  block_number?: number;
  timestamp?: string;
  source_provider?: string;
  removed?: number;
}

interface TxRecord {
  chain?: string;
  block_number?: number;
  block_hash?: string;
  tx_hash?: string;
  timestamp?: string;
  from_address?: string;
  to_address?: string;
  value_raw?: string;
  nonce?: number;
  gas?: number;
  gas_used?: number;
  gas_price?: string;
  tx_type?: string;
  status?: number | string;
  raw_status?: string;
  input?: string;
  source_provider?: string;
  method_id?: string;
  method_name?: string;
  method_signature?: string;
  method_source?: string;
  method_verified?: number;
}

interface TxDetail {
  chain?: string;
  tx_hash?: string;
  transaction?: TxRecord;
  input?: string;
  method?: MethodInfo;
  decoded_input?: Record<string, unknown>;
  decoded_params?: DecodedParam[];
  decode_source?: string;
  decode_verified?: boolean;
  decode_cache_hit?: boolean;
  decoded_at?: string;
  logs?: LogRow[];
  decoded_logs?: unknown[];
  decode_error?: string;
}

interface TraceFrame {
  type?: string;
  from?: string;
  to?: string | null;
  value?: string;
  input?: string;
  output?: string;
  gas?: number;
  gas_used?: number;
  error?: string;
  children?: TraceFrame[];
}

interface TraceResult {
  chain?: string;
  tx_hash?: string;
  root?: TraceFrame | null;
  source?: string;
  frame_count?: number;
  max_depth?: number;
  fetched_at?: string;
  cache_hit?: boolean;
  raw_object_key?: string;
  raw_bytes?: number;
}

const TABS = [
  { key: "overview", label: "概览" },
  { key: "logs", label: "Logs" },
  { key: "trace", label: "Trace" },
];

function nativeDecimals(chain: string): number {
  return chain === "tron" ? 6 : 18;
}

function jsonText(value: unknown, max = 160): string {
  try {
    const text = JSON.stringify(value);
    if (!text) return "—";
    return text.length > max ? `${text.slice(0, max)}…` : text;
  } catch {
    return "—";
  }
}

function statusLabel(record?: TxRecord): string {
  if (!record) return "—";
  if (record.raw_status) return record.raw_status;
  if (record.status === undefined || record.status === null) return "—";
  return String(record.status);
}

function statusToneOf(record?: TxRecord): StatusTone {
  if (!record) return "neutral";
  if (record.raw_status) return statusToneFor(record.raw_status);
  if (record.status === 1 || record.status === "1") return "ok";
  if (record.status === 0 || record.status === "0") return "danger";
  return "neutral";
}

function TraceFrameView({ frame, chain, depth }: { frame: TraceFrame; chain: string; depth: number }) {
  const [open, setOpen] = useState(depth < 2);
  const children = frame.children ?? [];
  const decimals = nativeDecimals(chain);
  return (
    <div
      style={{
        borderLeft: depth > 0 ? "2px solid var(--c-border)" : undefined,
        paddingLeft: depth > 0 ? "var(--sp-3)" : 0,
        display: "flex",
        flexDirection: "column",
        gap: "var(--sp-2)",
        minWidth: 0,
      }}
    >
      <div className="toolbar" style={{ alignItems: "center", rowGap: "var(--sp-1)" }}>
        {children.length > 0 ? (
          <button
            type="button"
            className="btn btn--sm btn--ghost"
            aria-expanded={open}
            onClick={() => setOpen((value) => !value)}
          >
            {open ? "收起" : "展开"}
          </button>
        ) : null}
        <span className="badge badge--neutral">{frame.type || "CALL"}</span>
        <span
          className="mono"
          style={{ overflowWrap: "anywhere" }}
          title={`${frame.from ?? "—"} → ${frame.to ?? "—"}`}
        >
          {shortValue(frame.from, 10, 6)} → {frame.to ? shortValue(frame.to, 10, 6) : "—"}
        </span>
        <span className="amount" title={`raw ${frame.value ?? "0"}`}>
          {formatRawAmount(frame.value ?? "0", decimals)}
        </span>
        <StatusBadge tone={frame.error ? "danger" : "ok"}>{frame.error ? "失败" : "成功"}</StatusBadge>
      </div>
      <div className="s4-list__sub">
        gas {frame.gas ?? "—"} · used {frame.gas_used ?? "—"}
        {frame.error ? ` · ${frame.error}` : ""}
      </div>
      {open
        ? children.map((child, index) => (
            <TraceFrameView key={index} frame={child} chain={chain} depth={depth + 1} />
          ))
        : null}
    </div>
  );
}

function TraceResultView({ trace, chain }: { trace: TraceResult; chain: string }) {
  return (
    <>
      <Card title="Trace 概要">
        <div className="metric-grid">
          <Metric label="来源" value={trace.source ?? "—"} />
          <Metric label="帧数" value={trace.frame_count ?? "—"} />
          <Metric label="最大深度" value={trace.max_depth ?? "—"} />
          <Metric label="缓存命中" value={trace.cache_hit ? "是" : "否"} />
          <Metric label="原始字节" value={formatBytes(trace.raw_bytes)} />
          <Metric label="获取时间" value={formatTime(trace.fetched_at)} />
        </div>
      </Card>
      <Card flush title="调用树">
        {trace.root ? (
          <div style={{ padding: "var(--sp-4)", display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}>
            <TraceFrameView frame={trace.root} chain={chain} depth={0} />
          </div>
        ) : (
          <EmptyState title="Trace 结果没有调用帧" />
        )}
      </Card>
    </>
  );
}

function LogsTable({ rows }: { rows: LogRow[] }) {
  return (
    <DataTable<LogRow>
      rows={rows}
      rowKey={(row, index) => `${row.log_index ?? index}-${row.address ?? ""}`}
      empty={<EmptyState title="没有事件日志" />}
      columns={[
        {
          key: "index",
          header: "log_index",
          width: "90px",
          render: (row) => <span className="mono">{row.log_index ?? "—"}</span>,
        },
        {
          key: "event",
          header: "事件",
          width: "190px",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div>{row.event_name || "—"}</div>
              <div className="s4-list__sub mono" style={{ overflowWrap: "anywhere" }} title={row.event_signature}>
                {row.event_signature || "—"}
              </div>
            </div>
          ),
        },
        {
          key: "address",
          header: "合约地址",
          width: "150px",
          render: (row) => (
            <span className="mono" title={row.address}>
              {row.address ? shortValue(row.address, 10, 8) : "—"}
            </span>
          ),
        },
        {
          key: "topics",
          header: "topics",
          width: "180px",
          render: (row) => {
            const topics = row.topics ?? [];
            return (
              <span className="mono" style={{ overflowWrap: "anywhere" }} title={topics.join("\n")}>
                {topics.length > 0 ? `${topics.length} 项 · ${shortValue(topics[0], 10, 6)}` : "—"}
              </span>
            );
          },
        },
        {
          key: "data",
          header: "data",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={row.data}>
              {row.data ? shortValue(row.data, 18, 8) : "—"}
            </span>
          ),
        },
        {
          key: "block",
          header: "区块",
          width: "110px",
          render: (row) => <span className="mono">{row.block_number ?? "—"}</span>,
        },
        {
          key: "time",
          header: "时间",
          width: "160px",
          render: (row) => <span>{formatTime(row.timestamp)}</span>,
        },
        {
          key: "source",
          header: "来源",
          width: "140px",
          render: (row) => <span>{row.source_provider || row.event_source || "—"}</span>,
        },
        {
          key: "verified",
          header: "verified",
          width: "100px",
          render: (row) => (
            <StatusBadge tone={row.event_verified ? "ok" : "neutral"}>
              {row.event_verified ? "verified" : "unverified"}
            </StatusBadge>
          ),
        },
      ]}
    />
  );
}

export default function TransactionDetailPage() {
  const { chain = "", hash = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const dialog = useDialog();
  const { can } = useAuth();
  const action = useS5Action();
  const requestedTab = searchParams.get("tab") ?? "overview";
  const tab = TABS.some((item) => item.key === requestedTab) ? requestedTab : "overview";
  const ready = Boolean(chain && hash);
  const base = ready
    ? `/api/v2/explorer/transactions/${encodeURIComponent(chain)}/${encodeURIComponent(hash)}`
    : null;

  const detail = useApi<{ items: TxDetail | null }>(base, [chain, hash]);
  const logs = useApi<{ items: LogRow[] }>(
    tab === "logs" && base ? `${base}/logs` : null,
    [chain, hash, tab],
  );
  const localTrace = useApi<{ items: TraceResult | null }>(
    tab === "trace" && base ? `${base}/trace` : null,
    [chain, hash, tab],
    { mode: "local_only" },
  );
  const [providerTrace, setProviderTrace] = useState<TraceResult | null>(null);
  const notifiedErrorsRef = useRef<Set<string>>(new Set());
  const lastDecodeErrorRef = useRef("");

  useEffect(() => {
    setProviderTrace(null);
  }, [chain, hash]);

  const loadErrors = [detail.error, logs.error].filter((message): message is string => Boolean(message));
  const loadErrorsKey = loadErrors.join("|");
  useEffect(() => {
    if (loadErrors.length === 0) {
      notifiedErrorsRef.current.clear();
      return;
    }
    for (const message of loadErrors) {
      if (notifiedErrorsRef.current.has(message)) continue;
      notifiedErrorsRef.current.add(message);
      dialog.notify("error", "加载失败", message);
    }
    // loadErrorsKey 仅用于在错误集合变化时触发本 effect。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loadErrorsKey, dialog]);

  const decodeError = detail.data?.items?.decode_error ?? "";
  useEffect(() => {
    if (!decodeError) {
      lastDecodeErrorRef.current = "";
      return;
    }
    if (lastDecodeErrorRef.current === decodeError) return;
    lastDecodeErrorRef.current = decodeError;
    dialog.notify("warning", "解码未完成", decodeError);
  }, [decodeError, dialog]);

  const tx = detail.data?.items ?? null;
  const trace = providerTrace ?? localTrace.data?.items ?? null;

  const updateParams = (updates: Record<string, string | null>) => {
    const params = new URLSearchParams(searchParams);
    for (const [key, value] of Object.entries(updates)) {
      if (value === null || value === "") params.delete(key);
      else params.set(key, value);
    }
    setSearchParams(params);
  };

  const copy = async (value: string, label: string) => {
    if (!value) {
      dialog.notify("warning", "没有可复制的内容");
      return;
    }
    try {
      await navigator.clipboard.writeText(value);
      dialog.notify("success", "已复制", `${label} 已写入剪贴板。`);
    } catch {
      dialog.notify("warning", "复制失败", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
    }
  };

  const fetchProviderTrace = async () => {
    if (!base) return;
    if (!can("explorer:fetch")) {
      dialog.notify(
        "warning",
        "缺少 explorer:fetch 权限",
        "当前账号不能触发 Provider Trace 补取。请联系管理员授予 explorer:fetch 后重试。",
      );
      return;
    }
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "从 Provider 补取 Trace？",
      body: "本地没有这笔交易的 Trace。继续将新增一次 Provider 请求，可能产生费用并占用供应商配额；只有确认后才会发出请求。",
      details: [
        { label: "链", value: chain },
        { label: "交易", value: shortValue(hash, 14, 10) },
        { label: "模式", value: "auto" },
        { label: "所需权限", value: "explorer:fetch" },
      ],
    });
    if (answer !== "confirm") return;
    const payload = await action.run(async () => {
      try {
        const response = await action.client.get<{ items: TraceResult | null }>(`${base}/trace`, {
          query: { mode: "auto" },
        });
        return response.data;
      } catch (err) {
        if (err instanceof ApiError && err.status === 403) {
          dialog.notify(
            "warning",
            "缺少 explorer:fetch 权限",
            "当前账号不能触发 Provider Trace 补取。请联系管理员授予 explorer:fetch 后重试。",
          );
          return null;
        }
        throw err;
      }
    });
    if (payload?.items) {
      setProviderTrace(payload.items);
      dialog.notify("success", "Trace 已补取", `来源：${payload.items.source || "provider"}`);
    }
  };

  const renderOverview = () => {
    if (detail.loading && !detail.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    if (!tx) {
      return (
        <Card flush>
          <ErrorState title="交易数据不可用" message="加载失败，请重试。" onRetry={() => void detail.reload()} />
        </Card>
      );
    }
    const record = tx.transaction;
    const decimals = nativeDecimals(chain);
    return (
      <>
        <Card title="交易字段">
          <dl className="s4-kv">
            <div className="s4-kv__row">
              <dt>block_number</dt>
              <dd className="mono">{record?.block_number ?? "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>timestamp</dt>
              <dd>{formatTime(record?.timestamp)}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>from</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }} title={record?.from_address}>
                {record?.from_address || "—"}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>to</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }} title={record?.to_address}>
                {record?.to_address || "—"}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>value_raw</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }}>
                {formatRawAmount(record?.value_raw, decimals)}
                <div className="s4-list__sub">raw {record?.value_raw ?? "—"}</div>
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>gas</dt>
              <dd className="mono">
                {record?.gas ?? "—"}
                {record?.gas_used !== undefined ? ` / used ${record.gas_used}` : ""}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>gas_price</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }}>
                {record?.gas_price ?? "—"}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>nonce</dt>
              <dd className="mono">{record?.nonce ?? "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>tx_type</dt>
              <dd className="mono">{record?.tx_type ?? "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>status</dt>
              <dd>
                <StatusBadge tone={statusToneOf(record)}>{statusLabel(record)}</StatusBadge>
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>source_provider</dt>
              <dd className="mono">{record?.source_provider ?? "—"}</dd>
            </div>
          </dl>
        </Card>
        <Card title="Method">
          <dl className="s4-kv">
            <div className="s4-kv__row">
              <dt>name</dt>
              <dd className="mono">{tx.method?.name || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>signature</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }}>
                {tx.method?.signature || "—"}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>selector</dt>
              <dd className="mono">{tx.method?.id || record?.method_id || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>source</dt>
              <dd className="mono">{tx.method?.source || tx.decode_source || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>verified</dt>
              <dd>
                <StatusBadge tone={tx.method?.verified ? "ok" : "neutral"}>
                  {tx.method?.verified ? "verified" : "unverified"}
                </StatusBadge>
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>confidence</dt>
              <dd className="mono">{tx.method?.confidence || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>method_type</dt>
              <dd className="mono">{tx.method?.method_type || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>category</dt>
              <dd className="mono">{tx.method?.category || "—"}</dd>
            </div>
          </dl>
        </Card>
        <Card flush title="解码结果">
          {tx.decoded_params && tx.decoded_params.length > 0 ? (
            <DataTable<DecodedParam>
              rows={tx.decoded_params}
              rowKey={(row, index) => `${row.name ?? "param"}-${index}`}
              empty={<EmptyState title="没有本地 ABI 解码结果" />}
              columns={[
                {
                  key: "name",
                  header: "name",
                  width: "160px",
                  render: (row) => <span className="mono">{row.name || "—"}</span>,
                },
                {
                  key: "type",
                  header: "type",
                  width: "160px",
                  render: (row) => <span className="mono">{row.type || "—"}</span>,
                },
                {
                  key: "value",
                  header: "value",
                  render: (row) => (
                    <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.value)}>
                      {jsonText(row.value)}
                    </span>
                  ),
                },
              ]}
            />
          ) : tx.decoded_input ? (
            <div style={{ padding: "var(--sp-4)" }}>
              <pre
                className="mono"
                style={{
                  margin: 0,
                  whiteSpace: "pre-wrap",
                  wordBreak: "break-all",
                  maxHeight: 420,
                  overflow: "auto",
                  fontSize: "var(--fs-12)",
                }}
              >
                {jsonText(tx.decoded_input, 4000)}
              </pre>
            </div>
          ) : (
            <EmptyState title="没有本地 ABI 解码结果" />
          )}
        </Card>
      </>
    );
  };

  const renderLogs = () => {
    if (logs.loading && !logs.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={5} />
        </Card>
      );
    }
    if (!logs.data) {
      return (
        <Card flush>
          <ErrorState title="日志数据不可用" message="加载失败，请重试。" onRetry={() => void logs.reload()} />
        </Card>
      );
    }
    return (
      <Card flush title="Logs">
        <LogsTable rows={logs.data.items ?? []} />
      </Card>
    );
  };

  const renderTrace = () => {
    if (trace) return <TraceResultView trace={trace} chain={chain} />;
    if (localTrace.loading && !localTrace.data) {
      return (
        <Card flush>
          <SkeletonRows rows={5} columns={3} />
        </Card>
      );
    }
    const localMissing = !localTrace.error || localTrace.error.includes("本地没有 Trace");
    if (!localMissing && !localTrace.data) {
      return (
        <Card flush>
          <ErrorState title="Trace 加载失败" message="加载失败，请重试。" onRetry={() => void localTrace.reload()} />
        </Card>
      );
    }
    return (
      <Card flush>
        <EmptyState
          title="本地无 Trace"
          action={
            <button
              type="button"
              className="btn btn--primary btn--sm"
              disabled={action.busy}
              onClick={() => void fetchProviderTrace()}
            >
              {action.busy ? "请求中…" : "向 Provider 补取 Trace"}
            </button>
          }
        />
      </Card>
    );
  };

  const content = tab === "logs" ? renderLogs() : tab === "trace" ? renderTrace() : renderOverview();

  return (
    <>
      <BackLink to="/explorer" label="返回地址与交易查询" />
      <PageHeader
        title="交易详情"
        subtitle={chain ? `${chain} · ${shortValue(hash, 12, 8)}` : undefined}
        actions={
          <button
            type="button"
            className="btn btn--sm"
            onClick={() => void copy(tx?.tx_hash ?? hash, "交易哈希")}
          >
            复制哈希
          </button>
        }
      />
      <Card
        title="对象"
        actions={
          <StatusBadge tone={tx?.decode_verified ? "ok" : "neutral"}>
            {tx?.decode_verified ? "verified" : "unverified"}
          </StatusBadge>
        }
      >
        <dl className="s4-kv">
          <div className="s4-kv__row">
            <dt>chain</dt>
            <dd className="mono">{tx?.chain || chain || "—"}</dd>
          </div>
          <div className="s4-kv__row">
            <dt>tx_hash</dt>
            <dd className="mono" style={{ overflowWrap: "anywhere" }}>
              {tx?.tx_hash || hash || "—"}
            </dd>
          </div>
          <div className="s4-kv__row">
            <dt>方法</dt>
            <dd className="mono" style={{ overflowWrap: "anywhere" }}>
              {tx?.method?.signature || tx?.method?.name || "—"}
            </dd>
          </div>
          <div className="s4-kv__row">
            <dt>解码来源</dt>
            <dd className="mono">{tx?.decode_source || "—"}</dd>
          </div>
          <div className="s4-kv__row">
            <dt>状态</dt>
            <dd>
              <StatusBadge tone={statusToneOf(tx?.transaction)}>{statusLabel(tx?.transaction)}</StatusBadge>
            </dd>
          </div>
        </dl>
      </Card>
      <nav className="s4-tabs" aria-label="交易详情分区">
        {TABS.map((item) => (
          <a
            key={item.key}
            className={`s4-tab${item.key === tab ? " is-active" : ""}`}
            href={`?tab=${item.key}`}
            aria-current={item.key === tab ? "page" : undefined}
            onClick={(event) => {
              event.preventDefault();
              updateParams({ tab: item.key });
            }}
          >
            {item.label}
          </a>
        ))}
      </nav>
      {content}
    </>
  );
}
