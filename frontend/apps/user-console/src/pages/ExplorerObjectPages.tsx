import { useEffect, useRef } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { Button, Card, Descriptions, Empty, Space, Table, Tabs } from "antd";
import { HashValue, useDialog } from "@chain/ui";
import { formatRawAmount, formatTime, useApi } from "../sprint5/common";
import { ChainIcon, TokenIcon } from "./explorer/AssetIcon";
import { ExplorerSearch } from "./explorer/ExplorerSearch";
import { EntityHash } from "./address-detail/tabs/AddressTables";

interface TxRow { tx_hash: string; block_number: string | number; block_hash: string; timestamp: string; from_address: string; to_address: string; value_raw: string; token_decimals?: number; amount_raw?: string; token_symbol?: string; log_index?: number }
function useQueryError(error: string | null, title: string) { const dialog = useDialog(); const last = useRef(""); useEffect(() => { if (error && error !== last.current) { last.current = error; dialog.notify("error", title, error); } }, [error, title, dialog]); }

function TransactionRows({ rows, chain, loading, token = false }: { rows: TxRow[]; chain: string; loading: boolean; token?: boolean }) {
  return <Table<TxRow> size="small" rowKey={row => `${row.tx_hash}-${row.log_index ?? ""}`} dataSource={rows} loading={loading} pagination={false} scroll={{ x: 940 }} columns={[
    { title: "交易哈希", key: "hash", render: (_, row) => <EntityHash value={row.tx_hash} to={`/transactions/${encodeURIComponent(chain)}/${encodeURIComponent(row.tx_hash)}`} label="交易哈希" /> },
    { title: "区块", key: "block", render: (_, row) => <Link to={`/blocks/${encodeURIComponent(chain)}/${row.block_number}`}>{row.block_number}</Link> },
    { title: "时间", key: "time", render: (_, row) => formatTime(row.timestamp) },
    { title: "发送方", key: "from", render: (_, row) => <EntityHash value={row.from_address} to={`/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(row.from_address)}`} label="发送方" /> },
    { title: "接收方", key: "to", render: (_, row) => <EntityHash value={row.to_address} to={`/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(row.to_address)}`} label="接收方" /> },
    { title: token ? "金额" : "原始金额", key: "amount", render: (_, row) => token ? `${formatRawAmount(row.amount_raw, row.token_decimals)} ${row.token_symbol || ""}` : row.value_raw || "—" },
  ]} />;
}

export function BlockDetailPage() {
  const { chain = "", block = "" } = useParams(); const [params, setParams] = useSearchParams(); const dialog = useDialog();
  const api = useApi<{ summary: { block_number?: string | number; block_hash?: string; timestamp?: string }; items: TxRow[]; source: string }>(`/api/v2/explorer/blocks/${encodeURIComponent(chain)}/${encodeURIComponent(block)}`, [], { lookup: "auto", limit: 50, cursor: params.get("cursor") || undefined });
  useQueryError(api.error, "区块查询失败");
  const summary = api.data?.summary; const height = summary?.block_number === undefined ? null : BigInt(summary.block_number);
  return <div className="cs-explorer-object-page"><ExplorerSearch initialChain={chain} />
    <div className="cs-query-breadcrumb"><Link to="/explorer">区块链查询</Link><span>/</span><span>区块详情</span></div>
    <Card><div className="cs-query-object-title"><ChainIcon chain={chain} size={36} /><h1>区块 {summary?.block_number ?? block}</h1></div>
      <Descriptions column={{ xs: 1, sm: 2 }} items={[
        { key: "hash", label: "区块哈希", span: 2, children: summary?.block_hash ? <HashValue value={summary.block_hash} label="区块哈希" wrap /> : "—" },
        { key: "time", label: "时间", children: formatTime(summary?.timestamp) }, { key: "source", label: "数据来源", children: api.data?.source === "rpc" ? "链上 RPC" : "本地交易索引" },
      ]} />
      <Space>{height !== null && height > 0n && <Link to={`/blocks/${chain}/${height - 1n}`}>上一块</Link>}{height !== null && <Link to={`/blocks/${chain}/${height + 1n}`}>下一块</Link>}
        <Button type="link" onClick={() => dialog.notify("info", "区块数据范围", "区块字段及交易列表来自已经入库的交易记录。当前接口未保存完整区块头和全块交易清单。", [{ label: "链", value: chain }, { label: "查询", value: block }])}>数据范围</Button></Space>
    </Card>
    <Card title="交易列表"><TransactionRows rows={api.data?.items || []} chain={chain} loading={api.loading} />
      <div className="cs-query-list-pagination"><span>本页 {api.data?.items.length ?? 0} 条</span>{params.has("cursor") && <Button onClick={() => setParams({})}>回到首页</Button>}
        <Button disabled={!api.meta?.next_cursor} onClick={() => setParams({ cursor: api.meta?.next_cursor || "" })}>下一页</Button></div>
    </Card>
  </div>;
}

export function TokenDetailPage() {
  const { chain = "", contract = "" } = useParams(); const [params, setParams] = useSearchParams(); const dialog = useDialog();
  const base = `/api/v2/explorer/tokens/${encodeURIComponent(chain)}/${encodeURIComponent(contract)}`;
  const meta = useApi<{ metadata: { name?: string; symbol?: string; decimals?: number; verified?: boolean; metadata_source?: string; last_resolved_at?: string } | null }>(base);
  const tab = params.get("tab") === "holders" ? "holders" : "transfers";
  const transfers = useApi<{ items: TxRow[] }>(tab === "transfers" ? base + "/transfers" : null, [], { limit: 50, cursor: params.get("cursor") || undefined });
  const holders = useApi<{ items: Array<{ address: string; amount_raw: string; token_decimals: number; observed_at: string }>; limit: number }>(tab === "holders" ? base + "/holders" : null);
  useQueryError(meta.error || transfers.error || holders.error, "代币查询失败");
  const token = meta.data?.metadata;
  return <div className="cs-explorer-object-page"><ExplorerSearch initialChain={chain} />
    <div className="cs-query-breadcrumb"><Link to="/explorer">区块链查询</Link><span>/</span><span>代币详情</span></div>
    <Card><div className="cs-query-object-title"><TokenIcon chain={chain} contract={contract} symbol={token?.symbol} size={36} /><h1>{token?.name || token?.symbol || "代币"}</h1><span>{token?.symbol || ""}</span></div>
      <Descriptions column={{ xs: 1, sm: 2 }} items={[
        { key: "contract", label: "合约地址", span: 2, children: <EntityHash value={contract} to={`/addresses/${chain}/${contract}`} label="代币合约" head={42} tail={0} /> },
        { key: "chain", label: "区块链", children: <Space><ChainIcon chain={chain} />{chain.toUpperCase()}</Space> }, { key: "decimals", label: "精度", children: token?.decimals ?? "—" },
        { key: "verified", label: "元数据核验", children: token?.verified ? "已核验" : "未核验" }, { key: "source", label: "元数据来源", children: token?.metadata_source || "—" },
        { key: "time", label: "更新时间", children: formatTime(token?.last_resolved_at) },
      ]} />
    </Card>
    <Card><Tabs activeKey={tab} onChange={value => setParams({ tab: value })} items={[{ key: "transfers", label: "代币转账" }, { key: "holders", label: "本地持仓" }]} />
      {tab === "transfers" ? <><TransactionRows rows={transfers.data?.items || []} chain={chain} loading={transfers.loading} token />
        <div className="cs-query-list-pagination"><span>本页 {transfers.data?.items.length ?? 0} 条</span>{params.has("cursor") && <Button onClick={() => setParams({ tab })}>回到首页</Button>}
          <Button disabled={!transfers.meta?.next_cursor} onClick={() => setParams({ tab, cursor: transfers.meta?.next_cursor || "" })}>下一页</Button></div></> : <>
        <Button type="link" onClick={() => dialog.notify("info", "本地持仓范围", "按本地余额快照中每个地址最新的记录排序，最多展示前 200 个地址。", [{ label: "数据来源", value: "本地余额快照" }])}>数据范围</Button>
        <Table rowKey="address" dataSource={holders.data?.items || []} loading={holders.loading} pagination={{ pageSize: 20 }} scroll={{ x: 650 }} columns={[
          { title: "地址", dataIndex: "address", render: value => <EntityHash value={value} to={`/addresses/${chain}/${value}`} label="持仓地址" /> },
          { title: "金额", key: "amount", render: (_, row) => formatRawAmount(row.amount_raw, row.token_decimals) }, { title: "快照时间", dataIndex: "observed_at", render: value => formatTime(value) },
        ]} />
      </>}
    </Card>
  </div>;
}
