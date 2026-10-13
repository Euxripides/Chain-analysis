import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ArrowDownWideNarrow, ArrowLeftRight, Box, CircleHelp, Coins, Copy, Search, Trash2, Wallet } from "lucide-react";
import { Card, EmptyState, useDialog } from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { ChainIcon } from "./explorer/AssetIcon";
import { ExplorerSearch } from "./explorer/ExplorerSearch";
import { useExplorerLookup } from "./explorer/useExplorerLookup";
import { formatTime, useApi } from "../sprint5/common";
import "./explorer/explorer-entry.css";

interface ChainItem { slug: string; name: string; enabled: boolean; }
interface RecentItem {
  chain: string;
  value: string;
  kind: "address" | "transaction" | "block" | "token";
  at: string;
}

const RECENT_KEY = "chainsight.explorer.recent.v1";
const MAX_RECENT = 8;
const KIND_LABELS = { address: "地址", transaction: "交易", block: "区块", token: "代币" };
const KIND_ICONS = { address: Wallet, transaction: ArrowLeftRight, block: Box, token: Coins };
const KIND_ROUTES = { address: "addresses", transaction: "transactions", block: "blocks", token: "tokens" };
const FILTERS = ["all", "address", "transaction", "block", "token"] as const;

function readRecent(): RecentItem[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(RECENT_KEY) ?? "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is RecentItem => Boolean(item) && typeof item === "object" &&
      typeof item.chain === "string" && typeof item.value === "string" &&
      ["address", "transaction", "block", "token"].includes(item.kind)).slice(0, MAX_RECENT);
  } catch { return []; }
}

function writeRecent(items: RecentItem[]) {
  try { window.localStorage.setItem(RECENT_KEY, JSON.stringify(items.slice(0, MAX_RECENT))); }
  catch { /* 存储不可用时，查询和跳转仍然可用。 */ }
}

/** 页面内筛选仅处理本机记录；查询识别、跳转和存储键沿用现有实现。 */
export default function ExplorerPage() {
  const dialog = useDialog();
  const { data, loading, error, reload } = useApi<{ items: ChainItem[] }>("/api/v2/meta/chains");
  const [recent, setRecent] = useState<RecentItem[]>(readRecent);
  const [filter, setFilter] = useState<(typeof FILTERS)[number]>("all");
  const lastErrorRef = useRef("");

  useEffect(() => {
    if (!error) { lastErrorRef.current = ""; return; }
    if (lastErrorRef.current === error) return;
    lastErrorRef.current = error;
    dialog.notify("error", "链列表加载失败", error);
  }, [error, dialog]);

  const remember = (item: RecentItem) => {
    setRecent(previous => {
      const next = [item, ...previous.filter(entry => !(entry.chain === item.chain && entry.value === item.value))].slice(0, MAX_RECENT);
      writeRecent(next);
      return next;
    });
  };

  const chainName = (slug: string) => data?.items.find(item => item.slug === slug)?.name ?? slug;
  const lookup = useExplorerLookup((chain, value, kind) => remember({ chain, value, kind, at: new Date().toISOString() }));

  const removeOne = async (target: RecentItem) => {
    const answer = await dialog.confirm({
      kind: "confirm", title: "删除这条查询记录？", body: "删除后，这条记录将从最近查询中移除。",
      details: [{ label: "查询内容", value: target.value }, { label: "区块链网络", value: chainName(target.chain) }],
    });
    if (answer !== "confirm") return;
    setRecent(previous => {
      const next = previous.filter(entry => !(entry.chain === target.chain && entry.value === target.value));
      writeRecent(next);
      return next;
    });
  };

  const clearAll = async () => {
    if (!recent.length) return;
    const answer = await dialog.confirm({
      kind: "confirm", title: "清空最近查询？", body: "清空后本机不再保留这些查询记录。",
      details: [{ label: "条数", value: String(recent.length) }],
    });
    if (answer !== "confirm") return;
    setRecent([]); writeRecent([]);
  };

  const explain = () => dialog.notify("info", "查询说明",
    "选择网络后输入地址、交易哈希、区块高度或代币，系统自动识别。按 Enter 查询，按 / 定位输入框。先校验输入并检查本地数据，本地未命中才探测链上记录。历史完整且尾部未更新不足七天时直接展示；补全先确认再创建下载任务。哈希通过 RPC 展示单笔详情。",
    [{ label: "本机历史记录", value: `最多 ${MAX_RECENT} 条；同一内容在不同网络中分别保存` }]);

  const copy = async (value: string) => {
    try { await navigator.clipboard.writeText(value); dialog.notify("success", "已复制查询内容", value); }
    catch { dialog.notify("error", "复制失败", "请选中查询内容手动复制。", [{ label: "查询内容", value }]); }
  };

  const visible = recent.filter(entry => filter === "all" || entry.kind === filter);
  const countOf = (kind: (typeof FILTERS)[number]) => kind === "all" ? recent.length : recent.filter(entry => entry.kind === kind).length;

  return <div className="cs-explorer-page">
    <CsPageHeader title="区块链查询" breadcrumb={[{ label: "工作台", to: "/home" }, { label: "区块链查询" }]}
      secondaryAction={<button type="button" className="cs-entry-help" aria-label="查询说明" onClick={explain}><CircleHelp size={17} aria-hidden="true" /><span>查询帮助</span></button>} />
    <section className="cs-entry-query" aria-labelledby="cs-entry-query-title">
      <header className="cs-entry-query__head"><span className="cs-entry-query__mark"><Search size={19} aria-hidden="true" /></span><h2 id="cs-entry-query-title">查询条件</h2></header>
      <ExplorerSearch variant="entry" chains={data?.items ?? []} chainsLoading={loading} lookupEngine={lookup}
        onOpen={(chain, value, kind) => remember({ chain, value, kind, at: new Date().toISOString() })} />
      {error ? <button type="button" className="cs-entry-reload" onClick={() => void reload()}>重新加载网络</button> : null}
    </section>
    <section className="cs-entry-history" aria-labelledby="cs-entry-history-title">
      <header className="cs-entry-history__head"><div><h2 id="cs-entry-history-title">最近查询</h2><span className="cs-entry-count">{recent.length} 条记录</span></div>
        <button type="button" className="cs-entry-clear" disabled={!recent.length} onClick={() => void clearAll()}><Trash2 size={16} aria-hidden="true" />清空记录</button>
      </header>
      <div className="cs-entry-history__panel">
        <div className="cs-entry-history__tools"><div className="cs-entry-filters" role="group" aria-label="筛选查询类型">
          {FILTERS.filter(kind => kind === "all" || kind === "address" || kind === "transaction" || countOf(kind) > 0 || filter === kind).map(kind =>
            <button type="button" key={kind} className={`cs-entry-filter${filter === kind ? " is-active" : ""}`} aria-pressed={filter === kind} onClick={() => setFilter(kind)}>{kind === "all" ? "全部" : KIND_LABELS[kind]}<span>{countOf(kind)}</span></button>)}
        </div><span className="cs-entry-order"><ArrowDownWideNarrow size={15} aria-hidden="true" />最近查询优先</span></div>
        {visible.length ? <table className="cs-entry-table" aria-label="最近查询记录"><thead><tr><th scope="col">查询内容</th><th scope="col">类型</th><th scope="col">区块链网络</th><th scope="col">查询时间</th><th scope="col">操作</th></tr></thead>
          <tbody>{visible.map(entry => {
            const Icon = KIND_ICONS[entry.kind];
            return <tr key={`${entry.chain}-${entry.value}`}>
              <td className="cs-entry-table__content"><div className="cs-entry-record"><span className={`cs-entry-record__icon is-${entry.kind}`}><Icon size={19} aria-hidden="true" /></span><Link className="cs-entry-record__value" to={`/${KIND_ROUTES[entry.kind]}/${encodeURIComponent(entry.chain)}/${encodeURIComponent(entry.value)}`} title={entry.value} aria-label={`查看${KIND_LABELS[entry.kind]} ${entry.value}`}>{entry.value}</Link><button type="button" className="cs-entry-copy" aria-label={`复制${KIND_LABELS[entry.kind]} ${entry.value}`} onClick={() => void copy(entry.value)}><Copy size={15} aria-hidden="true" /></button></div></td>
              <td className="cs-entry-table__kind"><span className={`cs-entry-badge is-${entry.kind}`}>{KIND_LABELS[entry.kind]}</span></td>
              <td className="cs-entry-table__network"><span className="cs-entry-network"><ChainIcon chain={entry.chain} size={23} />{chainName(entry.chain)}</span></td>
              <td className="cs-entry-table__time"><time dateTime={entry.at}>{formatTime(entry.at)}</time></td>
              <td className="cs-entry-table__actions"><div><button type="button" className="cs-entry-delete" aria-label="删除该条最近查询" title="删除记录" onClick={() => void removeOne(entry)}><Trash2 size={16} aria-hidden="true" /></button></div></td>
            </tr>;
          })}</tbody></table> : <Card flush><EmptyState title={recent.length ? "暂无此类查询记录" : "暂无最近查询"} /></Card>}
        <footer className="cs-entry-history__foot"><span>显示 {visible.length} 条记录</span><span>最多保留 {MAX_RECENT} 条</span></footer>
      </div>
    </section>
  </div>;
}
