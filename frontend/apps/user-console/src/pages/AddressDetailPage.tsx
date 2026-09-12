import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import {
  BackLink,
  Card,
  DataTable,
  EmptyState,
  ErrorState,
  EvidenceRail,
  Metric,
  PageHeader,
  SkeletonRows,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { useAuth } from "@chain/api-client";
import { formatRawAmount, formatTime, shortValue, useApi } from "../sprint5/common";

/**
 * U-EXP-02 地址详情。
 * - 只有真实 API：overview / assets / activity / counterparties / semantic / flow-profile。
 * - 普通交易 / Token Transfer / Internal 均来自 activity 真实行，按 activity_type 区分（Internal 为链上内部转账记录）。
 * - 当前余额（is_current_balance=true）与历史净流量（is_current_balance=false）分开呈现。
 * - Tab / cursor / 筛选全部写入 URL，刷新可恢复；筛选变化重置 cursor。
 */

interface Asset {
  token_symbol?: string;
  token_name?: string;
  token_address?: string;
  token_type?: string;
  decimals?: number | null;
  raw_amount?: string;
  display_amount?: string;
  amount_usd?: string;
  token_price?: string;
  snapshot_block?: number;
  observed_at?: string;
  source_provider?: string;
  source?: string;
  is_current_balance?: boolean;
}

interface NetFlowAsset {
  asset?: string;
  token_symbol?: string;
  token_address?: string;
  decimals?: number | null;
  in_raw?: string;
  out_raw?: string;
  net_raw?: string;
  transaction_count?: number;
  source?: string;
  is_current_balance?: boolean;
}

interface BalanceBlock<T> {
  source?: string;
  is_current_balance?: boolean;
  observed_at?: string | null;
  items?: T[];
  note?: string;
}

interface CoverageItem {
  dataset?: string;
  from_block?: number | null;
  to_block?: number | null;
  from_timestamp?: string | null;
  to_timestamp?: string | null;
  updated_at?: string | null;
  complete_through?: string | null;
  status?: string;
  source?: string;
}

interface OverviewData {
  subject?: { type?: string; chain?: string; address?: string; normalized_address?: string };
  summary?: {
    transaction_count?: number;
    token_transfer_count?: number;
    counterparty_count?: number;
    first_seen?: string | null;
    last_seen?: string | null;
    current_balance?: BalanceBlock<Asset>;
    historical_net_flow?: BalanceBlock<NetFlowAsset>;
  };
  coverage?: { items?: CoverageItem[]; source?: string };
  caliber?: Record<string, unknown>;
  source?: { provider?: string; tables?: string[]; as_of?: string };
}

interface AssetsPayload {
  items?: { current_balance?: Asset[]; historical_net_flow?: NetFlowAsset[] };
}

interface ActivityRow {
  chain?: string;
  address?: string;
  counterparty?: string;
  direction?: string;
  activity_type?: string;
  timestamp?: string;
  block_number?: number;
  tx_hash?: string;
  log_index?: number;
  token_address?: string;
  token_symbol?: string;
  token_decimals?: number;
  amount_raw?: string;
  amount?: string;
  amount_usd?: string;
  dataset?: string;
  source_provider?: string;
  source_ingested_at?: string;
}

interface CounterpartyRow {
  counterparty?: string;
  transaction_count?: number;
  in_raw?: string;
  out_raw?: string;
  net_raw?: string;
  first_seen?: string;
  last_seen?: string;
}

interface SemanticEvent {
  id?: string;
  type?: string;
  confidence?: number;
  payload?: unknown;
  provenance?: unknown;
  tx_hash?: string;
  algorithm_version?: string;
  occurred_at?: string;
  computed_at?: string;
}

interface SemanticResult {
  events?: SemanticEvent[];
  counts?: Record<string, number>;
  algorithm_version?: string;
  data_version?: string;
  computed_at?: string;
  evidence_boundary?: string;
  [key: string]: unknown;
}

interface AnalysisEdge {
  direction?: string;
  counterparty?: string;
  asset?: string;
  amount?: string;
  flow_ratio?: string;
  transactions?: number;
  first_seen?: string;
  last_seen?: string;
  evidence?: string[];
}

interface AnalysisData {
  algorithm_version?: string;
  transaction_count?: number;
  unique_counterparties?: number;
  first_seen?: string;
  last_seen?: string;
  edges?: AnalysisEdge[];
  first_funder?: AnalysisEdge | null;
  cashout_candidates?: AnalysisEdge[];
  evidence_boundary?: string;
}

const TABS = [
  { key: "overview", label: "概览" },
  { key: "assets", label: "资产" },
  { key: "transactions", label: "普通交易" },
  { key: "transfers", label: "Token Transfer" },
  { key: "internal", label: "Internal" },
  { key: "counterparties", label: "对手方" },
  { key: "semantic", label: "语义事件" },
  { key: "analysis", label: "分析" },
  { key: "coverage", label: "Coverage" },
];

function decimalsOf(item?: { decimals?: number | null }): number | undefined {
  const value = item?.decimals;
  return typeof value === "number" ? value : undefined;
}

function blockText(value?: number | null): string {
  return value === null || value === undefined ? "—" : String(value);
}

function jsonText(value: unknown, max = 140): string {
  try {
    const text = JSON.stringify(value);
    if (!text) return "—";
    return text.length > max ? `${text.slice(0, max)}…` : text;
  } catch {
    return "—";
  }
}

function extractEvents(result: unknown): SemanticEvent[] {
  const candidate = Array.isArray(result)
    ? result
    : result && typeof result === "object" && Array.isArray((result as SemanticResult).events)
      ? (result as SemanticResult).events
      : [];
  return (candidate ?? []).filter((item): item is SemanticEvent => Boolean(item) && typeof item === "object");
}

function hasSemanticResultShape(result: unknown): boolean {
  return Boolean(result) && typeof result === "object" && Array.isArray((result as SemanticResult).events);
}

function CurrentBalanceTable({ items }: { items: Asset[] }) {
  return (
    <DataTable<Asset>
      rows={items}
      rowKey={(row, index) => `${row.token_address ?? "native"}-${row.snapshot_block ?? "na"}-${index}`}
      empty={<EmptyState title="没有当前余额快照" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600 }}>{row.token_symbol || row.token_name || "未知资产"}</div>
              <div className="s4-list__sub mono" title={row.token_address}>
                {row.token_address ? shortValue(row.token_address, 10, 8) : "native"}
              </div>
            </div>
          ),
        },
        {
          key: "amount",
          header: "金额",
          align: "right",
          render: (row) => {
            const raw = row.raw_amount;
            const decimals = decimalsOf(row);
            return (
              <span className="amount" style={{ whiteSpace: "normal", display: "inline-block" }}>
                <div>{formatRawAmount(raw, decimals)}</div>
                <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400, overflowWrap: "anywhere" }}>
                  raw {raw ?? "—"}
                </div>
                {row.amount_usd ? (
                  <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400 }}>
                    ≈ {row.amount_usd} USD
                  </div>
                ) : null}
              </span>
            );
          },
        },
        {
          key: "decimals",
          header: "decimals",
          width: "92px",
          render: (row) => <span className="mono">{row.decimals ?? "—"}</span>,
        },
        {
          key: "source",
          header: "来源",
          width: "150px",
          render: (row) => (
            <span>
              <StatusBadge tone="info">{row.source || "—"}</StatusBadge>
              {row.source_provider ? <div className="s4-list__sub">{row.source_provider}</div> : null}
            </span>
          ),
        },
        {
          key: "block",
          header: "快照区块",
          width: "110px",
          render: (row) => <span className="mono">{blockText(row.snapshot_block)}</span>,
        },
        {
          key: "observed",
          header: "观测时间",
          width: "165px",
          render: (row) => <span>{formatTime(row.observed_at)}</span>,
        },
      ]}
    />
  );
}

function NetFlowTable({ items }: { items: NetFlowAsset[] }) {
  return (
    <DataTable<NetFlowAsset>
      rows={items}
      rowKey={(row, index) => `${row.token_address ?? row.asset ?? "native"}-${index}`}
      empty={<EmptyState title="没有历史净流量记录" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600 }}>{row.token_symbol || row.asset || "未知资产"}</div>
              <div className="s4-list__sub mono" title={row.token_address}>
                {row.token_address ? shortValue(row.token_address, 10, 8) : row.asset || "native"} · decimals{" "}
                {row.decimals ?? "—"}
              </div>
            </div>
          ),
        },
        {
          key: "in",
          header: "入账",
          align: "right",
          render: (row) => (
            <span className="amount" style={{ whiteSpace: "normal", display: "inline-block" }}>
              <div>{formatRawAmount(row.in_raw, decimalsOf(row))}</div>
              <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400, overflowWrap: "anywhere" }}>
                raw {row.in_raw ?? "—"}
              </div>
            </span>
          ),
        },
        {
          key: "out",
          header: "出账",
          align: "right",
          render: (row) => (
            <span className="amount" style={{ whiteSpace: "normal", display: "inline-block" }}>
              <div>{formatRawAmount(row.out_raw, decimalsOf(row))}</div>
              <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400, overflowWrap: "anywhere" }}>
                raw {row.out_raw ?? "—"}
              </div>
            </span>
          ),
        },
        {
          key: "net",
          header: "净额",
          align: "right",
          render: (row) => {
            const negative = (row.net_raw ?? "").startsWith("-");
            return (
              <span className="amount" style={{ whiteSpace: "normal", display: "inline-block" }}>
                <div style={{ color: negative ? "var(--c-danger)" : "var(--c-green)" }}>
                  {formatRawAmount(row.net_raw, decimalsOf(row))}
                </div>
                <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400, overflowWrap: "anywhere" }}>
                  raw {row.net_raw ?? "—"}
                </div>
              </span>
            );
          },
        },
        {
          key: "count",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transaction_count ?? "—"}</span>,
        },
        {
          key: "source",
          header: "来源",
          width: "120px",
          render: (row) => <StatusBadge tone="neutral">{row.source || "—"}</StatusBadge>,
        },
      ]}
    />
  );
}

function ActivityTable({
  rows,
  chain,
  loading,
  emptyTitle = "没有活动记录",
}: {
  rows: ActivityRow[];
  chain: string;
  loading?: boolean;
  emptyTitle?: string;
}) {
  const navigate = useNavigate();
  return (
    <DataTable<ActivityRow>
      rows={rows}
      loading={loading}
      rowKey={(row, index) =>
        `${row.tx_hash ?? "tx"}-${row.log_index ?? index}-${row.activity_type ?? ""}-${row.counterparty ?? ""}-${row.token_address ?? ""}`
      }
      empty={<EmptyState title={emptyTitle} />}
      columns={[
        {
          key: "time",
          header: "时间",
          width: "160px",
          render: (row) => <span>{formatTime(row.timestamp)}</span>,
        },
        {
          key: "direction",
          header: "方向",
          width: "78px",
          render: (row) => (
            <StatusBadge tone={row.direction === "in" ? "ok" : row.direction === "out" ? "warn" : "neutral"}>
              {row.direction || "—"}
            </StatusBadge>
          ),
        },
        {
          key: "type",
          header: "类型",
          width: "100px",
          render: (row) => <span>{row.activity_type || "—"}</span>,
        },
        {
          key: "counterparty",
          header: "对手方",
          width: "150px",
          render: (row) =>
            row.counterparty ? (
              <a
                className="mono"
                href={`/addresses/${chain}/${row.counterparty}`}
                title={row.counterparty}
                onClick={(event) => {
                  event.preventDefault();
                  navigate(`/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(row.counterparty ?? "")}`);
                }}
              >
                {shortValue(row.counterparty, 10, 8)}
              </a>
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "asset",
          header: "资产",
          width: "130px",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div>{row.token_symbol || (row.token_address ? shortValue(row.token_address, 8, 6) : "原生")}</div>
              {row.token_address ? <div className="s4-list__sub mono">{shortValue(row.token_address, 8, 6)}</div> : null}
            </div>
          ),
        },
        {
          key: "amount",
          header: "金额",
          align: "right",
          render: (row) => (
            <span className="amount" style={{ whiteSpace: "normal", display: "inline-block" }}>
              <div>{formatRawAmount(row.amount_raw, row.token_decimals)}</div>
              <div className="mono" style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", fontWeight: 400, overflowWrap: "anywhere" }}>
                raw {row.amount_raw ?? "—"}
              </div>
            </span>
          ),
        },
        {
          key: "tx",
          header: "tx_hash",
          width: "150px",
          render: (row) =>
            row.tx_hash ? (
              <a
                className="mono"
                href={`/transactions/${chain}/${row.tx_hash}`}
                title={row.tx_hash}
                onClick={(event) => {
                  event.preventDefault();
                  navigate(`/transactions/${encodeURIComponent(chain)}/${encodeURIComponent(row.tx_hash ?? "")}`);
                }}
              >
                {shortValue(row.tx_hash, 10, 8)}
              </a>
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "source",
          header: "来源",
          width: "140px",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div>{row.source_provider || "—"}</div>
              {row.dataset ? <div className="s4-list__sub">{row.dataset}</div> : null}
            </div>
          ),
        },
      ]}
    />
  );
}

function CounterpartyTable({ rows, chain }: { rows: CounterpartyRow[]; chain: string }) {
  const navigate = useNavigate();
  return (
    <DataTable<CounterpartyRow>
      rows={rows}
      rowKey={(row, index) => `${row.counterparty ?? "cp"}-${index}`}
      empty={<EmptyState title="没有对手方记录" />}
      columns={[
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <a
                className="mono"
                href={`/addresses/${chain}/${row.counterparty}`}
                title={row.counterparty}
                onClick={(event) => {
                  event.preventDefault();
                  navigate(`/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(row.counterparty ?? "")}`);
                }}
              >
                {shortValue(row.counterparty, 10, 8)}
              </a>
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "count",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transaction_count ?? "—"}</span>,
        },
        {
          key: "in",
          header: "入账 raw",
          align: "right",
          render: (row) => <span className="mono">{row.in_raw ?? "—"}</span>,
        },
        {
          key: "out",
          header: "出账 raw",
          align: "right",
          render: (row) => <span className="mono">{row.out_raw ?? "—"}</span>,
        },
        {
          key: "net",
          header: "净额 raw",
          align: "right",
          render: (row) => (
            <span className="mono" style={{ color: (row.net_raw ?? "").startsWith("-") ? "var(--c-danger)" : undefined }}>
              {row.net_raw ?? "—"}
            </span>
          ),
        },
        {
          key: "first",
          header: "首次",
          width: "160px",
          render: (row) => <span>{formatTime(row.first_seen)}</span>,
        },
        {
          key: "last",
          header: "最近",
          width: "160px",
          render: (row) => <span>{formatTime(row.last_seen)}</span>,
        },
      ]}
    />
  );
}

function SemanticTable({ events, chain }: { events: SemanticEvent[]; chain: string }) {
  const navigate = useNavigate();
  return (
    <DataTable<SemanticEvent>
      rows={events}
      rowKey={(row, index) => `${row.id ?? row.tx_hash ?? "event"}-${index}`}
      empty={<EmptyState title="没有语义事件" />}
      columns={[
        {
          key: "type",
          header: "类型",
          width: "170px",
          render: (row) => (
            <div>
              <StatusBadge tone="info">{row.type || "—"}</StatusBadge>
              <div className="s4-list__sub">confidence {row.confidence ?? "—"}</div>
            </div>
          ),
        },
        {
          key: "tx",
          header: "tx_hash",
          width: "150px",
          render: (row) =>
            row.tx_hash ? (
              <a
                className="mono"
                href={`/transactions/${chain}/${row.tx_hash}`}
                title={row.tx_hash}
                onClick={(event) => {
                  event.preventDefault();
                  navigate(`/transactions/${encodeURIComponent(chain)}/${encodeURIComponent(row.tx_hash ?? "")}`);
                }}
              >
                {shortValue(row.tx_hash, 10, 8)}
              </a>
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "algorithm",
          header: "algorithm_version",
          width: "180px",
          render: (row) => <span className="mono">{row.algorithm_version || "—"}</span>,
        },
        {
          key: "payload",
          header: "payload",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.payload)}>
              {jsonText(row.payload, 120)}
            </span>
          ),
        },
        {
          key: "provenance",
          header: "provenance",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.provenance)}>
              {jsonText(row.provenance, 100)}
            </span>
          ),
        },
      ]}
    />
  );
}

function EdgeTable({ edges, chain, emptyTitle }: { edges: AnalysisEdge[]; chain: string; emptyTitle: string }) {
  const navigate = useNavigate();
  return (
    <DataTable<AnalysisEdge>
      rows={edges}
      rowKey={(row, index) => `${row.direction ?? ""}-${row.counterparty ?? "cp"}-${row.asset ?? ""}-${index}`}
      empty={<EmptyState title={emptyTitle} />}
      columns={[
        {
          key: "direction",
          header: "方向",
          width: "90px",
          render: (row) => (
            <StatusBadge tone={row.direction === "in" ? "ok" : row.direction === "out" ? "warn" : "neutral"}>
              {row.direction || "—"}
            </StatusBadge>
          ),
        },
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <a
                className="mono"
                href={`/addresses/${chain}/${row.counterparty}`}
                title={row.counterparty}
                onClick={(event) => {
                  event.preventDefault();
                  navigate(`/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(row.counterparty ?? "")}`);
                }}
              >
                {shortValue(row.counterparty, 10, 8)}
              </a>
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "asset",
          header: "资产",
          width: "120px",
          render: (row) => <span className="mono">{row.asset || "—"}</span>,
        },
        {
          key: "amount",
          header: "金额 raw",
          align: "right",
          render: (row) => <span className="mono">{row.amount ?? "—"}</span>,
        },
        {
          key: "ratio",
          header: "flow_ratio",
          align: "right",
          width: "110px",
          render: (row) => <span className="mono">{row.flow_ratio ?? "—"}</span>,
        },
        {
          key: "transactions",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transactions ?? "—"}</span>,
        },
        {
          key: "first",
          header: "首次",
          width: "160px",
          render: (row) => <span>{formatTime(row.first_seen)}</span>,
        },
        {
          key: "last",
          header: "最近",
          width: "160px",
          render: (row) => <span>{formatTime(row.last_seen)}</span>,
        },
      ]}
    />
  );
}

function AnalysisView({ data, chain }: { data: AnalysisData; chain: string }) {
  return (
    <>
      <Card title="分析指标">
        <div className="metric-grid">
          <Metric label="交易数" value={data.transaction_count ?? "—"} />
          <Metric label="唯一对手方" value={data.unique_counterparties ?? "—"} />
          <Metric label="首次出现" value={formatTime(data.first_seen)} />
          <Metric label="最近出现" value={formatTime(data.last_seen)} />
          <Metric label="算法版本" value={data.algorithm_version ?? "—"} />
        </div>
      </Card>
      <Card title="首笔资金来源" flush>
        {data.first_funder ? (
          <dl className="s4-kv" style={{ padding: "var(--sp-4)" }}>
            <div className="s4-kv__row">
              <dt>counterparty</dt>
              <dd className="mono" style={{ overflowWrap: "anywhere" }}>
                {data.first_funder.counterparty || "—"}
              </dd>
            </div>
            <div className="s4-kv__row">
              <dt>asset</dt>
              <dd className="mono">{data.first_funder.asset || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>amount raw</dt>
              <dd className="mono">{data.first_funder.amount || "—"}</dd>
            </div>
            <div className="s4-kv__row">
              <dt>首次</dt>
              <dd>{formatTime(data.first_funder.first_seen)}</dd>
            </div>
          </dl>
        ) : (
          <EmptyState title="没有识别到首笔资金来源" />
        )}
      </Card>
      <Card title="现金流出候选" flush>
        <EdgeTable edges={data.cashout_candidates ?? []} chain={chain} emptyTitle="没有现金流出候选" />
      </Card>
      <Card title="资金流边" flush>
        <EdgeTable edges={data.edges ?? []} chain={chain} emptyTitle="没有资金流边" />
      </Card>
    </>
  );
}

function CoverageTable({ items }: { items: CoverageItem[] }) {
  return (
    <DataTable<CoverageItem>
      rows={items}
      rowKey={(row, index) => `${row.dataset ?? "dataset"}-${row.source ?? "source"}-${index}`}
      empty={<EmptyState title="没有 Coverage 记录" />}
      columns={[
        {
          key: "dataset",
          header: "dataset",
          width: "160px",
          render: (row) => <span className="mono">{row.dataset || "—"}</span>,
        },
        {
          key: "from",
          header: "from_block",
          width: "110px",
          render: (row) => <span className="mono">{blockText(row.from_block)}</span>,
        },
        {
          key: "to",
          header: "to_block",
          width: "110px",
          render: (row) => <span className="mono">{blockText(row.to_block)}</span>,
        },
        {
          key: "through",
          header: "complete_through",
          width: "180px",
          render: (row) => <span>{formatTime(row.complete_through)}</span>,
        },
        {
          key: "status",
          header: "status",
          width: "120px",
          render: (row) => (
            <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status || "—"}</StatusBadge>
          ),
        },
        {
          key: "source",
          header: "source",
          width: "200px",
          render: (row) => <span className="mono" style={{ overflowWrap: "anywhere" }}>{row.source || "—"}</span>,
        },
        {
          key: "updated",
          header: "updated_at",
          width: "165px",
          render: (row) => <span>{formatTime(row.updated_at)}</span>,
        },
      ]}
    />
  );
}

export default function AddressDetailPage() {
  const { chain = "", address = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const dialog = useDialog();
  const { client } = useAuth();
  const [bulkRows, setBulkRows] = useState<ActivityRow[] | null>(null);
  const [bulkLoading, setBulkLoading] = useState(false);
  const requestedTab = searchParams.get("tab") ?? "overview";
  const tab = TABS.some((item) => item.key === requestedTab) ? requestedTab : "overview";
  const cursor = searchParams.get("cursor") ?? "";
  const direction = searchParams.get("direction") ?? "";
  const token = searchParams.get("token") ?? "";
  const [tokenDraft, setTokenDraft] = useState(token);
  const notifiedErrorsRef = useRef<Set<string>>(new Set());

  const ready = Boolean(chain && address);
  const base = ready
    ? `/api/v2/explorer/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}`
    : null;

  const overview = useApi<OverviewData>(base ? `${base}/overview` : null, [chain, address]);
  const assets = useApi<AssetsPayload>(tab === "assets" && base ? `${base}/assets` : null, [chain, address, tab]);
  const activityActive = tab === "transactions" || tab === "transfers" || tab === "internal";
  const activity = useApi<{ items: ActivityRow[] }>(
    activityActive && base ? `${base}/activity` : null,
    [chain, address, tab, cursor, direction, token],
    activityActive
      ? {
          limit: 200,
          cursor: cursor || undefined,
          direction: direction || undefined,
          token: tab === "transfers" ? token || undefined : undefined,
        }
      : undefined,
  );
  const counterparties = useApi<{ items: CounterpartyRow[] }>(
    tab === "counterparties" && base ? `${base}/counterparties` : null,
    [chain, address, tab],
    { limit: 200 },
  );
  const semantic = useApi<{ items: unknown }>(
    tab === "semantic" && base ? `${base}/semantic` : null,
    [chain, address, tab],
  );
  const analysis = useApi<{ items: AnalysisData }>(
    tab === "analysis" && base ? `${base}/flow-profile` : null,
    [chain, address, tab],
  );

  const errors = [
    overview.error,
    assets.error,
    activity.error,
    counterparties.error,
    semantic.error,
    analysis.error,
  ].filter((message): message is string => Boolean(message));

  const errorsKey = errors.join("|");
  useEffect(() => {
    if (errors.length === 0) {
      notifiedErrorsRef.current.clear();
      return;
    }
    for (const message of errors) {
      if (notifiedErrorsRef.current.has(message)) continue;
      notifiedErrorsRef.current.add(message);
      dialog.notify("error", "加载失败", message);
    }
    // errorsKey 仅用于在错误集合变化时触发本 effect。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [errorsKey, dialog]);

  useEffect(() => {
    setTokenDraft(token);
  }, [token]);

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

  const explainInternal = () => {
    dialog.notify(
      "info",
      "Internal 数据说明",
      "Internal 行来自本地 address_activity 中 activity_type=internal_transfer 的真实记录；是否可用取决于该链 internal_transactions 数据集是否已同步。空列表表示本地索引没有记录，不代表链上不存在。",
    );
  };

  const showCaliber = () => {
    const caliber = overview.data?.caliber ?? {};
    const details = Object.entries(caliber).map(([label, value]) => ({
      label,
      value: typeof value === "string" ? value : jsonText(value),
    }));
    dialog.notify("info", "数据口径", undefined, details.length > 0 ? details : undefined);
  };

  const subject = overview.data?.subject;
  const summary = overview.data?.summary;
  const currentBlock = summary?.current_balance;
  const flowBlock = summary?.historical_net_flow;
  const coverageItems = overview.data?.coverage?.items ?? [];
  const currentAssets = currentBlock?.items ?? [];
  const flowItems = flowBlock?.items ?? [];
  const assetsPayload = assets.data?.items;
  const events = extractEvents(semantic.data?.items);
  const nextCursor = activity.meta.next_cursor ?? "";

  // UX-004：显式加载最多 10,000 行真实数据，验证客户端虚拟滚动而非渲染 10k DOM 行。
  const loadAllActivity = async () => {
    if (!base || bulkLoading) return;
    const answer = await dialog.confirm({
      title: "加载全量活动行？",
      body: "将按 cursor 顺序最多加载 10,000 行真实本地索引数据；表格使用虚拟滚动，只渲染视口附近的行。数据量较大会增加等待时间。",
      details: [
        { label: "上限", value: "10,000 行" },
        { label: "单页", value: "200 行" },
        { label: "渲染", value: "虚拟滚动（DOM 行数受视口限制）" },
      ],
      actions: [
        { label: "取消", result: "", variant: "default" },
        { label: "开始加载", result: "load", variant: "primary" },
      ],
    });
    if (answer !== "load") return;
    setBulkLoading(true);
    const collected: ActivityRow[] = [];
    let cursorValue = "";
    try {
      for (let page = 0; page < 50 && collected.length < 10000; page += 1) {
        const response = await client.get<{ items: ActivityRow[] }>(`${base}/activity`, {
          query: {
            limit: 200,
            cursor: cursorValue || undefined,
            direction: direction || undefined,
            token: token || undefined,
          },
        });
        const items = response.data?.items ?? [];
        collected.push(...items);
        const next = response.meta.next_cursor ?? "";
        if (!next || items.length === 0) break;
        cursorValue = next;
      }
      setBulkRows(collected.slice(0, 10000));
      dialog.notify("success", "全量活动已加载", `共 ${collected.length} 行；表格仅渲染视口附近的行。`);
    } catch (error) {
      dialog.notify("error", "加载全量活动失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setBulkLoading(false);
    }
  };

  const renderOverview = () => {
    if (overview.loading && !overview.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    if (!overview.data) {
      return (
        <Card flush>
          <ErrorState title="概览数据不可用" message="加载失败，请重试。" onRetry={() => void overview.reload()} />
        </Card>
      );
    }
    const railItems = [
      { label: "数据来源", value: overview.data.source?.provider || "—" },
      { label: "as_of", value: formatTime(overview.data.source?.as_of) },
      { label: "数据表", value: (overview.data.source?.tables ?? []).join("，") || "—" },
      { label: "Coverage", value: `${coverageItems.length} 个 dataset` },
      {
        label: "余额口径",
        value: `${currentBlock?.source || "—"} · is_current_balance=${String(currentBlock?.is_current_balance ?? true)}`,
      },
      {
        label: "净流口径",
        value: `${flowBlock?.source || "—"} · is_current_balance=${String(flowBlock?.is_current_balance ?? false)}`,
      },
    ];
    return (
      <div className="workbench">
        <div style={{ flex: 1, minWidth: 0, display: "flex", flexDirection: "column", gap: "var(--sp-5)" }}>
          <Card title="概览指标">
            <div className="metric-grid">
              <Metric label="交易数" value={summary?.transaction_count ?? "—"} />
              <Metric label="Token Transfer" value={summary?.token_transfer_count ?? "—"} />
              <Metric label="对手方" value={summary?.counterparty_count ?? "—"} />
              <Metric label="首次出现" value={formatTime(summary?.first_seen)} />
              <Metric label="最近出现" value={formatTime(summary?.last_seen)} />
            </div>
          </Card>
          <Card
            flush
            title={
              <span>
                当前余额 <StatusBadge tone="info">is_current_balance=true</StatusBadge>
              </span>
            }
            actions={
              <span className="s4-list__sub">
                {currentBlock?.source || "—"}
                {currentBlock?.observed_at ? ` · ${formatTime(currentBlock.observed_at)}` : ""}
              </span>
            }
          >
            <CurrentBalanceTable items={currentAssets} />
          </Card>
          <Card
            flush
            title={
              <span>
                历史净流量 <StatusBadge tone="neutral">is_current_balance=false</StatusBadge>
              </span>
            }
            actions={<span className="s4-list__sub">{flowBlock?.source || "—"}</span>}
          >
            <NetFlowTable items={flowItems} />
          </Card>
        </div>
        <EvidenceRail title="数据来源" items={railItems} />
      </div>
    );
  };

  const renderAssets = () => {
    if (assets.loading && !assets.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    if (!assets.data) {
      return (
        <Card flush>
          <ErrorState title="资产数据不可用" message="加载失败，请重试。" onRetry={() => void assets.reload()} />
        </Card>
      );
    }
    return (
      <div style={{ display: "flex", flexDirection: "column", gap: "var(--sp-5)", minWidth: 0 }}>
        <Card
          flush
          title={
            <span>
              当前余额 <StatusBadge tone="info">is_current_balance=true</StatusBadge>
            </span>
          }
        >
          <CurrentBalanceTable items={assetsPayload?.current_balance ?? []} />
        </Card>
        <Card
          flush
          title={
            <span>
              历史净流量 <StatusBadge tone="neutral">is_current_balance=false</StatusBadge>
            </span>
          }
        >
          <NetFlowTable items={assetsPayload?.historical_net_flow ?? []} />
        </Card>
      </div>
    );
  };

  const activityTitle = tab === "transfers" ? "Token Transfer" : tab === "internal" ? "Internal" : "普通交易";

  const renderActivity = () => {
    if (activity.loading && !activity.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={6} />
        </Card>
      );
    }
    if (!activity.data) {
      return (
        <Card flush>
          <ErrorState
            title={`${activityTitle} 数据不可用`}
            message="加载失败，请重试。"
            onRetry={() => void activity.reload()}
          />
        </Card>
      );
    }
    const rows = activity.data?.items ?? [];
    const filtered =
      tab === "transfers"
        ? rows.filter((row) => row.activity_type === "transfer")
        : tab === "internal"
          ? rows.filter((row) => row.activity_type === "internal_transfer")
          : rows.filter((row) => row.activity_type !== "transfer" && row.activity_type !== "internal_transfer");
    return (
      <>
        <Card title="筛选">
          <div className="toolbar">
            <select
              className="select"
              style={{ flex: "0 1 170px", minWidth: 0 }}
              aria-label="方向筛选"
              value={direction}
              onChange={(event) => updateParams({ direction: event.target.value || null, cursor: null })}
            >
              <option value="">全部方向</option>
              <option value="in">流入 in</option>
              <option value="out">流出 out</option>
              <option value="self">自转 self</option>
            </select>
            {tab === "transfers" ? (
              <>
                <input
                  className="input"
                  style={{ flex: "1 1 220px", minWidth: 0 }}
                  aria-label="Token 筛选"
                  placeholder="Token 符号 / 合约地址"
                  value={tokenDraft}
                  onChange={(event) => setTokenDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      updateParams({ token: tokenDraft.trim() || null, cursor: null });
                    }
                  }}
                />
                <button
                  type="button"
                  className="btn btn--sm btn--primary"
                  onClick={() => updateParams({ token: tokenDraft.trim() || null, cursor: null })}
                >
                  应用
                </button>
              </>
            ) : null}
            <button
              type="button"
              className="btn btn--sm btn--ghost"
              onClick={() => updateParams({ direction: null, token: null, cursor: null })}
            >
              重置
            </button>
            {tab === "internal" ? (
              <button type="button" className="btn btn--sm btn--ghost" onClick={explainInternal}>
                说明
              </button>
            ) : null}
          </div>
        </Card>
        <Card flush title={activityTitle}>
          <ActivityTable
            rows={bulkRows ?? filtered}
            chain={chain}
            loading={activity.loading || bulkLoading}
            emptyTitle={`没有 ${activityTitle} 记录`}
          />
          {activity.data ? (
            <div
              className="toolbar"
              style={{
                justifyContent: "flex-end",
                padding: "var(--sp-3) var(--sp-4)",
                borderTop: "1px solid var(--c-border)",
              }}
            >
              {bulkRows ? (
                <button type="button" className="btn btn--sm" onClick={() => setBulkRows(null)}>
                  退出全量视图（{bulkRows.length} 行）
                </button>
              ) : (
                <>
                  {cursor ? (
                    <button type="button" className="btn btn--sm" onClick={() => updateParams({ cursor: null })}>
                      回到最新
                    </button>
                  ) : null}
                  <span style={{ color: "var(--c-slate)", fontSize: "var(--fs-12)" }}>本页 {filtered.length} 行</span>
                  <button
                    type="button"
                    className="btn btn--sm"
                    onClick={() => void loadAllActivity()}
                    disabled={bulkLoading}
                  >
                    {bulkLoading ? "加载中…" : "加载全量（最多 10,000 行）"}
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm"
                    disabled={!nextCursor}
                    onClick={() => updateParams({ cursor: nextCursor })}
                  >
                    加载下一页
                  </button>
                </>
              )}
            </div>
          ) : null}
        </Card>
      </>
    );
  };

  const renderCounterparties = () => {
    if (counterparties.loading && !counterparties.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={5} />
        </Card>
      );
    }
    if (!counterparties.data) {
      return (
        <Card flush>
          <ErrorState title="对手方数据不可用" message="加载失败，请重试。" onRetry={() => void counterparties.reload()} />
        </Card>
      );
    }
    return (
      <Card flush title="对手方">
        <CounterpartyTable rows={counterparties.data.items ?? []} chain={chain} />
      </Card>
    );
  };

  const renderSemantic = () => {
    if (semantic.loading && !semantic.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    if (!semantic.data) {
      return (
        <Card flush>
          <ErrorState title="语义事件不可用" message="加载失败，请重试。" onRetry={() => void semantic.reload()} />
        </Card>
      );
    }
    const result = semantic.data.items;
    if (result != null && !hasSemanticResultShape(result)) {
      return (
        <Card title="语义结果">
          <pre
            className="mono"
            style={{
              margin: 0,
              whiteSpace: "pre-wrap",
              wordBreak: "break-all",
              maxHeight: 480,
              overflow: "auto",
              fontSize: "var(--fs-12)",
            }}
          >
            {jsonText(result, 4000)}
          </pre>
        </Card>
      );
    }
    return (
      <Card flush title="语义事件">
        <SemanticTable events={events} chain={chain} />
      </Card>
    );
  };

  const renderAnalysis = () => {
    if (analysis.loading && !analysis.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    const data = analysis.data?.items;
    if (!data) {
      return (
        <Card flush>
          <ErrorState title="分析数据不可用" message="加载失败，请重试。" onRetry={() => void analysis.reload()} />
        </Card>
      );
    }
    return <AnalysisView data={data} chain={chain} />;
  };

  const renderCoverage = () => {
    if (overview.loading && !overview.data) {
      return (
        <Card flush>
          <SkeletonRows rows={6} columns={4} />
        </Card>
      );
    }
    if (!overview.data) {
      return (
        <Card flush>
          <ErrorState title="Coverage 数据不可用" message="加载失败，请重试。" onRetry={() => void overview.reload()} />
        </Card>
      );
    }
    return (
      <Card
        flush
        title="Coverage"
        actions={<span className="s4-list__sub">{overview.data.coverage?.source || "—"}</span>}
      >
        <CoverageTable items={coverageItems} />
      </Card>
    );
  };

  const content =
    tab === "overview"
      ? renderOverview()
      : tab === "assets"
        ? renderAssets()
        : tab === "transactions" || tab === "transfers" || tab === "internal"
          ? renderActivity()
          : tab === "counterparties"
              ? renderCounterparties()
              : tab === "semantic"
                ? renderSemantic()
                : tab === "analysis"
                  ? renderAnalysis()
                  : renderCoverage();

  return (
    <>
      <BackLink to="/explorer" label="返回地址与交易查询" />
      <PageHeader
        title="地址详情"
        subtitle={chain ? `${chain} · ${shortValue(address, 12, 8)}` : undefined}
        actions={
          <>
            <button type="button" className="btn btn--sm" onClick={showCaliber} disabled={!overview.data}>
              口径说明
            </button>
            <button
              type="button"
              className="btn btn--sm"
              onClick={() => void copy(subject?.normalized_address ?? address, "地址")}
            >
              复制地址
            </button>
          </>
        }
      />
      <Card title="对象" actions={<StatusBadge tone="info">{subject?.type || "address"}</StatusBadge>}>
        <dl className="s4-kv">
          <div className="s4-kv__row">
            <dt>chain</dt>
            <dd className="mono">{chain || "—"}</dd>
          </div>
          <div className="s4-kv__row">
            <dt>address</dt>
            <dd className="mono" style={{ overflowWrap: "anywhere" }}>
              {subject?.address || address || "—"}
            </dd>
          </div>
          <div className="s4-kv__row">
            <dt>normalized_address</dt>
            <dd className="mono" style={{ overflowWrap: "anywhere" }}>
              {subject?.normalized_address || "—"}
            </dd>
          </div>
          <div className="s4-kv__row">
            <dt>数据来源</dt>
            <dd className="mono">{overview.data?.source?.provider || "—"}</dd>
          </div>
        </dl>
      </Card>
      <nav className="s4-tabs" aria-label="地址详情分区">
        {TABS.map((item) => {
          const params = new URLSearchParams(searchParams);
          params.set("tab", item.key);
          params.delete("cursor");
          if (item.key !== "transfers") params.delete("token");
          return (
            <a
              key={item.key}
              className={`s4-tab${item.key === tab ? " is-active" : ""}`}
              href={`?${params.toString()}`}
              aria-current={item.key === tab ? "page" : undefined}
              onClick={(event) => {
                event.preventDefault();
                updateParams({
                  tab: item.key,
                  cursor: null,
                  ...(item.key !== "transfers" ? { token: null } : {}),
                });
              }}
            >
              {item.label}
            </a>
          );
        })}
      </nav>
      {content}
    </>
  );
}
