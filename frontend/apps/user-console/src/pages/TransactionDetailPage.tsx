import { useEffect, useRef, useState } from "react";
import { useParams, useSearchParams } from "react-router-dom";
import { ChevronDown, ChevronRight } from "lucide-react";
import { ApiError, useAuth } from "@chain/api-client";
import {
  Amount,
  BackLink,
  Button,
  Card,
  CopyButton,
  DataTable,
  EmptyState,
  ErrorState,
  HashValue,
  IconButton,
  KeyValueList,
  LinearProgress,
  MetaLine,
  MetricCard,
  PageHeader,
  PageSection,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import type { StatusTone } from "@chain/ui";
import { formatBytes, formatRawAmount, formatTime, shortValue, useApi, useS5Action } from "../sprint5/common";

/**
 * U-EXP-03 交易详情。
 * - 概览 / Logs / Trace 三个 Tab，Tab 写入 URL（保持 role=link 的深链接语义）。
 * - Trace 首次只请求 local_only；缺失时展示"本地无 Trace"，用户确认成本后才发 auto。
 * - 概览默认 local_only 解码，不产生 Provider 请求；错误/确认/说明全部走 Dialog。
 *
 * 2026-09 表现层重构（蓝图 §12.16）：
 *   Header（完整交易哈希 / 链 / 状态 / 时间 / 复制）→ Overview（from/to/value/gas/block/nonce/method）
 *   → Decode（Method 元数据 + 本地 ABI 解码参数 + 原始 input 的安全复制）
 *   → Logs（事件表格，topics/data 提供完整值复制）→ Trace（概要 + 调用树 + 补取）。
 *   接口、权限判断（explorer:fetch）、确认弹窗与错误处理语义一律未改。
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
    return text.length > max ? text.slice(0, max) + "…" : text;
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

/** 深层失败路径必须先被看见：有失败子节点的分支默认展开（蓝图 §12.16）。 */
function hasFailedFrame(frames?: TraceFrame[]): boolean {
  return (frames ?? []).some((frame) => Boolean(frame.error) || hasFailedFrame(frame.children));
}

/**
 * 调用树（TreeTable 语义）：role=tree / treeitem + aria-level + aria-expanded，
 * 层级缩进复用设计系统的 .cs-hier__children。默认展开：浅层 + 任何包含失败的路径。
 */
function TraceFrameView({ frame, chain, depth, path }: { frame: TraceFrame; chain: string; depth: number; path: string }) {
  const children = frame.children ?? [];
  const canExpand = children.length > 0;
  const [expanded, setExpanded] = useState(depth < 2 || hasFailedFrame(children));
  const decimals = nativeDecimals(chain);
  const label = (frame.type || "CALL") + " " + shortValue(frame.from, 10, 6) + " → " + (frame.to ? shortValue(frame.to, 10, 6) : "—");
  return (
    <div className="cs-hier__node" role="treeitem" aria-expanded={canExpand ? expanded : undefined} aria-level={depth + 1}>
      <div className="toolbar" style={{ alignItems: "center", rowGap: "var(--sp-1)" }}>
        {canExpand ? (
          <IconButton
            className="cs-hier__toggle"
            label={(expanded ? "折叠 " : "展开 ") + label}
            active={expanded}
            icon={expanded ? <ChevronDown size={14} aria-hidden="true" /> : <ChevronRight size={14} aria-hidden="true" />}
            onClick={() => setExpanded((value) => !value)}
          />
        ) : (
          <span className="cs-iconbtn cs-hier__toggle" aria-hidden="true" />
        )}
        <span className="badge badge--neutral">{frame.type || "CALL"}</span>
        <span className="mono" style={{ overflowWrap: "anywhere" }} title={(frame.from ?? "—") + " → " + (frame.to ?? "—")}>
          {shortValue(frame.from, 10, 6)} → {frame.to ? shortValue(frame.to, 10, 6) : "—"}
        </span>
        {/* 金额保留服务端原始字符串精度：raw 值完整写在 title 与检查器里。 */}
        <span className="amount" title={"raw " + (frame.value ?? "0")}>
          {formatRawAmount(frame.value ?? "0", decimals)}
        </span>
        <StatusBadge tone={frame.error ? "danger" : "ok"}>{frame.error ? "失败" : "成功"}</StatusBadge>
      </div>
      <div className="s4-list__sub">
        {"gas " + (frame.gas ?? "—") + " · used " + (frame.gas_used ?? "—")}
        {frame.error ? " · " + frame.error : ""}
      </div>
      {canExpand && expanded ? (
        <div className="cs-hier__children" role="group">
          {children.map((child, index) => (
            <TraceFrameView key={path + "." + index} frame={child} chain={chain} depth={depth + 1} path={path + "." + index} />
          ))}
        </div>
      ) : null}
    </div>
  );
}

function TraceResultView({ trace, chain }: { trace: TraceResult; chain: string }) {
  return (
    <>
      <PageSection title="Trace 概要">
        <div className="metric-grid cs-stagger">
          <MetricCard label="来源" value={trace.source ?? "—"} />
          <MetricCard label="帧数" value={trace.frame_count ?? "—"} />
          <MetricCard label="最大深度" value={trace.max_depth ?? "—"} />
          <MetricCard label="缓存命中" value={trace.cache_hit ? "是" : "否"} />
          <MetricCard label="原始字节" value={formatBytes(trace.raw_bytes)} />
          <MetricCard label="获取时间" value={formatTime(trace.fetched_at)} />
        </div>
      </PageSection>
      <PageSection title="调用树" count={trace.frame_count !== undefined ? trace.frame_count + " 帧" : undefined}>
        <Card flush>
          {trace.root ? (
            <div role="tree" aria-label="调用树" style={{ padding: "var(--sp-4)", display: "flex", flexDirection: "column", gap: "var(--sp-3)", minWidth: 0 }}>
              {/* 换一笔交易/换一次补取后重建整棵树，让"失败路径默认展开"重新生效。 */}
              <TraceFrameView
                key={(trace.fetched_at ?? "") + "-" + (trace.source ?? "")}
                frame={trace.root}
                chain={chain}
                depth={0}
                path="0"
              />
            </div>
          ) : (
            <EmptyState title="Trace 结果没有调用帧" />
          )}
        </Card>
      </PageSection>
    </>
  );
}

/** topics / data 的完整值只做安全文本呈现 + 复制，绝不作为 HTML 注入。 */
function RawValueCell({ label, value, head = 18, tail = 8 }: { label: string; value?: string; head?: number; tail?: number }) {
  if (!value) return <span>—</span>;
  return (
    <span className="cs-hash">
      <span className="cs-hash__text" title={value}>
        {shortValue(value, head, tail)}
      </span>
      <CopyButton value={value} label={label} />
    </span>
  );
}

function LogsTable({ rows }: { rows: LogRow[] }) {
  return (
    <DataTable<LogRow>
      rows={rows}
      rowKey={(row, index) => (row.log_index ?? index) + "-" + (row.address ?? "")}
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
          width: "170px",
          render: (row) =>
            row.address ? <HashValue value={row.address} label="合约地址" head={10} tail={8} /> : <span>—</span>,
        },
        {
          key: "topics",
          header: "topics",
          width: "220px",
          render: (row) => {
            const topics = row.topics ?? [];
            if (topics.length === 0) return <span>—</span>;
            return (
              <span className="cs-hash">
                <span className="cs-hash__text" title={topics.join("\n")}>
                  {topics.length + " 项 · " + shortValue(topics[0], 10, 6)}
                </span>
                <CopyButton value={topics.join("\n")} label="topics" />
              </span>
            );
          },
        },
        {
          key: "data",
          header: "data",
          render: (row) => <RawValueCell label="log data" value={row.data} />,
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
    ? "/api/v2/explorer/transactions/" + encodeURIComponent(chain) + "/" + encodeURIComponent(hash)
    : null;

  const detail = useApi<{ items: TxDetail | null }>(base, [chain, hash]);
  const logs = useApi<{ items: LogRow[] }>(
    tab === "logs" && base ? base + "/logs" : null,
    [chain, hash, tab],
  );
  const localTrace = useApi<{ items: TraceResult | null }>(
    tab === "trace" && base ? base + "/trace" : null,
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
  const record = tx?.transaction;
  const rawInput = tx?.input ?? record?.input ?? "";

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
      dialog.notify("success", "已复制", label + " 已写入剪贴板。");
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
        const response = await action.client.get<{ items: TraceResult | null }>(base + "/trace", {
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
      dialog.notify("success", "Trace 已补取", "来源：" + (payload.items.source || "provider"));
    }
  };

  const renderOverview = () => {
    if (detail.loading && !detail.data) {
      return (
        <PageSection title="概览">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!tx) {
      return (
        <PageSection title="概览">
          <Card flush>
            <ErrorState title="交易数据不可用" message="加载失败，请重试。" onRetry={() => void detail.reload()} />
          </Card>
        </PageSection>
      );
    }
    const decimals = nativeDecimals(chain);
    // 服务端解码参数：空数组与"没有解码结果"是同一件事，统一由下面的空状态承担。
    const decodedParams = tx.decoded_params ?? [];
    return (
      <>
        <PageSection title="交易字段">
          <Card>
            <KeyValueList
              items={[
                { label: "block_number", value: <span className="mono">{record?.block_number ?? "—"}</span> },
                {
                  label: "block_hash",
                  value: record?.block_hash ? <HashValue value={record.block_hash} label="区块哈希" wrap /> : "—",
                  wide: true,
                },
                { label: "timestamp", value: formatTime(record?.timestamp) },
                { label: "nonce", value: <span className="mono">{record?.nonce ?? "—"}</span> },
                { label: "tx_type", value: <span className="mono">{record?.tx_type ?? "—"}</span> },
                { label: "status", value: <StatusBadge tone={statusToneOf(record)}>{statusLabel(record)}</StatusBadge> },
                { label: "source_provider", value: record?.source_provider ?? "—" },
                {
                  label: "from",
                  value: record?.from_address ? <HashValue value={record.from_address} label="from 地址" wrap /> : "—",
                  wide: true,
                },
                {
                  label: "to",
                  value: record?.to_address ? <HashValue value={record.to_address} label="to 地址" wrap /> : "—",
                  wide: true,
                },
                {
                  label: "value_raw",
                  value: (
                    <span>
                      {/* 转账金额直接用服务端 raw 字符串按 decimals 展示，不经过 JS number。 */}
                      <Amount rawAmount={record?.value_raw} decimals={decimals} />
                      <span className="s4-list__sub mono" style={{ display: "block" }}>
                        raw {record?.value_raw ?? "—"}
                      </span>
                    </span>
                  ),
                  wide: true,
                },
                { label: "gas", value: <span className="mono">{record?.gas ?? "—"}</span> },
                { label: "gas_used", value: <span className="mono">{record?.gas_used ?? "—"}</span> },
                { label: "gas_price", value: <span className="mono">{record?.gas_price ?? "—"}</span>, wide: true },
              ]}
            />
          </Card>
        </PageSection>
        <PageSection title="Method">
          <Card>
            <KeyValueList
              items={[
                { label: "name", value: <span className="mono">{tx.method?.name || "—"}</span> },
                {
                  label: "signature",
                  value: <span className="mono">{tx.method?.signature || "—"}</span>,
                  wide: true,
                },
                { label: "selector", value: <span className="mono">{tx.method?.id || record?.method_id || "—"}</span> },
                { label: "source", value: <span className="mono">{tx.method?.source || tx.decode_source || "—"}</span> },
                {
                  label: "verified",
                  value: (
                    <StatusBadge tone={tx.method?.verified ? "ok" : "neutral"}>
                      {tx.method?.verified ? "verified" : "unverified"}
                    </StatusBadge>
                  ),
                },
                { label: "confidence", value: <span className="mono">{tx.method?.confidence || "—"}</span> },
                { label: "method_type", value: <span className="mono">{tx.method?.method_type || "—"}</span> },
                { label: "category", value: <span className="mono">{tx.method?.category || "—"}</span> },
              ]}
            />
          </Card>
        </PageSection>
        <PageSection title="解码结果（本地 ABI）" count={decodedParams.length > 0 ? decodedParams.length + " 个参数" : undefined}>
          <Card flush>
            <div className="toolbar" style={{ padding: "var(--sp-3) var(--sp-4)", borderBottom: "1px solid var(--border-default)" }}>
              <StatusBadge tone={tx.decode_verified ? "ok" : "neutral"}>
                {tx.decode_verified ? "verified" : "unverified"}
              </StatusBadge>
              <span className="s4-list__sub">{"decode_source " + (tx.decode_source || "—")}</span>
              <span className="s4-list__sub">{"cache_hit " + (tx.decode_cache_hit ? "是" : "否")}</span>
              <span className="s4-list__sub">{"decoded_at " + formatTime(tx.decoded_at)}</span>
            </div>
            {decodedParams.length > 0 ? (
              <DataTable<DecodedParam>
                rows={decodedParams}
                rowKey={(row, index) => (row.name ?? "param") + "-" + index}
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
                      <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.value, 4000)}>
                        {jsonText(row.value)}
                      </span>
                    ),
                  },
                ]}
              />
            ) : tx.decoded_input ? (
              <div style={{ padding: "var(--sp-4)" }}>
                {/* 原始内容只作为文本呈现，绝不作为 HTML 注入。 */}
                <pre className="s5-json">{jsonText(tx.decoded_input, 4000)}</pre>
              </div>
            ) : (
              <EmptyState title="没有本地 ABI 解码结果" />
            )}
          </Card>
        </PageSection>
        <PageSection title="原始 input">
          <Card>
            {rawInput ? (
              <details>
                <summary style={{ cursor: "pointer" }}>
                  <span className="toolbar">
                    <span>{"原始 input（" + rawInput.length + " 个字符）"}</span>
                    <span onClick={(event) => event.stopPropagation()}>
                      <CopyButton value={rawInput} label="原始 input" />
                    </span>
                  </span>
                </summary>
                <pre className="s5-json" style={{ marginTop: "var(--sp-3)" }}>
                  {rawInput}
                </pre>
              </details>
            ) : (
              <EmptyState title="这笔交易没有 input 数据" />
            )}
          </Card>
        </PageSection>
      </>
    );
  };

  const renderLogs = () => {
    if (logs.loading && !logs.data) {
      return (
        <PageSection title="Logs">
          <Card flush>
            <SkeletonRows rows={6} columns={5} />
          </Card>
        </PageSection>
      );
    }
    if (!logs.data) {
      return (
        <PageSection title="Logs">
          <Card flush>
            <ErrorState title="日志数据不可用" message="加载失败，请重试。" onRetry={() => void logs.reload()} />
          </Card>
        </PageSection>
      );
    }
    const rows = logs.data.items ?? [];
    return (
      <PageSection title="Logs" count={rows.length + " 条事件"}>
        <Card flush>
          <LogsTable rows={rows} />
        </Card>
      </PageSection>
    );
  };

  const renderTrace = () => {
    if (trace) return <TraceResultView trace={trace} chain={chain} />;
    if (localTrace.loading && !localTrace.data) {
      return (
        <PageSection title="Trace">
          <Card flush>
            <SkeletonRows rows={5} columns={3} />
          </Card>
        </PageSection>
      );
    }
    const localMissing = !localTrace.error || localTrace.error.includes("本地没有 Trace");
    if (!localMissing && !localTrace.data) {
      return (
        <PageSection title="Trace">
          <Card flush>
            <ErrorState title="Trace 加载失败" message="加载失败，请重试。" onRetry={() => void localTrace.reload()} />
          </Card>
        </PageSection>
      );
    }
    return (
      <PageSection title="Trace">
        <Card flush>
          <EmptyState
            title="本地无 Trace"
            action={
              <>
                <Button variant="primary" size="sm" busy={action.busy} busyLabel="请求中…" onClick={() => void fetchProviderTrace()}>
                  向 Provider 补取 Trace
                </Button>
                {/* Provider 拉取没有服务端进度：进度条保持 indeterminate，绝不伪造百分比。 */}
                {action.busy ? <LinearProgress percent={null} state="running" label="Provider Trace 补取" /> : null}
              </>
            }
          />
        </Card>
      </PageSection>
    );
  };

  const content = tab === "logs" ? renderLogs() : tab === "trace" ? renderTrace() : renderOverview();

  return (
    <>
      <BackLink to="/explorer" label="返回地址与交易查询" />
      <PageHeader
        breadcrumb={[{ label: "地址与交易查询", to: "/explorer" }, { label: "交易详情" }]}
        title="交易详情"
        status={<StatusBadge tone={statusToneOf(record)}>{statusLabel(record)}</StatusBadge>}
        subtitle={
          <div className="cs-objecthead">
            <div className="cs-objecthead__top">
              <span className="cs-objecthead__kind">{chain || "链未知"}</span>
              <span className="cs-objecthead__value">
                <HashValue value={tx?.tx_hash || hash} label="交易哈希" wrap />
              </span>
            </div>
            <MetaLine
              items={[
                { label: "区块", value: record?.block_number ?? "—" },
                { label: "时间", value: formatTime(record?.timestamp) },
                { label: "方法", value: tx?.method?.signature || tx?.method?.name || "—" },
                { label: "解码来源", value: tx?.decode_source || "—" },
                { label: "来源 provider", value: record?.source_provider || "—" },
              ]}
            />
          </div>
        }
        primaryAction={
          <Button size="sm" variant="primary" onClick={() => void copy(tx?.tx_hash ?? hash, "交易哈希")}>
            复制哈希
          </Button>
        }
      />
      {/*
        Tab 保持链接语义（写进 URL、刷新可恢复、现有 E2E 按 role=link 定位）；
        选中态由 .cs-tab[aria-current="page"] 表达，与按钮版 .cs-tab[aria-selected="true"] 同一套视觉。
      */}
      <nav className="cs-tabs" aria-label="交易详情分区">
        {TABS.map((item) => (
          <a
            key={item.key}
            className="cs-tab"
            href={"?" + "tab=" + item.key}
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
      <div className="cs-bento-stack">{content}</div>
    </>
  );
}
