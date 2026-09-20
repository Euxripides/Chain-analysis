/**
 * 地址详情的展示表格（用户端重构规范 V4 §43）
 *
 * 从 1434 行的 AddressDetailPage.tsx **原样抽出**，每个组件都是纯展示：
 * 只接收已经取好的行数据，不发请求、不改状态、不做业务判断。
 * 这样主页面只剩编排（取数 / URL 状态 / 检查器联动），表格可以独立评审与复用。
 *
 * 注意：这些组件是 9 个业务 Tab 的公共底座（概览与资产共用余额/净流量表，
 * 普通交易 / Token Transfer / Internal 共用 ActivityTable），因此放在共享模块里，
 * 而不是复制三份——Tab 的 key 与顺序仍由 address-detail.types.ts 的 TABS 单一来源决定。
 */
import { Link } from "react-router-dom";
import { Amount, CopyButton, DataTable, EmptyState, HashValue, StatusBadge, statusToneFor, Card, PageSection, KeyValueList, MetricCard } from "@chain/ui";

import { formatRawAmount, formatTime, shortValue } from "../../../sprint5/common";
import { addressPath, amountDirection, blockText, decimalsOf, directionTone, fiatText, jsonText, transactionPath } from "../address-detail.types";
import type { ActivityRow, AnalysisData, AnalysisEdge, Asset, CounterpartyRow, CoverageItem, NetFlowAsset, SemanticEvent } from "../address-detail.types";

export function EntityHash({
  value,
  to,
  label,
  head = 10,
  tail = 8,
}: {
  value?: string;
  to: string;
  label: string;
  head?: number;
  tail?: number;
}) {
  if (!value) return <span>—</span>;
  return (
    <span className="cs-hash">
      <Link className="cs-hash__text" to={to} title={value} onClick={(event) => event.stopPropagation()}>
        {shortValue(value, head, tail)}
      </Link>
      {/* 复制按钮不能顺带触发整行的"选中到检查器"。 */}
      <span onClick={(event) => event.stopPropagation()}>
        <CopyButton value={value} label={label} />
      </span>
    </span>
  );
}

export function CurrentBalanceTable({ items }: { items: Asset[] }) {
  return (
    <DataTable<Asset>
      rows={items}
      rowKey={(row, index) => (row.token_address ?? "native") + "-" + (row.snapshot_block ?? "na") + "-" + index}
      empty={<EmptyState title="没有当前余额快照" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600 }}>{row.token_symbol || row.token_name || "未知资产"}</div>
              <div className="s4-list__sub">
                {row.token_address ? (
                  <HashValue value={row.token_address} label="Token 合约地址" head={10} tail={8} />
                ) : (
                  <span className="mono">native</span>
                )}
              </div>
            </div>
          ),
        },
        {
          key: "amount",
          header: "金额",
          align: "right",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <Amount
                rawAmount={row.raw_amount}
                decimals={decimalsOf(row)}
                unit={row.token_symbol}
                fiat={fiatText(row.amount_usd)}
              />
              <div className="s4-list__sub mono" title={row.raw_amount}>
                raw {row.raw_amount ?? "—"}
              </div>
            </div>
          ),
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

export function NetFlowTable({ items }: { items: NetFlowAsset[] }) {
  const rawCell = (raw?: string, decimals?: number, color?: string) => (
    <div style={{ minWidth: 0 }}>
      <div className="mono" style={color ? { color } : undefined}>
        {formatRawAmount(raw, decimals)}
      </div>
      <div className="s4-list__sub mono" title={raw}>
        raw {raw ?? "—"}
      </div>
    </div>
  );
  return (
    <DataTable<NetFlowAsset>
      rows={items}
      rowKey={(row, index) => (row.token_address ?? row.asset ?? "native") + "-" + index}
      empty={<EmptyState title="没有历史净流量记录" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600 }}>{row.token_symbol || row.asset || "未知资产"}</div>
              <div className="s4-list__sub">
                {row.token_address ? (
                  <HashValue value={row.token_address} label="Token 合约地址" head={10} tail={8} />
                ) : (
                  <span className="mono">{row.asset || "native"}</span>
                )}
                {" · decimals "}
                {row.decimals ?? "—"}
              </div>
            </div>
          ),
        },
        { key: "in", header: "入账", align: "right", render: (row) => rawCell(row.in_raw, decimalsOf(row)) },
        { key: "out", header: "出账", align: "right", render: (row) => rawCell(row.out_raw, decimalsOf(row)) },
        {
          key: "net",
          header: "净额",
          align: "right",
          render: (row) =>
            rawCell(
              row.net_raw,
              decimalsOf(row),
              (row.net_raw ?? "").startsWith("-") ? "var(--c-danger)" : "var(--c-green)",
            ),
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

export function ActivityTable({
  rows,
  chain,
  loading,
  emptyTitle = "没有活动记录",
  onSelectActivity,
}: {
  rows: ActivityRow[];
  chain: string;
  loading?: boolean;
  emptyTitle?: string;
  /** 行激活只负责"选中到检查器"；跳转仍由行内明确链接承担（蓝图 §8.4）。 */
  onSelectActivity?: (row: ActivityRow) => void;
}) {
  return (
    <DataTable<ActivityRow>
      rows={rows}
      loading={loading}
      rowKey={(row, index) =>
        (row.tx_hash ?? "tx") +
        "-" +
        (row.log_index ?? index) +
        "-" +
        (row.activity_type ?? "") +
        "-" +
        (row.counterparty ?? "") +
        "-" +
        (row.token_address ?? "")
      }
      empty={<EmptyState title={emptyTitle} />}
      onRowActivate={onSelectActivity}
      rowActivateLabel={(row) =>
        "在检查器中查看 " + formatTime(row.timestamp) + " 的 " + (row.activity_type || "活动") + " 详情"
      }
      columns={[
        { key: "time", header: "时间", width: "160px", render: (row) => <span>{formatTime(row.timestamp)}</span> },
        {
          key: "direction",
          header: "方向",
          width: "78px",
          render: (row) => <StatusBadge tone={directionTone(row.direction)}>{row.direction || "—"}</StatusBadge>,
        },
        { key: "type", header: "类型", width: "100px", render: (row) => <span>{row.activity_type || "—"}</span> },
        {
          key: "counterparty",
          header: "对手方",
          width: "150px",
          render: (row) =>
            row.counterparty ? (
              <EntityHash value={row.counterparty} to={addressPath(chain, row.counterparty)} label="对手方地址" />
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "asset",
          header: "资产",
          width: "150px",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div>{row.token_symbol || (row.token_address ? shortValue(row.token_address, 8, 6) : "原生")}</div>
              {row.token_address ? (
                <div className="s4-list__sub">
                  <HashValue value={row.token_address} label="Token 合约地址" head={8} tail={6} />
                </div>
              ) : null}
            </div>
          ),
        },
        {
          key: "amount",
          header: "金额",
          align: "right",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <Amount
                rawAmount={row.amount_raw}
                decimals={row.token_decimals}
                unit={row.token_symbol}
                direction={amountDirection(row.direction)}
                fiat={fiatText(row.amount_usd)}
              />
              <div className="s4-list__sub mono" title={row.amount_raw}>
                raw {row.amount_raw ?? "—"}
              </div>
            </div>
          ),
        },
        {
          key: "tx",
          header: "tx_hash",
          width: "150px",
          render: (row) =>
            row.tx_hash ? <EntityHash value={row.tx_hash} to={transactionPath(chain, row.tx_hash)} label="交易哈希" /> : <span>—</span>,
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

/**
 * NFT 转账表（读取面 /activity?asset_type=nft）。
 *
 * 列对齐参考样式：交易哈希 / 时间 / 发送方 / 接收方 / 数量 / 代币（合集 + token_id + 标准）。
 * 纯展示组件：只接收已取好的行，不发请求、不做业务判断（与 ActivityTable 同一约定）。
 */
export function NftTransferTable({
  rows,
  chain,
  loading,
  emptyTitle = "没有 NFT 转账记录",
  onSelectActivity,
}: {
  rows: ActivityRow[];
  chain: string;
  loading?: boolean;
  emptyTitle?: string;
  onSelectActivity?: (row: ActivityRow) => void;
}) {
  return (
    <DataTable<ActivityRow>
      rows={rows}
      loading={loading}
      rowKey={(row, index) => (row.tx_hash ?? "tx") + "-" + (row.log_index ?? index) + "-" + (row.token_id ?? "")}
      empty={<EmptyState title={emptyTitle} />}
      onRowActivate={onSelectActivity}
      rowActivateLabel={(row) => "在检查器中查看 " + formatTime(row.timestamp) + " 的 NFT 转账详情"}
      columns={[
        {
          key: "tx",
          header: "交易哈希",
          width: "170px",
          render: (row) =>
            row.tx_hash ? (
              <EntityHash value={row.tx_hash} to={transactionPath(chain, row.tx_hash)} label="交易哈希" />
            ) : (
              <span>—</span>
            ),
        },
        { key: "time", header: "时间", width: "160px", render: (row) => <span>{formatTime(row.timestamp)}</span> },
        {
          key: "from",
          header: "发送方",
          width: "170px",
          render: (row) =>
            row.from_address ? <HashValue value={row.from_address} label="发送方地址" head={8} tail={6} /> : <span>—</span>,
        },
        {
          key: "to",
          header: "接收方",
          width: "170px",
          render: (row) =>
            row.to_address ? <HashValue value={row.to_address} label="接收方地址" head={8} tail={6} /> : <span>—</span>,
        },
        { key: "amount", header: "数量", align: "right", render: (row) => <span className="mono">{row.amount_raw ?? "—"}</span> },
        {
          key: "nft",
          header: "代币",
          width: "220px",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div>{row.token_name || row.token_symbol || shortValue(row.contract_address, 8, 6)}</div>
              <div className="s4-list__sub mono">
                {"#" + (row.token_id || "—")}
                {row.token_standard ? " · " + row.token_standard : ""}
              </div>
            </div>
          ),
        },
      ]}
    />
  );
}

export function CounterpartyTable({
  rows,
  chain,
  onSelectCounterparty,
}: {
  rows: CounterpartyRow[];
  chain: string;
  onSelectCounterparty?: (row: CounterpartyRow) => void;
}) {
  return (
    <DataTable<CounterpartyRow>
      rows={rows}
      rowKey={(row, index) => (row.counterparty ?? "cp") + "-" + index}
      empty={<EmptyState title="没有对手方记录" />}
      onRowActivate={onSelectCounterparty}
      rowActivateLabel={(row) => "在检查器中查看对手方 " + (row.counterparty ?? "") + " 详情"}
      columns={[
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <EntityHash value={row.counterparty} to={addressPath(chain, row.counterparty)} label="对手方地址" />
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
        { key: "in", header: "入账 raw", align: "right", render: (row) => <span className="mono">{row.in_raw ?? "—"}</span> },
        { key: "out", header: "出账 raw", align: "right", render: (row) => <span className="mono">{row.out_raw ?? "—"}</span> },
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
        { key: "first", header: "首次", width: "160px", render: (row) => <span>{formatTime(row.first_seen)}</span> },
        { key: "last", header: "最近", width: "160px", render: (row) => <span>{formatTime(row.last_seen)}</span> },
      ]}
    />
  );
}

export function SemanticTable({ events, chain }: { events: SemanticEvent[]; chain: string }) {
  return (
    <DataTable<SemanticEvent>
      rows={events}
      rowKey={(row, index) => (row.id ?? row.tx_hash ?? "event") + "-" + index}
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
          width: "170px",
          render: (row) =>
            row.tx_hash ? (
              <EntityHash value={row.tx_hash} to={transactionPath(chain, row.tx_hash)} label="交易哈希" />
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
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.payload, 2000)}>
              {jsonText(row.payload, 120)}
            </span>
          ),
        },
        {
          key: "provenance",
          header: "provenance",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.provenance, 2000)}>
              {jsonText(row.provenance, 100)}
            </span>
          ),
        },
      ]}
    />
  );
}

export function EdgeTable({ edges, chain, emptyTitle }: { edges: AnalysisEdge[]; chain: string; emptyTitle: string }) {
  return (
    <DataTable<AnalysisEdge>
      rows={edges}
      rowKey={(row, index) => (row.direction ?? "") + "-" + (row.counterparty ?? "cp") + "-" + (row.asset ?? "") + "-" + index}
      empty={<EmptyState title={emptyTitle} />}
      columns={[
        {
          key: "direction",
          header: "方向",
          width: "90px",
          render: (row) => <StatusBadge tone={directionTone(row.direction)}>{row.direction || "—"}</StatusBadge>,
        },
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <EntityHash value={row.counterparty} to={addressPath(chain, row.counterparty)} label="对手方地址" />
            ) : (
              <span>—</span>
            ),
        },
        { key: "asset", header: "资产", width: "120px", render: (row) => <span className="mono">{row.asset || "—"}</span> },
        { key: "amount", header: "金额 raw", align: "right", render: (row) => <span className="mono">{row.amount ?? "—"}</span> },
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
        { key: "first", header: "首次", width: "160px", render: (row) => <span>{formatTime(row.first_seen)}</span> },
        { key: "last", header: "最近", width: "160px", render: (row) => <span>{formatTime(row.last_seen)}</span> },
      ]}
    />
  );
}

/** Fund Flow / Analysis：全部来自服务端 flow-profile 的真实边，不做前端推算。 */
export function AnalysisView({ data, chain }: { data: AnalysisData; chain: string }) {
  const edges = data.edges ?? [];
  const cashout = data.cashout_candidates ?? [];
  const funder = data.first_funder ?? null;
  return (
    <>
      <PageSection title="分析指标">
        <div className="metric-grid cs-stagger">
          <MetricCard
            label="交易数"
            value={data.transaction_count ?? "—"}
            source="flow-profile.transaction_count"
            sourceNote="由服务端 flow-profile 聚合返回，界面不做二次计算。"
          />
          <MetricCard
            label="唯一对手方"
            value={data.unique_counterparties ?? "—"}
            source="flow-profile.unique_counterparties"
            sourceNote="由服务端 flow-profile 聚合返回，界面不做二次计算。"
          />
          <MetricCard
            label="首次出现"
            value={formatTime(data.first_seen)}
            source="flow-profile.first_seen"
            sourceNote="来自服务端 flow-profile.first_seen：参与这些资金流关系的最早时间。"
          />
          <MetricCard
            label="最近出现"
            value={formatTime(data.last_seen)}
            source="flow-profile.last_seen"
            sourceNote="来自服务端 flow-profile.last_seen：参与这些资金流关系的最新时间。"
          />
          <MetricCard
            label="算法版本"
            value={data.algorithm_version ?? "—"}
            source="flow-profile.algorithm_version"
            sourceNote="产生这些资金流边的服务端算法版本；版本变化会改变边的口径。"
          />
        </div>
      </PageSection>
      <PageSection title="首笔资金来源">
        <Card flush>
          {funder ? (
            <div style={{ padding: "var(--sp-4)" }}>
              <KeyValueList
                items={[
                  {
                    label: "counterparty",
                    value: funder.counterparty ? (
                      <EntityHash
                        value={funder.counterparty}
                        to={addressPath(chain, funder.counterparty)}
                        label="对手方地址"
                        head={14}
                        tail={10}
                      />
                    ) : (
                      "—"
                    ),
                    wide: true,
                  },
                  { label: "asset", value: <span className="mono">{funder.asset || "—"}</span> },
                  { label: "amount raw", value: <span className="mono">{funder.amount || "—"}</span> },
                  { label: "首次", value: formatTime(funder.first_seen) },
                ]}
              />
            </div>
          ) : (
            <EmptyState title="没有识别到首笔资金来源" />
          )}
        </Card>
      </PageSection>
      <PageSection title="资金流边" count={edges.length + " 条"}>
        <Card flush>
          {/* 宽资金关系表自带滚动容器，不能把页面撑宽（蓝图 §13）。 */}
          <div className="cs-chart-frame">
            <EdgeTable edges={edges} chain={chain} emptyTitle="没有资金流边" />
          </div>
        </Card>
      </PageSection>
      <PageSection title="现金流出候选" count={cashout.length + " 条"}>
        <Card flush>
          <div className="cs-chart-frame">
            <EdgeTable edges={cashout} chain={chain} emptyTitle="没有现金流出候选" />
          </div>
        </Card>
      </PageSection>
    </>
  );
}

export function CoverageTable({ items }: { items: CoverageItem[] }) {
  return (
    <DataTable<CoverageItem>
      rows={items}
      rowKey={(row, index) => (row.dataset ?? "dataset") + "-" + (row.source ?? "source") + "-" + index}
      empty={<EmptyState title="没有 Coverage 记录" />}
      columns={[
        { key: "dataset", header: "dataset", width: "160px", render: (row) => <span className="mono">{row.dataset || "—"}</span> },
        { key: "from", header: "from_block", width: "110px", render: (row) => <span className="mono">{blockText(row.from_block)}</span> },
        { key: "to", header: "to_block", width: "110px", render: (row) => <span className="mono">{blockText(row.to_block)}</span> },
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
          render: (row) => <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status || "—"}</StatusBadge>,
        },
        {
          key: "source",
          header: "source",
          width: "200px",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }}>
              {row.source || "—"}
            </span>
          ),
        },
        { key: "updated", header: "updated_at", width: "165px", render: (row) => <span>{formatTime(row.updated_at)}</span> },
      ]}
    />
  );
}

