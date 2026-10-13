import {
  lazy,
  Suspense,
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import { Link,
  useParams,
  useSearchParams } from "react-router-dom";
import {
  Amount,
  BackLink,
  Button,
  Card,
  CopyButton,
  DataTable,
  EmptyState,
  ErrorState,
  FilterBar,
  FilterChip,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  LinearProgress,
  MetaLine,
  MetricCard,
  PageSection,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import type { KeyValueEntry } from "@chain/ui";
import { useAuth } from "@chain/api-client";
import { formatRawAmount, formatTime, shortValue, useApi } from "../sprint5/common";
import { Button as AntButton, Card as AntCard, DatePicker, Descriptions, Dropdown, Input, QRCode, Select, Space, Tag } from "antd";
import { Copy, QrCode, Download, Share2 } from "lucide-react";
import dayjs from "dayjs";
import { TransferToolbar } from "./address-detail/TransferToolbar";
import "./detail-polish.css";
import { ChainIcon } from "./explorer/AssetIcon";
import { ExplorerSearch } from "./explorer/ExplorerSearch";
const StatisticsPanel = lazy(() => import("./explorer/StatisticsPanel"));

/**
 * U-EXP-02 地址详情。
 * - 只有真实 API：overview / assets / activity / counterparties / semantic / flow-profile。
 * - 普通交易 / Token Transfer / Internal 均来自 activity 真实行，按 activity_type 区分（Internal 为链上内部转账记录）。
 * - 当前余额（is_current_balance=true）与历史净流量（is_current_balance=false）**分开呈现**，各自标注口径来源。
 * - Tab / cursor / 筛选全部写入 URL，刷新可恢复；筛选变化重置 cursor。
 *
 * 2026-09 表现层重构（蓝图 §12.15）：
 *   Header（链 / 完整地址 / 复制 / 数据时间）→ Overview（指标 + 当前余额 + 历史净流量）
 *   → Activity（原 tab + FilterBar）→ Fund Flow（对手方 / 资金流边）→ Analysis（flow-profile）
 *   → Side Inspector（选中活动行或对手方行的详情；未选中时显示数据来源与口径）。
 *   接口路径、请求参数、分页与错误语义一律未改；本文件只重组信息架构。
 */

/* 类型与纯函数已抽到 ./address-detail/address-detail.types.ts（规范 §43 的目标文件之一）。
   本次只做位移：字段名、口径与派生规则一字未改，9 个业务 Tab 的 key 与顺序也保持不变。 */
import {
  DIRECTION_LABEL,
  TABS,
  addressPath,
  amountDirection,
  blockText,
  decimalsOf,
  directionTone,
  extractEvents,
  fiatText,
  hasSemanticResultShape,
  jsonText,
  transactionPath,
} from "./address-detail/address-detail.types";
import type {
  ActivityRow,
  AnalysisData,
  AnalysisEdge,
  Asset,
  AssetsPayload,
  BalanceBlock,
  CounterpartyRow,
  CoverageItem,
  NetFlowAsset,
  OverviewData,
  SemanticEvent,
  SemanticResult,
} from "./address-detail/address-detail.types";

/**
 * 带 SPA 导航的地址 / 交易哈希展示。
 *
 * 为什么不直接用 HashValue 的 href：应用挂在 BrowserRouter basename="/app-v2" 下，
 * 页面里写死 "/addresses/..." 的原生链接会跳出 SPA（原实现正是用 navigate() 拦住它）。
 * 这里复用设计系统同一组类（.cs-hash / .cs-hash__text）与同一个 CopyButton：
 * 视觉省略规则、完整 title、完整复制值与 HashValue 完全一致，只把链接换成 router Link。
 */
import { EntityHash, CurrentBalanceTable, NetFlowTable, ActivityTable, NftTransferTable, CounterpartyTable, SemanticTable, AnalysisView, CoverageTable } from "./address-detail/tabs/AddressTables";

export default function AddressDetailPage() {
  const { chain = "", address = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const dialog = useDialog();
  const { client, can } = useAuth();
  // P1-1 第 3 项：活动是长表路径（实测 8.09s @ limit=200 全量窗口），默认收窄到最近 30 天。
  // 服务端的 Filter 一直支持 from_time/to_time（会写成 WHERE timestamp >=/<=），此前只是前端
  // 从不传、handler 也不透传，所以每次都是全量拉。选「全部」会退回较慢的全量查询。
  const [windowDays, setWindowDays] = useState(Number(searchParams.get("window_days") ?? "30"));
  const assetType = searchParams.get("asset_type") === "nft" ? "nft" : "token";
  const fromAddress = searchParams.get("from_address") ?? "";
  const toAddress = searchParams.get("to_address") ?? "";
  const fromDate = searchParams.get("from_date") ?? "";
  const toDate = searchParams.get("to_date") ?? "";
  const hideZero = searchParams.get("hide_zero") === "1";
  const [refreshNonce, setRefreshNonce] = useState(0);
  useEffect(() => setWindowDays(Number(searchParams.get("window_days") ?? "30")), [searchParams.get("window_days")]);
  const [bulkRows, setBulkRows] = useState<ActivityRow[] | null>(null);
  const [bulkLoading, setBulkLoading] = useState(false);
  // 仅用于展示"已经真实拿到多少行"——服务端不提供总量，因此进度条保持 indeterminate。
  const [bulkLoaded, setBulkLoaded] = useState(0);
  const bulkController = useRef<AbortController | null>(null);
  // 右侧检查器：选中活动行 / 对手方行 → 行详情；未选中 → 数据来源与口径。
  const [selectedActivity, setSelectedActivity] = useState<ActivityRow | null>(null);
  const [selectedCounterparty, setSelectedCounterparty] = useState<CounterpartyRow | null>(null);
  const requestedTab = searchParams.get("tab") ?? "transfers";
  const tab = TABS.some((item) => item.key === requestedTab) ? requestedTab : "transfers";
  const cursor = searchParams.get("cursor") ?? "";
  const direction = searchParams.get("direction") ?? "";
  const token = searchParams.get("token") ?? "";
  const counterparty = searchParams.get("counterparty") ?? "";
  const fromTime = searchParams.get("from_time");
  const toTime = searchParams.get("to_time");
  const [counterpartyDraft, setCounterpartyDraft] = useState(counterparty);
  const minAmount = searchParams.get("min_amount") ?? "";
  const maxAmount = searchParams.get("max_amount") ?? "";
  useEffect(() => {
    bulkController.current?.abort();
    setBulkRows(null);
    setBulkLoaded(0);
    setBulkLoading(false);
    return () => bulkController.current?.abort();
  }, [chain, address, tab, direction, token, windowDays, fromTime, toTime, counterparty, minAmount, maxAmount, assetType, fromAddress, toAddress, fromDate, toDate]);
  const [tokenDraft, setTokenDraft] = useState(token);
  const notifiedErrorsRef = useRef<Set<string>>(new Set());

  const ready = Boolean(chain && address);
  const base = ready
    ? "/api/v2/explorer/addresses/" + encodeURIComponent(chain) + "/" + encodeURIComponent(address)
    : null;

  /**
   * 时间窗锚点必须是**稳定值**：它进入 useApi 的 queryKey。
   * 曾经在这里直接调用 Date.now()：每次渲染都产生新的毫秒级时间戳 → queryKey 每次都变 →
   * load 的 identity 每次渲染都变 → useEffect 反复 abort 上一次请求并重新发起，
   * 请求被无限取消、loading 永远为 true，地址详情的活动表**永远是骨架屏**，
   * 同时把 /activity 打成请求风暴（2026-09-16 实测：一次页面停留里连着 ERR_ABORTED）。
   * 用 useMemo 绑定到 windowDays：窗口不变就不变。
   */
  const activityFromTime = useMemo(
    () => fromTime || (fromDate ? new Date(fromDate + "T00:00:00").toISOString() : undefined) || (windowDays > 0 ? new Date(Date.now() - windowDays * 86_400_000).toISOString() : undefined),
    [windowDays, fromTime, fromDate],
  );
  const activityToTime = toTime || (toDate ? new Date(toDate + "T23:59:59.999").toISOString() : undefined);
  const statisticsActive = tab === "statistics" || tab === "counterparties";
  const overview = useApi<OverviewData>(base && !statisticsActive ? base + "/overview" : null, [chain, address, tab]);
  const assets = useApi<AssetsPayload>(tab === "assets" && base ? base + "/assets" : null, [chain, address, tab]);
  const activityActive = tab === "transactions" || tab === "transfers" || tab === "internal";
  const activity = useApi<{ items: ActivityRow[] }>(
    activityActive && base ? base + "/activity" : null,
    [chain, address, tab, cursor, direction, token, windowDays, assetType, fromAddress, toAddress, refreshNonce],
    activityActive
      ? {
          limit: 200,
          dataset: tab === "transfers" ? "token_transfers" : tab === "internal" ? "internal_transactions" : "transactions",
          light: 1,
          asset_type: tab === "transfers" && assetType === "nft" ? "nft" : undefined,
          from_address: fromAddress || undefined,
          to_address: toAddress || undefined,
          cursor: cursor || undefined,
          direction: direction || undefined,
          token: tab === "transfers" ? token || undefined : undefined,
          from_time: activityFromTime,
          to_time: activityToTime,
          counterparty: counterparty || undefined,
          min_amount: minAmount || undefined,
          max_amount: maxAmount || undefined,
          as_of: searchParams.get("as_of") || undefined,
          addresses: searchParams.get("addresses") || undefined,
        }
      : undefined,
  );
  const semantic = useApi<{ items: unknown }>(
    tab === "semantic" && base ? base + "/semantic" : null,
    [chain, address, tab],
  );
  const analysis = useApi<{ items: AnalysisData }>(
    tab === "analysis" && base ? base + "/flow-profile" : null,
    [chain, address, tab],
  );

  const errors = [
    overview.error,
    assets.error,
    activity.error,
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
      dialog.notify("success", "已复制", label + " 已写入剪贴板。");
    } catch {
      dialog.notify("warning", "复制失败", "浏览器拒绝了剪贴板访问，请手动选中并复制。");
    }
  };

  const explainInternal = () => {
    dialog.notify(
      "info",
      "内部转账数据说明",
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
  const fullAddress = subject?.normalized_address || subject?.address || address;

  /** 数据来源与口径：Header 元信息与右侧检查器共用同一份服务端字段。 */
  const sourceItems: KeyValueEntry[] = [
    { label: "数据来源", value: overview.data?.source?.provider || "—" },
    { label: "as_of", value: formatTime(overview.data?.source?.as_of) },
    { label: "数据表", value: (overview.data?.source?.tables ?? []).join("，") || "—", wide: true },
    { label: "数据覆盖", value: coverageItems.length + " 个 dataset" },
    {
      label: "余额口径",
      value: (currentBlock?.source || "—") + " · is_current_balance=" + String(currentBlock?.is_current_balance ?? true),
    },
    {
      label: "净流口径",
      value: (flowBlock?.source || "—") + " · is_current_balance=" + String(flowBlock?.is_current_balance ?? false),
    },
    { label: "数据覆盖 来源", value: overview.data?.coverage?.source || "—", wide: true },
    { label: "规范化地址", value: subject?.normalized_address ? <HashValue value={subject.normalized_address} label="规范化地址" wrap /> : "—", wide: true },
  ];

  // UX-004：显式加载最多 10,000 行真实数据，验证客户端虚拟滚动而非渲染 10k DOM 行。
  const loadAllActivity = async () => {
    if (!base || bulkLoading) return;
    const answer = await dialog.confirm({
      title: "批量加载活动记录？",
      body: "按当前筛选条件顺序加载，最多 10,000 行。超过上限的记录仍需分页查看或创建数据下载。",
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
    setBulkLoaded(0);
    const controller = new AbortController();
    bulkController.current = controller;
    const collected: ActivityRow[] = [];
    let cursorValue = "";
    let hasMore = false;
    try {
      for (let page = 0; page < 50 && collected.length < 10000; page += 1) {
        const response = await client.get<{ items: ActivityRow[] }>(base + "/activity", {
          signal: controller.signal,
          query: {
            limit: 200,
            light: 1,
            asset_type: tab === "transfers" && assetType === "nft" ? "nft" : undefined,
            from_address: fromAddress || undefined,
            to_address: toAddress || undefined,
            dataset: tab === "transfers" ? "token_transfers" : tab === "internal" ? "internal_transactions" : "transactions",
            cursor: cursorValue || undefined,
            direction: direction || undefined,
            token: token || undefined,
            from_time: activityFromTime,
            to_time: activityToTime,
            counterparty: counterparty || undefined,
            min_amount: minAmount || undefined,
            max_amount: maxAmount || undefined,
            as_of: searchParams.get("as_of") || undefined,
            addresses: searchParams.get("addresses") || undefined,
            // 全量视图跳过逐行元数据富化，仅保留本地索引原始列，避免 50 页被 Contract/Case 查询拖慢。
            full: 1,
          },
        });
        if (controller.signal.aborted) return;
        const items = response.data?.items ?? [];
        collected.push(...items);
        setBulkLoaded(collected.length);
        const next = response.meta.next_cursor ?? "";
        hasMore = Boolean(next);
        if (!next || items.length === 0) break;
        cursorValue = next;
      }
      setBulkRows(collected.slice(0, 10000));
      dialog.notify("success", "批量活动记录已加载", `已加载 ${Math.min(collected.length, 10000)} 行。${hasMore ? (collected.length >= 10000 ? "已达到 10,000 行上限，后面仍有记录。" : "服务端仍返回后续游标，可继续分页查看。") : "当前筛选条件下的记录已加载完毕。"}`);
    } catch (error) {
      if (!controller.signal.aborted) dialog.notify("error", "批量加载失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      if (bulkController.current === controller) { bulkController.current = null; setBulkLoading(false); }
    }
  };

  const renderOverview = () => {
    if (overview.loading && !overview.data) {
      return (
        <PageSection title="概览">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!overview.data) {
      return (
        <PageSection title="概览">
          <Card flush>
            <ErrorState title="概览数据不可用" message="加载失败，请重试。" onRetry={() => void overview.reload()} />
          </Card>
        </PageSection>
      );
    }
    return (
      <>
        <PageSection title="概览指标" count="5 项">
          <div className="metric-grid cs-stagger">
            <MetricCard
              label="交易数"
              value={summary?.transaction_count ?? "—"}
              source="summary.transaction_count"
              sourceNote="来自服务端地址概览接口的 summary.transaction_count：该地址作为参与方的普通交易计数，范围受当前链与本地索引覆盖限制。"
            />
            <MetricCard
              label="代币转账"
              value={summary?.token_transfer_count ?? "—"}
              source="summary.token_transfer_count"
              sourceNote="来自 summary.token_transfer_count：token 转账事件计数（activity_type=transfer 的真实行）。"
            />
            <MetricCard
              label="对手方"
              value={summary?.counterparty_count ?? "—"}
              source="summary.counterparty_count"
              sourceNote="来自 summary.counterparty_count：与该地址发生过资金往来的不同地址数，由服务端去重后给出。"
            />
            <MetricCard
              label="首次活动"
              value={formatTime(summary?.first_seen)}
              source="summary.first_seen"
              sourceNote="来自 summary.first_seen：本地索引中该地址最早一条活动记录的链上时间。"
            />
            <MetricCard
              label="最近活动"
              value={formatTime(summary?.last_seen)}
              source="summary.last_seen"
              sourceNote="来自 summary.last_seen：本地索引中该地址最新一条活动记录的链上时间。"
            />
          </div>
        </PageSection>
        <PageSection
          title="当前余额"
          count={<StatusBadge tone="info">is_current_balance=true</StatusBadge>}
          actions={
            <span className="s4-list__sub">
              {[currentBlock?.source, currentBlock?.observed_at ? formatTime(currentBlock.observed_at) : ""].filter(Boolean).join(" · ") || "—"}
            </span>
          }
        >
          <Card flush>
            <CurrentBalanceTable chain={chain} items={currentAssets} />
          </Card>
        </PageSection>
        <PageSection
          title="历史净流量"
          count={<StatusBadge tone="neutral">is_current_balance=false</StatusBadge>}
          actions={<span className="s4-list__sub">{flowBlock?.source || "—"}</span>}
        >
          <Card flush>
            <NetFlowTable chain={chain} items={flowItems} />
          </Card>
        </PageSection>
      </>
    );
  };

  const renderAssets = () => {
    if (assets.loading && !assets.data) {
      return (
        <PageSection title="资产">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!assets.data) {
      return (
        <PageSection title="资产">
          <Card flush>
            <ErrorState title="资产数据不可用" message="加载失败，请重试。" onRetry={() => void assets.reload()} />
          </Card>
        </PageSection>
      );
    }
    return (
      <>
        <PageSection title="当前余额" count={<StatusBadge tone="info">is_current_balance=true</StatusBadge>}>
          <Card flush>
            <CurrentBalanceTable chain={chain} items={assetsPayload?.current_balance ?? []} />
          </Card>
        </PageSection>
        <PageSection title="历史净流量" count={<StatusBadge tone="neutral">is_current_balance=false</StatusBadge>}>
          <Card flush>
            <NetFlowTable chain={chain} items={assetsPayload?.historical_net_flow ?? []} />
          </Card>
        </PageSection>
      </>
    );
  };

  const activityTitle = tab === "transfers" ? "代币转账" : tab === "internal" ? "内部转账" : "普通交易";

  const renderActivity = () => {
    if (activity.loading && !activity.data) {
      return (
        <PageSection title={activityTitle}>
          <Card flush>
            <SkeletonRows rows={6} columns={6} />
          </Card>
        </PageSection>
      );
    }
    if (!activity.data) {
      return (
        <PageSection title={activityTitle}>
          <Card flush>
            <ErrorState
              title={activityTitle + " 数据不可用"}
              message="加载失败，请重试。"
              onRetry={() => void activity.reload()}
            />
          </Card>
        </PageSection>
      );
    }
    const rows = activity.data?.items ?? [];
    const filtered =
      tab === "transfers"
        ? rows.filter((row) => row.activity_type === (assetType === "nft" ? "nft_transfer" : "transfer"))
        : tab === "internal"
          ? rows.filter((row) => row.activity_type === "internal_transfer")
          : rows.filter((row) => row.activity_type !== "transfer" && row.activity_type !== "internal_transfer");
    const visible = hideZero ? filtered.filter(row => !/^(0+|0x0+)$/i.test(row.amount_raw ?? row.amount ?? "")) : filtered;
    return (
      <PageSection title={activityTitle} count={filtered.length + " 行"}>
        {/* 筛选条件与请求参数一一对应，并且全部写进 URL（direction / token / 时间窗）。 */}
        <div className="cs-oktoolbar">
          <TransferToolbar rows={activity.data?.items ?? []} showToken={tab === "transfers"} showAmount={assetType === "token"}
            assetType={assetType} onAssetType={value => updateParams({ asset_type: value === "nft" ? "nft" : null, cursor: null })}
            token={token} onToken={value => updateParams({ token: value || null, cursor: null })}
            windowDays={windowDays} onWindowDays={value => updateParams({ window_days: String(value), from_time: null, to_time: null, from_date: null, to_date: null, cursor: null })}
            fromDate={fromDate || fromTime?.slice(0,10) || ""} toDate={toDate || toTime?.slice(0,10) || ""}
            windowFrom={activityFromTime?.slice(0,10) || ""} windowTo={activityToTime?.slice(0,10) || new Date().toISOString().slice(0,10)}
            onDates={value => updateParams({ from_date: value.from, to_date: value.to, from_time: null, to_time: null, cursor: null })}
            direction={direction} onDirection={value => updateParams({ direction: value || null, cursor: null })}
            fromAddress={fromAddress} toAddress={toAddress} onParty={value => updateParams({ from_address: value.from, to_address: value.to, cursor: null })}
            minAmount={minAmount} maxAmount={maxAmount} onAmount={value => updateParams({ min_amount: value.min, max_amount: value.max, cursor: null })}
            onRefresh={() => setRefreshNonce(value => value + 1)} onResetAll={() => updateParams({ direction: null, token: null, counterparty: null, from_address: null, to_address: null, from_time: null, to_time: null, from_date: null, to_date: null, min_amount: null, max_amount: null, window_days: null, hide_zero: null, asset_type: null, cursor: null })} />
          <details className="cs-query-extra-filters"><summary>更多筛选</summary>
            <Input.Search aria-label="对手方筛选" value={counterpartyDraft} placeholder="对手方完整地址" onChange={event => setCounterpartyDraft(event.target.value)} onSearch={value => updateParams({ counterparty: value.trim(), cursor: null })} />
            <label><input type="checkbox" checked={hideZero} onChange={event => updateParams({ hide_zero: event.target.checked ? "1" : null })} />隐藏数量为 0 的转账</label>
          </details>
          {can("export:create") && <Link className="cs-query-export-link" to={`/exports?${new URLSearchParams({ chain, address: fullAddress, datasets: assetType === "nft" && tab === "transfers" ? "nft_transfers" : tab === "transfers" ? "token_transfers" : tab === "internal" ? "internal_transactions" : "transactions", content: "details", ...(activityFromTime ? { from_time: activityFromTime } : {}), ...(activityToTime ? { to_time: activityToTime } : {}), ...(token ? { token } : {}), ...(direction ? { direction } : {}), ...(counterparty ? { counterparty } : {}), ...(fromAddress ? { from_address: fromAddress } : {}), ...(toAddress ? { to_address: toAddress } : {}), ...(minAmount ? { min_amount: minAmount } : {}), ...(maxAmount ? { max_amount: maxAmount } : {}), ...(searchParams.get("as_of") ? { as_of: searchParams.get("as_of")! } : {}), ...(searchParams.get("addresses") ? { addresses: searchParams.get("addresses")! } : {}) })}`}>导出当前筛选</Link>}
        </div>
        <Card flush>
          {bulkLoading ? (
            <div style={{ padding: "var(--sp-3) var(--sp-4)" }}>
              {/* 服务端不给总量：进度条保持 indeterminate，只显示已经真实拿到的行数。 */}
              <LinearProgress
                percent={null}
                state="running"
                label="批量加载活动"
                valueText={"已加载 " + bulkLoaded.toLocaleString("zh-CN") + " 行"}
              />
            </div>
          ) : null}
          <div className="cs-oklist">{tab === "transfers" && assetType === "nft" ? <NftTransferTable rows={bulkRows ?? visible} chain={chain} loading={activity.loading || bulkLoading} onSelectActivity={setSelectedActivity} /> : <ActivityTable
            rows={bulkRows ?? visible}
            chain={chain}
            loading={activity.loading || bulkLoading}
            emptyTitle={"没有 " + activityTitle + " 记录"}
            onSelectActivity={setSelectedActivity}
          />}</div>
          {activity.data ? (
            <div
              className="toolbar"
              style={{
                justifyContent: "flex-end",
                padding: "var(--sp-3) var(--sp-4)",
                borderTop: "1px solid var(--border-default)",
                flexWrap: "wrap",
              }}
            >
              {bulkRows ? (
                <Button size="sm" onClick={() => setBulkRows(null)}>
                  {"返回分页视图（" + bulkRows.length + " 行）"}
                </Button>
              ) : (
                <>
                  {cursor ? (
                    <Button size="sm" onClick={() => updateParams({ cursor: null })}>
                      回到最新
                    </Button>
                  ) : null}
                  <span className="cs-section__count">本页 {filtered.length} 行</span>
                  <Button size="sm" busy={bulkLoading} busyLabel="加载中…" onClick={() => void loadAllActivity()}>
                    批量加载（最多 10,000 行）
                  </Button>
                  <Button size="sm" disabled={!nextCursor} onClick={() => updateParams({ cursor: nextCursor })}>
                    加载下一页
                  </Button>
                </>
              )}
            </div>
          ) : null}
        </Card>
      </PageSection>
    );
  };

  const renderSemantic = () => {
    if (semantic.loading && !semantic.data) {
      return (
        <PageSection title="语义事件">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!semantic.data) {
      return (
        <PageSection title="语义事件">
          <Card flush>
            <ErrorState title="语义事件不可用" message="加载失败，请重试。" onRetry={() => void semantic.reload()} />
          </Card>
        </PageSection>
      );
    }
    const result = semantic.data.items;
    if (result != null && !hasSemanticResultShape(result)) {
      return (
        <PageSection title="语义结果">
          <Card>
            {/* 原始内容只作为文本呈现，绝不作为 HTML 注入。 */}
            <pre className="s5-json">{jsonText(result, 4000)}</pre>
          </Card>
        </PageSection>
      );
    }
    return (
      <PageSection title="语义事件" count={events.length + " 条"}>
        <Card flush>
          <SemanticTable events={events} chain={chain} />
        </Card>
      </PageSection>
    );
  };

  const renderAnalysis = () => {
    if (analysis.loading && !analysis.data) {
      return (
        <PageSection title="分析">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    const data = analysis.data?.items;
    if (!data) {
      return (
        <PageSection title="分析">
          <Card flush>
            <ErrorState title="分析数据不可用" message="加载失败，请重试。" onRetry={() => void analysis.reload()} />
          </Card>
        </PageSection>
      );
    }
    return <AnalysisView data={data} chain={chain} />;
  };

  const renderCoverage = () => {
    if (overview.loading && !overview.data) {
      return (
        <PageSection title="数据覆盖">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!overview.data) {
      return (
        <PageSection title="数据覆盖">
          <Card flush>
            <ErrorState title="数据覆盖 数据不可用" message="加载失败，请重试。" onRetry={() => void overview.reload()} />
          </Card>
        </PageSection>
      );
    }
    return (
      <PageSection
        title="数据覆盖"
        count={coverageItems.length + " 个 dataset"}
        actions={<span className="s4-list__sub">{overview.data.coverage?.source || "—"}</span>}
      >
        <Card flush>
          <CoverageTable items={coverageItems} />
        </Card>
      </PageSection>
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
            ? <Suspense fallback={<SkeletonRows rows={6} columns={4} />}><StatisticsPanel chain={chain} address={fullAddress} /></Suspense>
            : tab === "statistics" ? <Suspense fallback={<SkeletonRows rows={6} columns={4} />}><StatisticsPanel chain={chain} address={fullAddress} /></Suspense>
            : tab === "semantic"
              ? renderSemantic()
              : tab === "analysis"
                ? renderAnalysis()
                : renderCoverage();

  /** 右侧检查器：选中行 → 行详情；未选中 → 数据来源与口径（原先 EvidenceRail 的同一份真实字段）。 */
  const inspector = selectedActivity ? (
    <InspectorDrawer
      title="活动详情"
      subtitle={selectedActivity.activity_type || "—"}
      onClose={() => setSelectedActivity(null)}
      actions={
        <>
          {selectedActivity.tx_hash ? (
            <Link className="btn btn--sm" to={transactionPath(chain, selectedActivity.tx_hash)}>
              打开交易
            </Link>
          ) : null}
          {selectedActivity.counterparty ? (
            <Link className="btn btn--sm" to={addressPath(chain, selectedActivity.counterparty)}>
              打开对手方
            </Link>
          ) : null}
        </>
      }
    >
      <KeyValueList
        items={[
          { label: "时间", value: formatTime(selectedActivity.timestamp) },
          {
            label: "方向",
            value: <StatusBadge tone={directionTone(selectedActivity.direction)}>{selectedActivity.direction || "—"}</StatusBadge>,
          },
          { label: "类型", value: selectedActivity.activity_type || "—" },
          { label: "区块", value: <span className="mono">{blockText(selectedActivity.block_number)}</span> },
          {
            label: "对手方",
            value: selectedActivity.counterparty ? (
              <EntityHash
                value={selectedActivity.counterparty}
                to={addressPath(chain, selectedActivity.counterparty)}
                label="对手方地址"
                head={14}
                tail={10}
              />
            ) : (
              "—"
            ),
            wide: true,
          },
          {
            label: "tx_hash",
            value: selectedActivity.tx_hash ? (
              <EntityHash
                value={selectedActivity.tx_hash}
                to={transactionPath(chain, selectedActivity.tx_hash)}
                label="交易哈希"
                head={14}
                tail={10}
              />
            ) : (
              "—"
            ),
            wide: true,
          },
          {
            label: "资产合约",
            value: selectedActivity.token_address ? (
              <HashValue value={selectedActivity.token_address} label="Token 合约地址" wrap />
            ) : (
              selectedActivity.token_symbol || "原生"
            ),
            wide: true,
          },
          {
            label: "金额",
            value: (
              <Amount
                rawAmount={selectedActivity.amount_raw}
                decimals={selectedActivity.token_decimals}
                unit={selectedActivity.token_symbol}
                direction={amountDirection(selectedActivity.direction)}
                fiat={fiatText(selectedActivity.amount_usd)}
              />
            ),
            wide: true,
          },
          { label: "raw 金额", value: <span className="mono">{selectedActivity.amount_raw ?? "—"}</span>, wide: true },
          { label: "log_index", value: <span className="mono">{selectedActivity.log_index ?? "—"}</span> },
          { label: "来源 provider", value: selectedActivity.source_provider || "—" },
          { label: "dataset", value: selectedActivity.dataset || "—" },
          { label: "入库时间", value: formatTime(selectedActivity.source_ingested_at) },
        ]}
      />
    </InspectorDrawer>
  ) : selectedCounterparty ? (
    <InspectorDrawer
      title="对手方详情"
      subtitle={(selectedCounterparty.transaction_count ?? 0) + " 条活动"}
      onClose={() => setSelectedCounterparty(null)}
      actions={
        selectedCounterparty.counterparty ? (
          <Link className="btn btn--sm" to={addressPath(chain, selectedCounterparty.counterparty)}>
            打开对手方地址
          </Link>
        ) : null
      }
    >
      <KeyValueList
        items={[
          {
            label: "对手方",
            value: selectedCounterparty.counterparty ? (
              <HashValue value={selectedCounterparty.counterparty} label="对手方地址" wrap />
            ) : (
              "—"
            ),
            wide: true,
          },
          { label: "活动条数", value: <span className="mono">{selectedCounterparty.transaction_count ?? "—"}</span> },
          { label: "资产数", value: <span className="mono">{selectedCounterparty.assets?.length ?? "—"}</span> },
          ...(selectedCounterparty.assets ?? []).map((asset, index) => ({
            label: asset.token_symbol || asset.asset || `资产 ${index + 1}`,
            value: (
              <div className="mono">
                <div>{asset.token_address || asset.asset} · {asset.dataset} · {asset.activity_count} 条</div>
                <div>入账 raw：{asset.in_raw ?? "待补齐"}</div>
                <div>出账 raw：{asset.out_raw ?? "待补齐"}</div>
                <div>净额 raw：{asset.net_raw ?? "待补齐"}</div>
                {asset.missing_amount_rows ? <div>缺少原始金额：{asset.missing_amount_rows} 条</div> : null}
              </div>
            ),
            wide: true,
          })),
          { label: "首次", value: formatTime(selectedCounterparty.first_seen) },
          { label: "最近", value: formatTime(selectedCounterparty.last_seen) },
        ]}
      />
    </InspectorDrawer>
  ) : (
    <InspectorDrawer title="数据来源与口径" subtitle={overview.data?.source?.provider}>
      <KeyValueList items={sourceItems} />
    </InspectorDrawer>
  );

  useEffect(() => {
    if (!selectedActivity && !selectedCounterparty) return;
    const review = dialog.openSession({ kind: "info", title: selectedActivity ? "交易详情" : "对手方详情", body: inspector,
      actions: [{ label: "关闭", result: "close", variant: "default" }],
      onResolve: () => { setSelectedActivity(null); setSelectedCounterparty(null); },
    });
    return () => review.dismiss();
  }, [selectedActivity, selectedCounterparty]);

  const primaryTabs = ["transfers", "transactions", "internal", "assets", "counterparties", "statistics"];
  const nativeBalance = currentAssets.find(item => !item.token_address);
  const downloadParams = new URLSearchParams({ chain, address: fullAddress,
    datasets: tab === "transfers" ? (assetType === "nft" ? "nft_transfers" : "token_transfers") : tab === "internal" ? "internal_transactions" : "transactions",
    ...(activityFromTime ? { from_time: activityFromTime, to_time: activityToTime || new Date().toISOString() } : {}),
  });
  return (
    <div className="cs-explorer-object-page">
      <ExplorerSearch initialChain={chain} />
      <div className="cs-query-breadcrumb"><Link to="/explorer">区块链查询</Link><span>/</span><span>地址详情</span></div>
      <AntCard className="cs-address-overview" data-compact={tab === "statistics" || tab === "counterparties" ? "true" : undefined}>
        <div className="cs-query-object-title"><ChainIcon chain={chain} size={36} /><h1>地址</h1><Tag>{chain.toUpperCase()}</Tag></div>
        <div className="cs-query-address-line"><span className="mono cs-query-full-address">{fullAddress}</span>
          <AntButton type="text" aria-label="复制地址" icon={<Copy size={16} />} onClick={() => void copy(fullAddress, "地址")} />
          <AntButton type="text" aria-label="地址二维码" icon={<QrCode size={16} />} onClick={() => dialog.notify("info", "地址二维码", <div className="cs-query-qr"><QRCode value={fullAddress} size={220} /><span className="mono">{fullAddress}</span></div>)} />
          <AntButton type="text" aria-label="复制查询链接" icon={<Share2 size={16} />} onClick={() => void copy(window.location.href, "查询链接")} />
          <div className="cs-query-object-actions">{can("download:create") && <Link to={`/downloads/new?${downloadParams}`}>下载数据</Link>}
            {can("investigation:create") && <Link to={`/investigations/new?${new URLSearchParams({ chain, address: fullAddress })}`}>发起调查</Link>}
            {can("canvas:list") && <Link to={`/graph?${new URLSearchParams({ chain, address: fullAddress })}`}>资金图谱</Link>}
            <AntButton type="text" aria-label="地址数据说明" disabled={!overview.data} onClick={showCaliber}>数据说明</AntButton></div>
        </div>
        <div className="cs-address-summary-grid">
          <div><span>原生资产余额</span><strong>{nativeBalance ? `${formatRawAmount(nativeBalance.raw_amount, decimalsOf(nativeBalance))} ${nativeBalance.token_symbol || ""}` : "—"}</strong><span>快照时间 {formatTime(currentBlock?.observed_at)}</span></div>
          <div><span>本地交易 / 代币事件</span><strong>{summary?.transaction_count ?? "—"} / {summary?.token_transfer_count ?? "—"}</strong><span>对手方 {summary?.counterparty_count ?? "—"}</span></div>
          <div><span>首次活动</span><strong>{formatTime(summary?.first_seen)}</strong><span>最近活动 {formatTime(summary?.last_seen)}</span></div>
        </div>
        <div className="cs-query-data-meta"><span>数据更新时间 {formatTime(overview.data?.source?.as_of)}</span><span>本地索引</span>
          <AntButton type="link" size="small" onClick={() => updateParams({ tab: "coverage", cursor: null })}>{overview.data ? `数据覆盖 ${coverageItems.length} 个数据集` : "数据覆盖"}</AntButton></div>
      </AntCard>
      <div className="cs-query-results">
        <nav className="cs-query-tabs" aria-label="地址详情分区">
          {TABS.filter(item => primaryTabs.includes(item.key) || item.key === tab).map(item => {
            const next = new URLSearchParams(searchParams); next.set("tab", item.key); next.delete("cursor");
            return <a key={item.key} href={`?${next}`} aria-current={item.key === tab ? "page" : undefined} onClick={event => { event.preventDefault(); updateParams({ tab: item.key, cursor: null }); }}>{item.label}</a>;
          })}
          <Dropdown menu={{ items: TABS.filter(item => !primaryTabs.includes(item.key)).map(item => ({ key: item.key, label: item.label })), onClick: item => updateParams({ tab: item.key, cursor: null }) }} trigger={["click"]}><AntButton type="text">更多</AntButton></Dropdown>
        </nav>
        <div className="cs-query-results-content">{content}</div>
      </div>
    </div>
  );
}
