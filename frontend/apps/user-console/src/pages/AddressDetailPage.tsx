import {
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import { Link,
  useParams,
  useSearchParams } from "react-router-dom";
import { ArrowLeftRight, CalendarDays, Clock3, FileText, Users } from "lucide-react";
import { Tabs } from "antd";
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
  InspectorDrawer,
  KeyValueList,
  LinearProgress,
  MetaLine,
  MetricCard,
  PageSection,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  Switch,
  statusToneFor,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { useAuth } from "@chain/api-client";
import { formatRawAmount, formatTime, shortValue, useApi } from "../sprint5/common";
import "./detail-polish.css";

/**
 * U-EXP-02 地址详情。
 * - 只有真实 API：overview / assets / activity / counterparties / semantic / flow-profile。
 * - 普通交易 / Token Transfer / Internal 均来自 activity 真实行，按 activity_type 区分（Internal 为链上内部转账记录）。
 * - 当前余额（is_current_balance=true）与历史净流量（is_current_balance=false）**分开呈现**，各自标注口径来源。
 * - Tab / cursor / 筛选全部写入 URL，刷新可恢复；筛选变化重置 cursor。
 *
 * 2026-09 表现层重构（蓝图 §12.15）：
 *   Header（链 / 完整地址 / 复制 / 数据时间）→ Overview（指标 + 当前余额 + 历史净流量）
 *   → Activity（tab + 参考站点式胶囊工具栏）→ Fund Flow（对手方 / 资金流边）→ Analysis（flow-profile）
 *   → Side Inspector（仅在选中活动行或对手方行时显示详情）。
 *   接口路径、请求参数、分页与错误语义一律未改；本文件只重组信息架构。
 */

/* 类型与纯函数已抽到 ./address-detail/address-detail.types.ts（规范 §43 的目标文件之一）。
   本次只做位移：字段名、口径与派生规则一字未改，9 个业务 Tab 的 key 与顺序也保持不变。 */
import {
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
import { TransferToolbar } from "./address-detail/TransferToolbar";
import { EntityHash, CurrentBalanceTable, NetFlowTable, ActivityTable, NftTransferTable, CounterpartyTable, SemanticTable, AnalysisView, CoverageTable } from "./address-detail/tabs/AddressTables";

export default function AddressDetailPage() {
  const { chain = "", address = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const dialog = useDialog();
  const { client } = useAuth();
  // P1-1 第 3 项：活动是长表路径（实测 8.09s @ limit=200 全量窗口），默认收窄到最近 30 天。
  // 服务端的 Filter 一直支持 from_time/to_time（会写成 WHERE timestamp >=/<=），此前只是前端
  // 从不传、handler 也不透传，所以每次都是全量拉。选「全部」会退回较慢的全量查询。
  const [windowDays, setWindowDays] = useState(30);
  /**
   * 转账资产类别：代币走 address_activity，NFT 走 nft_transfers（同一接口的 asset_type）。
   * NFT 行不进地址活动投影，所以必须切数据集读，而不是在前端过滤。
   */
  const [assetType, setAssetType] = useState<"token" | "nft">("token");
  const [bulkRows, setBulkRows] = useState<ActivityRow[] | null>(null);
  const [bulkLoading, setBulkLoading] = useState(false);
  // 仅用于展示"已经真实拿到多少行"——服务端不提供总量，因此进度条保持 indeterminate。
  const [bulkLoaded, setBulkLoaded] = useState(0);
  // 右侧检查器只在选中活动行或对手方行时显示；未选中时主内容占满宽度。
  const [selectedActivity, setSelectedActivity] = useState<ActivityRow | null>(null);
  const [selectedCounterparty, setSelectedCounterparty] = useState<CounterpartyRow | null>(null);
  const requestedTab = searchParams.get("tab") ?? "overview";
  const tab = TABS.some((item) => item.key === requestedTab) ? requestedTab : "overview";
  const cursor = searchParams.get("cursor") ?? "";
  const direction = searchParams.get("direction") ?? "";
  const token = searchParams.get("token") ?? "";
  // OKLink 式工具栏（日期区间 / 数量区间 / 发送方·接收方 / 隐藏 0 数量）全部写进 URL，
  // 与已有 direction/token/cursor 同一约定：刷新可恢复，变化即重新取数。
  const fromAddress = searchParams.get("from_address") ?? "";
  const toAddress = searchParams.get("to_address") ?? "";
  const minAmount = searchParams.get("min_amount") ?? "";
  const maxAmount = searchParams.get("max_amount") ?? "";
  const fromDate = searchParams.get("from_date") ?? "";
  const toDate = searchParams.get("to_date") ?? "";
  const hideZero = searchParams.get("hide_zero") === "1";
  const [refreshNonce, setRefreshNonce] = useState(0);
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
  const activityFromTime = useMemo(() => {
    // 显式日期区间优先于「最近 N 天」下拉（两者同时存在时以日期为准，避免互相覆盖）。
    if (fromDate) return new Date(fromDate + "T00:00:00").toISOString();
    return windowDays > 0 ? new Date(Date.now() - windowDays * 86_400_000).toISOString() : undefined;
  }, [windowDays, fromDate]);
  const activityToTime = useMemo(
    () => (toDate ? new Date(toDate + "T23:59:59.999").toISOString() : undefined),
    [toDate],
  );
  const overview = useApi<OverviewData>(base ? base + "/overview" : null, [chain, address]);
  const assets = useApi<AssetsPayload>(tab === "assets" && base ? base + "/assets" : null, [chain, address, tab]);
  const activityActive = tab === "transactions" || tab === "transfers" || tab === "internal";
  const activity = useApi<{ items: ActivityRow[] }>(
    activityActive && base ? base + "/activity" : null,
    [
      chain,
      address,
      tab,
      cursor,
      direction,
      token,
      windowDays,
      assetType,
      fromAddress,
      toAddress,
      minAmount,
      maxAmount,
      fromDate,
      toDate,
      refreshNonce,
    ],
    activityActive
      ? {
          limit: 200,
          // 列表只需本地索引和批量元数据；完整富化会对一页中大量对手方逐个外呼。
          // 选中行所展示的证据字段仍由 /activity 返回，其他调用方默认响应不变。
          light: 1,
          cursor: cursor || undefined,
          direction: direction || undefined,
          token: tab === "transfers" && assetType === "token" ? token || undefined : undefined,
          asset_type: tab === "transfers" && assetType === "nft" ? "nft" : undefined,
          from_time: activityFromTime,
          to_time: activityToTime,
          // 发送方/接收方与数量区间：后端 Filter 早已支持，这里只是把工具栏接上去。
          from_address: fromAddress || undefined,
          to_address: toAddress || undefined,
          min_amount: tab === "transfers" && assetType === "token" ? minAmount || undefined : undefined,
          max_amount: tab === "transfers" && assetType === "token" ? maxAmount || undefined : undefined,
        }
      : undefined,
  );
  const counterparties = useApi<{ items: CounterpartyRow[] }>(
    tab === "counterparties" && base ? base + "/counterparties" : null,
    [chain, address, tab],
    { limit: 200 },
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

  const updateParams = (updates: Record<string, string | null>) => {
    const params = new URLSearchParams(searchParams);
    for (const [key, value] of Object.entries(updates)) {
      if (value === null || value === "") params.delete(key);
      else params.set(key, value);
    }
    setSearchParams(params);
  };

  const selectTab = (nextTab: string) => {
    setSelectedActivity(null);
    setSelectedCounterparty(null);
    updateParams({
      tab: nextTab,
      cursor: null,
      ...(nextTab !== "transfers" ? { token: null } : {}),
    });
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
      "内部交易数据说明",
      "内部交易行来自本地 address_activity 中 activity_type=internal_transfer 的真实记录；是否可用取决于该链 internal_transactions 数据集是否已同步。空列表表示本地索引没有记录，不代表链上不存在。",
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
  /**
   * 日期胶囊的展示值：没显式选日期时，把当前时间窗真正覆盖到的起止日填进胶囊
   * （参考站点的胶囊始终显示一个区间）。"全部时间"用该地址首末活动时间兜底，
   * 仍然是服务端真实字段，不是编出来的筛选条件；写 URL 只写用户真正选过的日期。
   */
  const windowRange = useMemo(() => {
    const from = activityFromTime
      ? activityFromTime.slice(0, 10)
      : (summary?.first_seen ?? "").slice(0, 10);
    const to = activityToTime ? activityToTime.slice(0, 10) : new Date().toISOString().slice(0, 10);
    return { from, to };
  }, [activityFromTime, activityToTime, summary?.first_seen]);
  const currentBlock = summary?.current_balance;
  const flowBlock = summary?.historical_net_flow;
  const coverageItems = overview.data?.coverage?.items ?? [];
  const currentAssets = currentBlock?.items ?? [];
  const flowItems = flowBlock?.items ?? [];
  const assetsPayload = assets.data?.items;
  const events = extractEvents(semantic.data?.items);
  const nextCursor = activity.meta.next_cursor ?? "";
  const fullAddress = subject?.normalized_address || subject?.address || address;

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
    setBulkLoaded(0);
    const collected: ActivityRow[] = [];
    let cursorValue = "";
    try {
      for (let page = 0; page < 50 && collected.length < 10000; page += 1) {
        const response = await client.get<{ items: ActivityRow[] }>(base + "/activity", {
          query: {
            limit: 200,
            cursor: cursorValue || undefined,
            direction: direction || undefined,
            token: token || undefined,
            from_time: activityFromTime,
            // 全量视图跳过逐行元数据富化，仅保留本地索引原始列，避免 50 页被 Contract/Case 查询拖慢。
            full: 1,
          },
        });
        const items = response.data?.items ?? [];
        collected.push(...items);
        setBulkLoaded(collected.length);
        const next = response.meta.next_cursor ?? "";
        if (!next || items.length === 0) break;
        cursorValue = next;
      }
      setBulkRows(collected.slice(0, 10000));
      dialog.notify("success", "全量活动已加载", "共 " + collected.length + " 行；表格仅渲染视口附近的行。");
    } catch (error) {
      dialog.notify("error", "加载全量活动失败", error instanceof Error ? error.message : "请稍后重试");
    } finally {
      setBulkLoading(false);
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
        <PageSection title="概览指标" count="5 项" className="cs-overview-metrics">
          <div className="metric-grid cs-stagger cs-statistic-group">
            <MetricCard
              label="交易数"
              icon={<ArrowLeftRight size={20} strokeWidth={1.9} />}
              value={summary?.transaction_count ?? "—"}
              source="summary.transaction_count"
              sourceNote="来自服务端地址概览接口的 summary.transaction_count：该地址作为参与方的普通交易计数，范围受当前链与本地索引覆盖限制。"
            />
            <MetricCard
              label="代币转账"
              icon={<FileText size={20} strokeWidth={1.9} />}
              value={summary?.token_transfer_count ?? "—"}
              source="summary.token_transfer_count"
              sourceNote="来自 summary.token_transfer_count：token 转账事件计数（activity_type=transfer 的真实行）。"
            />
            <MetricCard
              label="对手方"
              icon={<Users size={20} strokeWidth={1.9} />}
              value={summary?.counterparty_count ?? "—"}
              source="summary.counterparty_count"
              sourceNote="来自 summary.counterparty_count：与该地址发生过资金往来的不同地址数，由服务端去重后给出。"
            />
            <MetricCard
              label="首次活动"
              icon={<CalendarDays size={20} strokeWidth={1.9} />}
              value={formatTime(summary?.first_seen)}
              source="summary.first_seen"
              sourceNote="来自 summary.first_seen：本地索引中该地址最早一条活动记录的链上时间。"
            />
            <MetricCard
              label="最近活动"
              icon={<Clock3 size={20} strokeWidth={1.9} />}
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
            <CurrentBalanceTable items={currentAssets} />
          </Card>
        </PageSection>
        <PageSection
          title="历史净流量"
          count={<StatusBadge tone="neutral">is_current_balance=false</StatusBadge>}
          actions={<span className="s4-list__sub">{flowBlock?.source || "—"}</span>}
        >
          <Card flush>
            <NetFlowTable items={flowItems} />
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
            <CurrentBalanceTable items={assetsPayload?.current_balance ?? []} />
          </Card>
        </PageSection>
        <PageSection title="历史净流量" count={<StatusBadge tone="neutral">is_current_balance=false</StatusBadge>}>
          <Card flush>
            <NetFlowTable items={assetsPayload?.historical_net_flow ?? []} />
          </Card>
        </PageSection>
      </>
    );
  };

  const activityTitle = tab === "transfers" ? "代币转账" : tab === "internal" ? "内部交易" : "普通交易";

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
        ? rows.filter((row) =>
            assetType === "nft"
              ? row.activity_type === "nft_transfer"
              : row.activity_type === "transfer",
          )
        : tab === "internal"
          ? rows.filter((row) => row.activity_type === "internal_transfer")
          : rows.filter((row) => row.activity_type !== "transfer" && row.activity_type !== "internal_transfer");
    // 「隐藏数量为 0」是纯展示开关：不重发请求，只过滤本页/全量视图已取到的行。
    const visible = hideZero
      ? filtered.filter((row) => {
          const raw = (row.amount_raw ?? "").trim();
          if (raw) return !/^[-+]?0+$/.test(raw);
          const text = (row.amount ?? "").trim();
          return text ? !/^[-+]?0(\.0+)?$/.test(text) : false;
        })
      : filtered;
    /** 筛选是否生效：生效后服务端 summary 的总量不再是当前口径，计数行只报"已加载多少行"。 */
    const filtersActive = Boolean(
      direction || token || fromAddress || toAddress || minAmount || maxAmount || fromDate || toDate,
    );
    /*
     * 只有代币转账 Tab 能用服务端 summary 的总量：`token_transfer_count` 与这一页的行
     * （activity_type=transfer）是同一个口径。普通交易/内部交易/NFT 的 summary 字段
     * 与表格实际过滤出来的行不是一回事，宁可只报"已加载多少行"，也不编一个总数。
     */
    const serverTotal = tab === "transfers" && assetType === "token" ? summary?.token_transfer_count : undefined;
    const countText = activity.loading
      ? "正在加载本页交易记录…"
      : !filtersActive && typeof serverTotal === "number"
        ? "共计 " + serverTotal.toLocaleString("zh-CN") + " 条交易记录（最多加载 1 万行）"
        : "已加载 " + visible.length.toLocaleString("zh-CN") + " 行" + (filtersActive ? "（筛选结果，最多 1 万行）" : "");
    return (
      <PageSection title={activityTitle} count={activity.loading ? "加载中…" : visible.length + " 行"}>
        {/*
          工具栏与计数行按参考站点重排：一行胶囊控件（代币 / 数量 / 日期区间 / 方向 / 发送方·接收方 + 刷新）
          + 「共计 N 条交易记录」与「隐藏数量为 0 的转账」开关同一行。所有筛选能力一个都没少，
          每个变化都写 URL 并重置 cursor（与旧 FilterBar 完全同一套取数口径）。
        */}
        <div className="cs-oktoolbar">
          <TransferToolbar
            rows={rows}
            showToken={tab === "transfers"}
            showAmount={tab === "transfers" && assetType === "token"}
            assetType={assetType}
            onAssetType={(next) => {
              setAssetType(next);
              updateParams({ cursor: null });
            }}
            token={token}
            onToken={(next) => updateParams({ token: next || null, cursor: null })}
            windowDays={windowDays}
            onWindowDays={(next) => {
              setWindowDays(next);
              updateParams({ cursor: null });
            }}
            fromDate={fromDate}
            toDate={toDate}
            windowFrom={windowRange.from}
            windowTo={windowRange.to}
            onDates={(next) => updateParams({ from_date: next.from, to_date: next.to, cursor: null })}
            direction={direction}
            onDirection={(next) => updateParams({ direction: next || null, cursor: null })}
            fromAddress={fromAddress}
            toAddress={toAddress}
            onParty={(next) => updateParams({ from_address: next.from, to_address: next.to, cursor: null })}
            minAmount={minAmount}
            maxAmount={maxAmount}
            onAmount={(next) =>
              updateParams({
                min_amount: assetType === "token" ? next.min : null,
                max_amount: assetType === "token" ? next.max : null,
                cursor: null,
              })
            }
            onRefresh={() => setRefreshNonce((value) => value + 1)}
            onResetAll={() => {
              setAssetType("token");
              setWindowDays(30);
              updateParams({
                direction: null,
                token: null,
                from_address: null,
                to_address: null,
                min_amount: null,
                max_amount: null,
                from_date: null,
                to_date: null,
                hide_zero: null,
                cursor: null,
              });
            }}
          />
        </div>
        <Card flush>
          <div className="cs-okcount">
            <span className="cs-okcount__text">{countText}</span>
            <span className="cs-okcount__toggle">
              <Switch
                checked={hideZero}
                onChange={(next) => updateParams({ hide_zero: next ? "1" : null, cursor: null })}
                label="隐藏数量为 0 的转账"
              />
              隐藏数量为 0 的转账
            </span>
          </div>
          {/* 参考站点的行形状：14px 正文、48px 表头、固定列宽（表格不横向溢出）。 */}
          <div className="cs-oklist">
          {bulkLoading ? (
            <div style={{ padding: "var(--sp-3) var(--sp-4)" }}>
              {/* 服务端不给总量：进度条保持 indeterminate，只显示已经真实拿到的行数。 */}
              <LinearProgress
                percent={null}
                state="running"
                label="加载全量活动"
                valueText={"已加载 " + bulkLoaded.toLocaleString("zh-CN") + " 行"}
              />
            </div>
          ) : null}
          {tab === "transfers" && assetType === "nft" ? (
            <NftTransferTable
              rows={bulkRows ?? visible}
              chain={chain}
              loading={activity.loading || bulkLoading}
              onSelectActivity={setSelectedActivity}
              hidePager
            />
          ) : (
            <ActivityTable
              rows={bulkRows ?? visible}
              chain={chain}
              loading={activity.loading || bulkLoading}
              emptyTitle={"没有" + activityTitle + "记录"}
              onSelectActivity={setSelectedActivity}
              hidePager
            />
          )}
          </div>
          <div className="cs-okfooter">
            {bulkRows ? (
              <Button size="sm" onClick={() => setBulkRows(null)}>
                {"退出全量视图（" + bulkRows.length + " 行）"}
              </Button>
            ) : (
              <>
                {cursor ? (
                  <Button size="sm" onClick={() => updateParams({ cursor: null })}>
                    回到最新
                  </Button>
                ) : null}
                <Button
                  size="sm"
                  variant="primary"
                  disabled={activity.loading || !nextCursor}
                  onClick={() => updateParams({ cursor: nextCursor })}
                >
                  加载更多
                </Button>
                <Button size="sm" disabled={activity.loading} busy={bulkLoading} busyLabel="加载中…" onClick={() => void loadAllActivity()}>
                  加载全部（最多 10,000 行）
                </Button>
              </>
            )}
          </div>
        </Card>
      </PageSection>
    );
  };

  const renderCounterparties = () => {
    if (counterparties.loading && !counterparties.data) {
      return (
        <PageSection title="对手方">
          <Card flush>
            <SkeletonRows rows={6} columns={5} />
          </Card>
        </PageSection>
      );
    }
    if (!counterparties.data) {
      return (
        <PageSection title="对手方">
          <Card flush>
            <ErrorState title="对手方数据不可用" message="加载失败，请重试。" onRetry={() => void counterparties.reload()} />
          </Card>
        </PageSection>
      );
    }
    const rows = counterparties.data.items ?? [];
    return (
      <PageSection title="对手方" count={rows.length + " 个"}>
        <Card flush>
          <div className="cs-chart-frame">
            <CounterpartyTable rows={rows} chain={chain} onSelectCounterparty={setSelectedCounterparty} />
          </div>
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
        <PageSection title="Coverage">
          <Card flush>
            <SkeletonRows rows={6} columns={4} />
          </Card>
        </PageSection>
      );
    }
    if (!overview.data) {
      return (
        <PageSection title="Coverage">
          <Card flush>
            <ErrorState title="Coverage 数据不可用" message="加载失败，请重试。" onRetry={() => void overview.reload()} />
          </Card>
        </PageSection>
      );
    }
    return (
      <PageSection
        title="Coverage"
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
            ? renderCounterparties()
            : tab === "semantic"
              ? renderSemantic()
              : tab === "analysis"
                ? renderAnalysis()
                : renderCoverage();

  const detailTabs = TABS.map((item) => {
    const params = new URLSearchParams(searchParams);
    params.set("tab", item.key);
    params.delete("cursor");
    if (item.key !== "transfers") params.delete("token");
    const active = item.key === tab;
    return {
      key: item.key,
      label: (
        <a
          className="cs-tab"
          href={"?" + params.toString()}
          aria-current={active ? "page" : undefined}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            selectTab(item.key);
          }}
        >
          {item.label}
        </a>
      ),
    };
  });

  /** 右侧检查器只呈现用户主动选中的行详情。 */
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
          /* Solana 证据（ADR-023 D10）：服务端没有给值就不渲染，绝不为 EVM/TRON 编造代币账户。 */
          ...(selectedActivity.token_account
            ? [
                {
                  label: "主体代币账户 (ATA)",
                  value: <HashValue value={selectedActivity.token_account} label="主体代币账户" wrap />,
                  wide: true,
                },
              ]
            : []),
          ...(selectedActivity.counterparty_token_account
            ? [
                {
                  label: "对手方代币账户 (ATA)",
                  value: <HashValue value={selectedActivity.counterparty_token_account} label="对手方代币账户" wrap />,
                  wide: true,
                },
              ]
            : []),
          ...(selectedActivity.fee_amount_raw && selectedActivity.fee_amount_raw !== "0"
            ? [
                {
                  label: "代扣手续费",
                  value: <span className="mono">{selectedActivity.fee_amount_raw}</span>,
                  wide: true,
                },
              ]
            : []),
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
      subtitle={(selectedCounterparty.transaction_count ?? 0) + " 笔交易"}
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
          { label: "交易数", value: <span className="mono">{selectedCounterparty.transaction_count ?? "—"}</span> },
          { label: "入账 raw", value: <span className="mono">{selectedCounterparty.in_raw ?? "—"}</span>, wide: true },
          { label: "出账 raw", value: <span className="mono">{selectedCounterparty.out_raw ?? "—"}</span>, wide: true },
          { label: "净额 raw", value: <span className="mono">{selectedCounterparty.net_raw ?? "—"}</span>, wide: true },
          { label: "首次", value: formatTime(selectedCounterparty.first_seen) },
          { label: "最近", value: formatTime(selectedCounterparty.last_seen) },
        ]}
      />
    </InspectorDrawer>
  ) : null;

  return (
    <div className="cs-pro-page cs-pro-page--detail cs-pro-page--address">
      <BackLink to="/explorer" label="返回地址与交易查询" />
      <CsPageHeader
        className="cs-detail-header"
        breadcrumb={[{ label: "地址与交易查询", to: "/explorer" }, { label: "地址详情" }]}
        title="地址详情"
        status={<StatusBadge tone="info">{subject?.type || "address"}</StatusBadge>}
        subtitle={
          <div className="cs-objecthead">
            <div className="cs-objecthead__top">
              <span className="cs-objecthead__kind">{chain || "链未知"}</span>
              <span className="cs-objecthead__value">
                <HashValue value={fullAddress} label="地址" wrap />
              </span>
            </div>
            <MetaLine
              items={[
                { label: "数据来源", value: overview.data?.source?.provider || "—" },
                { label: "数据时间", value: formatTime(overview.data?.source?.as_of) },
                { label: "Coverage", value: coverageItems.length + " 个 dataset" },
              ]}
            />
          </div>
        }
        primaryAction={
          <Button size="sm" variant="primary" onClick={() => void copy(fullAddress, "地址")}>
            复制地址
          </Button>
        }
        secondaryAction={
          <Button size="sm" aria-label="地址数据说明" disabled={!overview.data} onClick={showCaliber}>
            ⓘ
          </Button>
        }
      />
      {/* Ant Tabs 提供键盘与 ink-bar；label 仍是可复制、可刷新的真实深链接。 */}
      <Tabs
        className="cs-detail-tabs"
        aria-label="地址详情分区"
        activeKey={tab}
        animated={{ inkBar: true, tabPane: false }}
        items={detailTabs}
        onChange={selectTab}
      />
      <SplitPane
        className="cs-detail-split"
        storageKey="cs-address-inspector-width"
        asideLabel="检查器"
        defaultAsideWidth={360}
        minAsideWidth={300}
        maxAsideWidth={640}
        /* 默认不显示侧栏；点击活动行或对手方行时才展开详情。 */
        aside={inspector}
        main={<div className="cs-bento-stack cs-detail-content">{content}</div>}
      />
    </div>
  );
}
