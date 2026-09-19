import {
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
import { EntityHash, CurrentBalanceTable, NetFlowTable, ActivityTable, CounterpartyTable, SemanticTable, AnalysisView, CoverageTable } from "./address-detail/tabs/AddressTables";

export default function AddressDetailPage() {
  const { chain = "", address = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const dialog = useDialog();
  const { client } = useAuth();
  // P1-1 第 3 项：活动是长表路径（实测 8.09s @ limit=200 全量窗口），默认收窄到最近 30 天。
  // 服务端的 Filter 一直支持 from_time/to_time（会写成 WHERE timestamp >=/<=），此前只是前端
  // 从不传、handler 也不透传，所以每次都是全量拉。选「全部」会退回较慢的全量查询。
  const [windowDays, setWindowDays] = useState(30);
  const [bulkRows, setBulkRows] = useState<ActivityRow[] | null>(null);
  const [bulkLoading, setBulkLoading] = useState(false);
  // 仅用于展示"已经真实拿到多少行"——服务端不提供总量，因此进度条保持 indeterminate。
  const [bulkLoaded, setBulkLoaded] = useState(0);
  // 右侧检查器：选中活动行 / 对手方行 → 行详情；未选中 → 数据来源与口径。
  const [selectedActivity, setSelectedActivity] = useState<ActivityRow | null>(null);
  const [selectedCounterparty, setSelectedCounterparty] = useState<CounterpartyRow | null>(null);
  const requestedTab = searchParams.get("tab") ?? "overview";
  const tab = TABS.some((item) => item.key === requestedTab) ? requestedTab : "overview";
  const cursor = searchParams.get("cursor") ?? "";
  const direction = searchParams.get("direction") ?? "";
  const token = searchParams.get("token") ?? "";
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
    () => (windowDays > 0 ? new Date(Date.now() - windowDays * 86_400_000).toISOString() : undefined),
    [windowDays],
  );
  const overview = useApi<OverviewData>(base ? base + "/overview" : null, [chain, address]);
  const assets = useApi<AssetsPayload>(tab === "assets" && base ? base + "/assets" : null, [chain, address, tab]);
  const activityActive = tab === "transactions" || tab === "transfers" || tab === "internal";
  const activity = useApi<{ items: ActivityRow[] }>(
    activityActive && base ? base + "/activity" : null,
    [chain, address, tab, cursor, direction, token, windowDays],
    activityActive
      ? {
          limit: 200,
          cursor: cursor || undefined,
          direction: direction || undefined,
          token: tab === "transfers" ? token || undefined : undefined,
          from_time: activityFromTime,
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
  const fullAddress = subject?.normalized_address || subject?.address || address;

  /** 数据来源与口径：Header 元信息与右侧检查器共用同一份服务端字段。 */
  const sourceItems: KeyValueEntry[] = [
    { label: "数据来源", value: overview.data?.source?.provider || "—" },
    { label: "as_of", value: formatTime(overview.data?.source?.as_of) },
    { label: "数据表", value: (overview.data?.source?.tables ?? []).join("，") || "—", wide: true },
    { label: "Coverage", value: coverageItems.length + " 个 dataset" },
    {
      label: "余额口径",
      value: (currentBlock?.source || "—") + " · is_current_balance=" + String(currentBlock?.is_current_balance ?? true),
    },
    {
      label: "净流口径",
      value: (flowBlock?.source || "—") + " · is_current_balance=" + String(flowBlock?.is_current_balance ?? false),
    },
    { label: "Coverage 来源", value: overview.data?.coverage?.source || "—", wide: true },
    { label: "规范化地址", value: subject?.normalized_address ? <HashValue value={subject.normalized_address} label="规范化地址" wrap /> : "—", wide: true },
  ];

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
        <PageSection title="概览指标" count="5 项">
          <div className="metric-grid cs-stagger">
            <MetricCard
              label="交易数"
              value={summary?.transaction_count ?? "—"}
              source="summary.transaction_count"
              sourceNote="来自服务端地址概览接口的 summary.transaction_count：该地址作为参与方的普通交易计数，范围受当前链与本地索引覆盖限制。"
            />
            <MetricCard
              label="Token Transfer"
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

  const activityTitle = tab === "transfers" ? "Token Transfer" : tab === "internal" ? "Internal" : "普通交易";

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
        ? rows.filter((row) => row.activity_type === "transfer")
        : tab === "internal"
          ? rows.filter((row) => row.activity_type === "internal_transfer")
          : rows.filter((row) => row.activity_type !== "transfer" && row.activity_type !== "internal_transfer");
    const tokenFilterActive = tab === "transfers" && Boolean(token);
    return (
      <PageSection title={activityTitle} count={filtered.length + " 行"}>
        {/* 筛选条件与请求参数一一对应，并且全部写进 URL（direction / token / 时间窗）。 */}
        <FilterBar
          ariaLabel="活动筛选"
          showClear
          clearLabel="重置"
          onClear={() => updateParams({ direction: null, token: null, cursor: null })}
        >
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
          <select
            className="select"
            style={{ flex: "0 1 150px", minWidth: 0 }}
            aria-label="时间窗筛选"
            value={String(windowDays)}
            onChange={(event) => {
              setWindowDays(Number(event.target.value));
              updateParams({ cursor: null });
            }}
          >
            <option value="7">最近 7 天</option>
            <option value="30">最近 30 天</option>
            <option value="90">最近 90 天</option>
            <option value="0">全部时间（较慢）</option>
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
              <Button
                size="sm"
                variant="primary"
                onClick={() => updateParams({ token: tokenDraft.trim() || null, cursor: null })}
              >
                应用
              </Button>
            </>
          ) : null}
          {tab === "internal" ? (
            <Button size="sm" variant="ghost" onClick={explainInternal}>
              说明
            </Button>
          ) : null}
          {direction ? (
            <FilterChip removeLabel="移除方向筛选" onRemove={() => updateParams({ direction: null, cursor: null })}>
              {"方向：" + (DIRECTION_LABEL[direction] ?? direction)}
            </FilterChip>
          ) : null}
          {tokenFilterActive ? (
            <FilterChip removeLabel="移除 Token 筛选" onRemove={() => updateParams({ token: null, cursor: null })}>
              {"Token：" + token}
            </FilterChip>
          ) : null}
        </FilterBar>
        <Card flush>
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
          <ActivityTable
            rows={bulkRows ?? filtered}
            chain={chain}
            loading={activity.loading || bulkLoading}
            emptyTitle={"没有 " + activityTitle + " 记录"}
            onSelectActivity={setSelectedActivity}
          />
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
                  {"退出全量视图（" + bulkRows.length + " 行）"}
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
                    加载全量（最多 10,000 行）
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
  ) : (
    <InspectorDrawer title="数据来源与口径" subtitle={overview.data?.source?.provider}>
      <KeyValueList items={sourceItems} />
    </InspectorDrawer>
  );

  return (
    <>
      <BackLink to="/explorer" label="返回地址与交易查询" />
      <CsPageHeader
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
                { label: "余额口径", value: currentBlock?.source || "—" },
                { label: "净流口径", value: flowBlock?.source || "—" },
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
          <Button size="sm" disabled={!overview.data} onClick={showCaliber}>
            口径说明
          </Button>
        }
      />
      {/*
        Tab 保持链接语义：它们是深链接（写进 URL、刷新可恢复、可复制分享），现有 E2E 也按
        role=link 定位。选中态由 .cs-tab[aria-current="page"] 表达（与按钮版
        .cs-tab[aria-selected="true"] 同一套视觉），不引入第二套样式。
      */}
      <nav className="cs-tabs" aria-label="地址详情分区">
        {TABS.map((item) => {
          const params = new URLSearchParams(searchParams);
          params.set("tab", item.key);
          params.delete("cursor");
          if (item.key !== "transfers") params.delete("token");
          const active = item.key === tab;
          return (
            <a
              key={item.key}
              className="cs-tab"
              href={"?" + params.toString()}
              aria-current={active ? "page" : undefined}
              onClick={(event) => {
                event.preventDefault();
                setSelectedActivity(null);
                setSelectedCounterparty(null);
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
      <SplitPane
        storageKey="cs-address-inspector-width"
        asideLabel="检查器"
        defaultAsideWidth={360}
        minAsideWidth={300}
        maxAsideWidth={640}
        aside={inspector}
        main={<div className="cs-bento-stack">{content}</div>}
      />
    </>
  );
}
