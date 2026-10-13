import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Button, Card, Collapse, DatePicker, Form, Input, Radio, Select, Skeleton, Space, Table, Tabs, Tag } from "antd";
import dayjs from "dayjs";
import { Link, useSearchParams } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { HashValue, useDialog } from "@chain/ui";
import { formatRawAmount, formatTime, useApi } from "../../sprint5/common";
import { datasetLabel } from "../../app/business-labels";
import { TokenIcon } from "./AssetIcon";

const FlowChart = lazy(() => import("./FlowChart"));
export interface FlowTotal { dataset: string; token_address: string; token_symbol: string; token_decimals: number; in_raw: string; out_raw: string; net_raw: string; internal_raw: string; self_raw: string; event_count: number; transaction_count: number; missing_amount_rows: number; failed_event_count: number; first_seen: string | null; last_seen: string | null }
export interface StatisticsGroup { key: string; total: FlowTotal; counterparty?: string; address?: string; bucket?: string; common_addresses?: string[]; share_percent?: string }
interface StatisticsData {
  statistics: { filter: { datasets: string[]; as_of: string; interval: string; timezone: string }; data_version: string; transaction_count: number; event_count: number; counterparty_count: number; first_seen: string | null; last_seen: string | null; assets: StatisticsGroup[]; trend: StatisticsGroup[]; hours: StatisticsGroup[]; distribution: StatisticsGroup[]; counterparties: StatisticsGroup[]; addresses: StatisticsGroup[]; top_transfers: Array<{ dataset: string; token_address: string; token_symbol: string; token_decimals: number; tx_hash: string; event_index: number; timestamp: string; from_address: string; to_address: string; amount_raw: string }> };
  source: { provider: string; as_of: string; data_version: string };
  coverage: Array<{ address: string; coverage: { items?: Array<{ dataset?: string; from_timestamp?: string; to_timestamp?: string; status?: string; complete_through?: string }> } }>;
  calculation: Record<string, string>;
}

export default function StatisticsPanel({ chain, address, initialView = "counterparties" }: { chain: string; address: string; initialView?: string }) {
  const [params, setParams] = useSearchParams(); const dialog = useDialog(); const { can } = useAuth();
  const [view, setView] = useState(initialView); const [selectedAsset, setSelectedAsset] = useState(""); const [sort, setSort] = useState("event_count"); const [groupDraft, setGroupDraft] = useState(params.get("addresses") || "");
  const [counterpartyDraft, setCounterpartyDraft] = useState(params.get("counterparty") || ""); const [tokenDraft, setTokenDraft] = useState(params.get("token") || "");
  const [minDraft, setMinDraft] = useState(params.get("min_amount") || ""); const [maxDraft, setMaxDraft] = useState(params.get("max_amount") || "");
  const notified = useRef("");
  const anchor = useMemo(() => new Date().toISOString(), [chain, address, params.get("snapshot")]);
  const fromTime = params.get("from_time") || new Date(new Date(anchor).getTime() - 30 * 86400000).toISOString();
  const toTime = params.get("to_time") || anchor;
  const datasets = params.get("datasets") || "token_transfers";
  const api = useApi<StatisticsData>(`/api/v2/explorer/addresses/${encodeURIComponent(chain)}/${encodeURIComponent(address)}/statistics`, [], {
    from_time: fromTime, to_time: toTime, as_of: params.get("as_of") || anchor, datasets,
    token: params.get("token") || undefined, direction: params.get("direction") || undefined, counterparty: params.get("counterparty") || undefined,
    min_amount: params.get("min_amount") || undefined, max_amount: params.get("max_amount") || undefined,
    addresses: params.get("addresses") || undefined, interval: params.get("interval") || "day", timezone: params.get("timezone") || "Asia/Shanghai",
  });
  useEffect(() => { if (api.error && api.error !== notified.current) { notified.current = api.error; dialog.notify("error", "统计查询失败", api.error); } }, [api.error, dialog]);
  const update = (changes: Record<string, string | null>) => { const next = new URLSearchParams(params); Object.entries(changes).forEach(([key, value]) => { if (value) next.set(key, value); else next.delete(key); }); next.delete("cursor"); setParams(next); };
  const stats = api.data?.statistics;
  const assets = stats?.assets || [];
  const asset = assets.find(item => item.key === selectedAsset) || assets[0];
  const match = (row: StatisticsGroup) => !asset || (row.total.dataset === asset.total.dataset && row.total.token_address === asset.total.token_address && row.total.token_decimals === asset.total.token_decimals);
  const amount = (row: StatisticsGroup, key: "in_raw" | "out_raw" | "net_raw" | "internal_raw" | "self_raw") => <span className="mono">{formatRawAmount(row.total[key], row.total.token_decimals)}</span>;
  const ranking = [...(stats?.counterparties || []).filter(match)].sort((a, b) => {
    if (sort === "event_count") return b.total.event_count - a.total.event_count;
    const av = BigInt(a.total[sort as "in_raw"] || "0"); const bv = BigInt(b.total[sort as "in_raw"] || "0"); return av === bv ? (a.counterparty || "").localeCompare(b.counterparty || "") : av < bv ? 1 : -1;
  });
  const points = useMemo(() => (stats?.trend || []).filter(match).sort((a, b) => (a.bucket || "").localeCompare(b.bucket || "")).map(row => ({ bucket: row.bucket || "", in_raw: row.total.in_raw, out_raw: row.total.out_raw, decimals: row.total.token_decimals })), [stats, asset?.key]);
  const drill = (changes: Record<string, string | null>) => update({ tab: datasets === "transactions" ? "transactions" : datasets === "internal_transactions" ? "internal" : "transfers", from_time: fromTime, to_time: toTime, as_of: stats?.filter.as_of || anchor, token: asset?.total.token_address || params.get("token"), ...changes });
  const exportLink = (content: string) => { const next = new URLSearchParams({ chain, address, datasets, content, from_time: fromTime, to_time: toTime, as_of: stats?.filter.as_of || anchor }); ["direction", "counterparty", "min_amount", "max_amount", "addresses", "timezone", "interval", "token", "from_address", "to_address", "hide_zero"].forEach(key => { const value = params.get(key); if (value) next.set(key, value); }); return `/exports?${next}`; };
  const explain = () => dialog.notify("info", "统计计算与数据覆盖", null, [
    { label: "数据范围", value: `${fromTime} → ${toTime}` }, { label: "数据版本", value: stats?.data_version || "—" },
    { label: "金额计算", value: "原始整数逐笔计算；不同链、合约和精度分别汇总。失败记录不计入资金金额，缺失金额单独计数。" },
    { label: "组内与自转", value: "组对外收入、支出排除组内往来和自转；分地址统计包含该地址与组内成员之间的往来。" },
    { label: "对手方占比", value: "当前数据集、合约和精度下，该对手方事件数占全部已筛选事件数的比例。" },
    ...(api.data?.coverage || []).map(row => ({ label: row.address, value: JSON.stringify(row.coverage) })),
  ]);
  const columns = [
    { title: "对手方", key: "counterparty", render: (_: unknown, row: StatisticsGroup) => <HashValue value={row.counterparty || row.address || ""} label="地址" /> },
    { title: "收入", key: "in", render: (_: unknown, row: StatisticsGroup) => amount(row, "in_raw") },
    { title: "支出", key: "out", render: (_: unknown, row: StatisticsGroup) => amount(row, "out_raw") },
    { title: "净流入", key: "net", render: (_: unknown, row: StatisticsGroup) => amount(row, "net_raw") },
    { title: "事件数", key: "events", render: (_: unknown, row: StatisticsGroup) => row.total.event_count },
    { title: "占比", key: "share", render: (_: unknown, row: StatisticsGroup) => row.share_percent ? `${row.share_percent}%` : "—" },
    { title: "操作", key: "actions", render: (_: unknown, row: StatisticsGroup) => <Button type="link" size="small" onClick={() => drill({ counterparty: row.counterparty || null })}>查看交易</Button> },
  ];
  return <div className="cs-statistics-panel">
    <Card><Form layout="inline" className="cs-query-filter-form">
      <Form.Item label="数据集"><Select value={datasets} onChange={value => update({ datasets: value })} options={["token_transfers", "transactions", "internal_transactions"].map(value => ({ value, label: datasetLabel(value) }))} /></Form.Item>
      <Form.Item label="时间"><DatePicker.RangePicker showTime format="YYYY-MM-DD HH:mm:ss" value={[dayjs(fromTime), dayjs(toTime)]} onChange={value => { if (value?.[0] && value[1]) update({ from_time: value[0].toISOString(), to_time: value[1].toISOString() }); }} /></Form.Item>
      <Form.Item label="方向"><Select value={params.get("direction") || ""} onChange={value => update({ direction: value })} options={[{ value: "", label: "全部方向" }, { value: "in", label: "收入" }, { value: "out", label: "支出" }, { value: "internal", label: "组内往来" }, { value: "self", label: "自转" }]} /></Form.Item>
      <Form.Item label="币种"><Input.Search aria-label="统计币种" value={tokenDraft} placeholder="代币合约 / 符号" onChange={event => setTokenDraft(event.target.value)} onSearch={value => update({ token: value.trim() })} /></Form.Item>
      <Button type="link" onClick={explain}>计算与覆盖</Button><Button onClick={() => update({ snapshot: String(Date.now()), as_of: null })} loading={api.loading}>刷新</Button>
    </Form><Collapse ghost className="cs-statistics-advanced" items={[{key:"advanced",label:"更多筛选",children:<Form layout="inline" className="cs-query-filter-form">      <Form.Item label="对手方"><Input.Search value={counterpartyDraft} placeholder="完整地址" onChange={event => setCounterpartyDraft(event.target.value)} onSearch={value => update({ counterparty: value.trim() })} /></Form.Item>
      <Form.Item label="金额"><Space.Compact><Input aria-label="最小金额" placeholder="最小金额" value={minDraft} onChange={event => setMinDraft(event.target.value)} /><Input aria-label="最大金额" placeholder="最大金额" value={maxDraft} onChange={event => setMaxDraft(event.target.value)} /><Button onClick={() => update({ min_amount: minDraft, max_amount: maxDraft })}>应用</Button></Space.Compact></Form.Item>
      <Form.Item label="地址组"><Input.Search value={groupDraft} placeholder="多个地址用逗号分隔" onChange={event => setGroupDraft(event.target.value)} onSearch={value => update({ addresses: value.trim() })} /></Form.Item>
</Form>}]} /></Card>
    {api.loading && !stats ? <Card><Skeleton active /></Card> : !stats ? <Card><Button onClick={() => void api.reload()}>重新加载</Button></Card> : <>
      <div className="cs-statistics-summary">
        {[{ label: "交易笔数", value: stats.transaction_count }, { label: "事件数", value: stats.event_count }, { label: "去重对手方", value: stats.counterparty_count }, { label: "首次活动", value: formatTime(stats.first_seen) }, { label: "末次活动", value: formatTime(stats.last_seen) }].map(item => <div key={item.label}><span>{item.label}</span><strong>{item.value}</strong></div>)}
      </div>
      <Card title="资金趋势" extra={<Space><Select aria-label="统计资产" value={asset?.key} onChange={setSelectedAsset} options={assets.map(item => ({ value: item.key, label: <Space><TokenIcon chain={chain} contract={item.total.token_address} symbol={item.total.token_symbol} />{item.total.token_symbol || "未知资产"} · {datasetLabel(item.total.dataset)}</Space> }))} />
        <Radio.Group value={stats.filter.interval} onChange={event => update({ interval: event.target.value })}><Radio.Button value="day">日</Radio.Button><Radio.Button value="hour">小时</Radio.Button></Radio.Group></Space>}>
        {asset && <div className="cs-flow-totals">{([['收入','in_raw'],['支出','out_raw'],['净流入','net_raw'],['组内往来','internal_raw'],['自转','self_raw']] as const).map(([label, key]) => <div key={key}><span>{label}</span><strong>{amount(asset, key)}</strong></div>)}
          {!!asset.total.missing_amount_rows && <Button type="link" onClick={explain}>缺失金额 {asset.total.missing_amount_rows} 条</Button>}</div>}
        <Suspense fallback={<Skeleton active />}><FlowChart points={points} onSelect={bucket => { const start = dayjs(bucket); if (start.isValid()) drill({ from_time: start.toISOString(), to_time: start.add(stats.filter.interval === "hour" ? 1 : 24, "hour").subtract(1, "millisecond").toISOString() }); }} /></Suspense>
        <div className="cs-query-data-meta"><span>汇总时区 {stats.filter.timezone}</span><span>本地索引 · {formatTime(api.data?.source.as_of)}</span><Button type="link" size="small" onClick={explain}>数据覆盖 {api.data?.coverage.length} 个地址</Button></div>
      </Card>
      <Card className="cs-statistics-detail" extra={can("export:create") ? <Link to={exportLink(view === "counterparties" ? "counterparties" : "statistics")}>导出当前结果</Link> : undefined}>
        <Tabs activeKey={view} onChange={setView} items={[{ key: "counterparties", label: "对手方排行" }, { key: "top", label: "大额转账" }, { key: "distribution", label: "金额分布" }, { key: "hours", label: "活跃时段" }, { key: "addresses", label: "分地址统计" }, { key: "bidirectional", label: "双向往来" }, { key: "common", label: "共同对手方" }]} />
        {view === "counterparties" || view === "common" || view === "bidirectional" ? <><Space className="cs-ranking-controls"><span>排序</span><Select value={sort} onChange={setSort} options={[{ value: "event_count", label: "事件数" }, { value: "in_raw", label: "收入" }, { value: "out_raw", label: "支出" }, { value: "net_raw", label: "净流入" }]} /></Space><Table<StatisticsGroup> rowKey="key" size="small" dataSource={view === "common" ? ranking.filter(row => (row.common_addresses?.length || 0) > 1) : view === "bidirectional" ? ranking.filter(row => BigInt(row.total.in_raw)>0n && BigInt(row.total.out_raw)>0n) : ranking} columns={columns} pagination={{ pageSize: 20, showSizeChanger: false }} scroll={{ x: 900 }} /></> : view === "top" ? <Table rowKey={row => `${row.dataset}-${row.tx_hash}-${row.event_index}-${row.token_address}`} size="small" dataSource={stats.top_transfers.filter(row => !asset || (row.dataset === asset.total.dataset && row.token_address === asset.total.token_address && row.token_decimals === asset.total.token_decimals))} pagination={{ pageSize: 20 }} scroll={{ x: 900 }} columns={[
          { title: "交易哈希", key: "hash", render: (_, row) => <Link to={`/transactions/${chain}/${row.tx_hash}`}><HashValue value={row.tx_hash} label="交易哈希" /></Link> }, { title: "时间", dataIndex: "timestamp", render: value => formatTime(value) }, { title: "发送方", dataIndex: "from_address", render: value => <HashValue value={value} label="发送方" /> }, { title: "接收方", dataIndex: "to_address", render: value => <HashValue value={value} label="接收方" /> }, { title: "金额", key: "amount", render: (_, row) => formatRawAmount(/^0x[0-9a-f]+$/i.test(row.amount_raw) ? BigInt(row.amount_raw).toString() : row.amount_raw, row.token_decimals) },
        ]} /> : <Table<StatisticsGroup> size="small" rowKey="key" dataSource={(view === "addresses" ? stats.addresses : view === "hours" ? stats.hours : stats.distribution).filter(match)} scroll={{ x: 760 }} pagination={{ pageSize: 24 }} columns={view === "addresses" ? columns.filter(column => column.key !== "share" && column.key !== "actions") : [{ title: view === "hours" ? "小时" : "金额区间", dataIndex: "bucket" }, ...columns.filter(column => !["counterparty", "share", "actions"].includes(column.key))]} />}
      </Card>
    </>}
  </div>;
}
