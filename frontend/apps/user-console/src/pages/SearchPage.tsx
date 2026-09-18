import { useEffect, useMemo, useRef, useState } from "react";
import type { ComponentType, FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { FileText, FolderOpen, Hash, ListChecks, ScrollText, SlidersHorizontal } from "lucide-react";
import {
  Card,
  EmptyState,
  ErrorState,
  FilterBar,
  FilterChip,
  HashValue,
  InspectorDrawer,
  KeyValueList,
  PageHeader,
  PageSection,
  SearchInput,
  SkeletonRows,
  SplitPane,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { useApi } from "../sprint5/common";
import { Highlight } from "../components/command/CommandPalette";

/**
 * §12.3 / USER-009 全局搜索。
 *
 * 表现层重构，数据层保持原样：
 * - 250ms debounce；输入变化触发 useApi 重建，旧请求由 AbortController 取消（useApi 内部）。
 * - 请求路径 /api/v2/search、参数 q 与 type、当前工作区限制全部不变。
 * - 结果按服务端 groups 分组展示；点击结果跳转 hit.url。
 * - 错误 / 说明只走 Dialog，正文只保留字段标签、表头与状态。
 *
 * 本次新增的表现层能力：
 * - 顶部大型搜索输入（SearchInput big）；深链接（?q=…）已带查询时不抢焦点。
 * - 结果按案件 / 调查 / 地址 / 交易 / 报告 / 任务分组，桌面为列表 + 右侧快速预览。
 * - 搜索词在可见文本中用 <mark> 高亮（纯文本切分，绝不注入未清洗 HTML）。
 */

interface SearchHit {
  type: string;
  id: string;
  title: string;
  subtitle: string;
  chain?: string;
  url: string;
}

interface SearchData {
  query: string;
  items: SearchHit[];
  groups: Record<string, SearchHit[]>;
  scopes: string[];
  caliber: Record<string, unknown>;
}

/** 请求参数用的类型集合：顺序即服务端收到的 type 顺序，**不得改动**。 */
const SEARCH_TYPES = ["case", "investigation", "job", "report", "address", "transaction"] as const;
type SearchType = (typeof SEARCH_TYPES)[number];

/** 展示顺序：与请求参数顺序解耦，只影响界面分组先后。 */
const DISPLAY_ORDER: SearchType[] = ["case", "investigation", "address", "transaction", "report", "job"];

const TYPE_LABEL: Record<SearchType, string> = {
  case: "案件",
  investigation: "调查",
  job: "任务",
  report: "报告",
  address: "地址",
  transaction: "交易",
};

/** 类型图标：形状通道，灰度下也能区分结果类别（文字标签始终同时存在）。 */
type IconProps = { size?: number; "aria-hidden"?: boolean };
const TYPE_ICON: Record<SearchType, ComponentType<IconProps>> = {
  case: FolderOpen,
  investigation: SlidersHorizontal,
  job: ListChecks,
  report: ScrollText,
  address: Hash,
  transaction: FileText,
};

function toneForType(type: string): "info" | "neutral" {
  return type === "address" || type === "transaction" ? "info" : "neutral";
}

function typeLabel(type: string): string {
  return TYPE_LABEL[type as SearchType] ?? type;
}

function TypeIcon({ type }: { type: string }) {
  const Icon = TYPE_ICON[type as SearchType] ?? FileText;
  /* `aria-hidden` 用布尔简写：TYPE_ICON 的取值是多个 lucide 组件的联合，
     字符串 "true" 在联合重载下会被判定为不可赋值。布尔写法在所有分支上等价。 */
  return <Icon size={13} aria-hidden />;
}

function hitKey(hit: SearchHit): string {
  return hit.type + ":" + hit.id + ":" + hit.url;
}

/** 断点判定：<900px 视为窄屏（蓝图 §13）。首帧即取真实值，避免布局跳动。 */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia(query).matches,
  );
  useEffect(() => {
    const list = window.matchMedia(query);
    const sync = () => setMatches(list.matches);
    sync();
    list.addEventListener("change", sync);
    return () => list.removeEventListener("change", sync);
  }, [query]);
  return matches;
}

export default function SearchPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const [searchParams] = useSearchParams();
  /** 深链接查询：只在首帧读取，用来预填输入并决定是否自动聚焦。 */
  const deepLinkRef = useRef(searchParams.get("q") ?? "");
  const [input, setInput] = useState(deepLinkRef.current);
  const [debounced, setDebounced] = useState(deepLinkRef.current.trim());
  const [groupFilter, setGroupFilter] = useState<string | null>(null);
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const lastErrorRef = useRef("");
  const wide = useMediaQuery("(min-width: 900px)");

  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(input.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [input]);

  const activeQuery = debounced.length >= 2 ? debounced : null;
  const { data, loading, error, reload } = useApi<SearchData>(
    activeQuery ? "/api/v2/search" : null,
    [activeQuery],
    activeQuery ? { q: activeQuery, type: SEARCH_TYPES.join(",") } : undefined,
  );
  // path 为空时 useApi 不会主动清空上一次的 error，这里只在有有效查询时消费错误。
  const activeError = activeQuery ? error : null;

  useEffect(() => {
    if (!activeError) {
      lastErrorRef.current = "";
      return;
    }
    if (lastErrorRef.current === activeError) return;
    lastErrorRef.current = activeError;
    dialog.notify("error", "搜索失败", activeError);
  }, [activeError, dialog]);

  const groups = useMemo(() => {
    if (!data) return [];
    return DISPLAY_ORDER.map((type) => ({
      type,
      label: TYPE_LABEL[type],
      items: data.groups?.[type] ?? data.items.filter((hit) => hit.type === type),
    })).filter((group) => group.items.length > 0);
  }, [data]);

  /** 查询词变化后，分组视图与快速预览都可能指向上一批结果：一并重置。 */
  useEffect(() => {
    setGroupFilter(null);
    setActiveKey(null);
  }, [activeQuery]);

  const items = data?.items ?? [];
  const scopes = data?.scopes ?? [];
  const visibleGroups = groupFilter ? groups.filter((group) => group.type === groupFilter) : groups;
  const activeHit = useMemo(
    () => (activeKey ? items.find((hit) => hitKey(hit) === activeKey) ?? null : null),
    [activeKey, items],
  );

  const trimmed = input.trim();
  const searching = loading || (trimmed.length >= 2 && activeQuery === null);
  const subtitle = data
    ? `共 ${items.length} 条${scopes.length > 0 ? ` · 范围 ${scopes.map((scope) => typeLabel(scope)).join("、")}` : ""}`
    : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setDebounced(input.trim());
  };

  const openHit = (hit: SearchHit) => {
    navigate(hit.url);
  };

  const explain = () => {
    dialog.notify(
      "info",
      "搜索说明",
      "搜索由服务端按当前账号权限裁剪：案件、调查、任务、报告来自控制面存储；地址、交易来自本地索引。服务端不会为了搜索触发 Provider 请求；未出现的类别表示当前范围内没有可读结果。",
      [
        { label: "请求路径", value: "/api/v2/search" },
        { label: "最小长度", value: "2 个字符" },
        { label: "覆盖类型", value: SEARCH_TYPES.map((type) => TYPE_LABEL[type]).join(" / ") },
        { label: "分组筛选", value: "仅对本次已返回的结果做视图过滤，不重复请求服务端" },
      ],
    );
  };

  const resultBody = () => {
    if (trimmed.length === 0) {
      return (
        <Card flush>
          <EmptyState title="输入关键词开始搜索" body="至少 2 个字符。" />
        </Card>
      );
    }
    if (trimmed.length < 2) {
      return (
        <Card flush>
          <EmptyState title="至少 2 个字符" body={`当前 ${trimmed.length} 个字符。`} />
        </Card>
      );
    }
    if (searching) {
      return (
        <Card flush>
          <SkeletonRows rows={5} columns={3} />
        </Card>
      );
    }
    if (activeError) {
      return (
        <Card flush>
          <ErrorState title="搜索失败" message="加载失败，请重试。" onRetry={() => void reload()} />
        </Card>
      );
    }
    if (items.length === 0) {
      return (
        <Card flush>
          <EmptyState title="没有匹配结果" body="可以缩短关键词，或确认对象属于当前工作区。" />
        </Card>
      );
    }
    if (visibleGroups.length === 0) {
      return (
        <Card flush>
          <EmptyState
            title="该分组没有结果"
            body="清除分组筛选即可查看其它类别。"
            action={
              <button type="button" className="btn btn--sm" onClick={() => setGroupFilter(null)}>
                查看全部分组
              </button>
            }
          />
        </Card>
      );
    }
    return (
      <>
        {visibleGroups.map((group) => (
          <PageSection key={group.type} title={group.label} count={group.items.length + " 条"}>
            <div className="cs-rowcards cs-stagger">
              {group.items.map((hit) => (
                <div
                  className="cs-rowcard"
                  key={hitKey(hit)}
                  style={
                    activeHit && hitKey(activeHit) === hitKey(hit)
                      ? { borderColor: "var(--interactive)" }
                      : undefined
                  }
                  onClick={
                    wide
                      ? undefined
                      : () => {
                          openHit(hit);
                        }
                  }
                >
                  <div className="cs-rowcard__head">
                    <span aria-hidden="true" style={{ color: "var(--text-secondary)", display: "inline-flex" }}>
                      <TypeIcon type={hit.type} />
                    </span>
                    <a
                      className="cs-rowcard__title"
                      href={hit.url}
                      title={hit.title || hit.id}
                      onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        openHit(hit);
                      }}
                    >
                      <Highlight text={hit.title || hit.id} query={activeQuery ?? ""} />
                    </a>
                    <StatusBadge tone={toneForType(hit.type)}>{typeLabel(hit.type)}</StatusBadge>
                  </div>
                  {hit.subtitle || hit.chain ? (
                    <div className="cs-rowcard__meta">
                      {hit.subtitle ? (
                        <span>
                          <Highlight text={hit.subtitle} query={activeQuery ?? ""} />
                        </span>
                      ) : null}
                      {hit.chain ? <span className="mono">{hit.chain}</span> : null}
                    </div>
                  ) : null}
                  {/* 复制按钮属于行内操作：不能让点击冒泡到"整卡进入详情"的移动端行为。 */}
                  <div className="cs-rowcard__meta" onClick={(event) => event.stopPropagation()}>
                    <HashValue value={hit.id} label={typeLabel(hit.type) + "标识"} />
                  </div>
                  <div className="cs-rowcard__actions">
                    {wide ? (
                      <button
                        type="button"
                        className="btn btn--sm"
                        aria-pressed={activeHit ? hitKey(activeHit) === hitKey(hit) : false}
                        onClick={() => setActiveKey(hitKey(hit))}
                      >
                        快速预览
                      </button>
                    ) : null}
                    <a
                      className="btn btn--sm btn--ghost"
                      href={hit.url}
                      onClick={(event) => {
                        event.preventDefault();
                        event.stopPropagation();
                        openHit(hit);
                      }}
                    >
                      打开
                    </a>
                  </div>
                </div>
              ))}
            </div>
          </PageSection>
        ))}
      </>
    );
  };

  const aside = wide ? (
    <InspectorDrawer
      title={activeHit ? activeHit.title || activeHit.id : "快速预览"}
      subtitle={activeHit ? typeLabel(activeHit.type) : undefined}
      ariaLabel="搜索结果快速预览"
      onClose={activeHit ? () => setActiveKey(null) : undefined}
      actions={
        activeHit ? (
          <a
            className="btn btn--primary btn--sm"
            href={activeHit.url}
            onClick={(event) => {
              event.preventDefault();
              openHit(activeHit);
            }}
          >
            打开详情
          </a>
        ) : null
      }
    >
      {activeHit ? (
        <KeyValueList
          items={[
            { label: "类型", value: typeLabel(activeHit.type) },
            { label: "标题", value: activeHit.title || activeHit.id },
            { label: "摘要", value: activeHit.subtitle },
            { label: "链", value: activeHit.chain },
            { label: "完整标识", value: <HashValue value={activeHit.id} wrap label="对象标识" />, wide: true },
            { label: "目标地址", value: <HashValue value={activeHit.url} wrap copyable={false} />, wide: true },
          ]}
        />
      ) : (
        <EmptyState title="未选择结果" body="在左侧结果上点击“快速预览”，这里显示该对象的完整标识与入口。" />
      )}
    </InspectorDrawer>
  ) : undefined;

  return (
    <>
      <PageHeader
        title="全局搜索"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "全局搜索" }]}
        subtitle={subtitle}
        secondaryAction={
          <button type="button" className="btn btn--sm" onClick={explain}>
            说明
          </button>
        }
      />
      <form className="cs-search-hero" role="search" onSubmit={submit}>
        <div className="cs-search-hero__row">
          <SearchInput
            className="cs-search-hero__input"
            value={input}
            onChange={setInput}
            onSubmit={(value) => setDebounced(value)}
            label="搜索关键词"
            placeholder="案件 / 调查 / 任务 / 报告 / 地址 / 交易"
            minLength={2}
            autoFocus={deepLinkRef.current === ""}
            big
            busy={searching}
          />
          <button className="btn btn--primary" type="submit" disabled={trimmed.length < 2 || loading}>
            {loading ? "搜索中…" : "搜索"}
          </button>
        </div>
      </form>
      {groups.length > 0 ? (
        <FilterBar
          ariaLabel="结果分组"
          showClear={groupFilter !== null}
          onClear={() => setGroupFilter(null)}
        >
          <FilterChip active={groupFilter === null} onClick={() => setGroupFilter(null)}>
            全部
          </FilterChip>
          {groups.map((group) => (
            <FilterChip
              key={group.type}
              active={groupFilter === group.type}
              onClick={() => setGroupFilter(group.type)}
            >
              {group.label} {group.items.length}
            </FilterChip>
          ))}
        </FilterBar>
      ) : null}
      <SplitPane
        main={resultBody()}
        aside={aside}
        storageKey="cs.search.preview.width"
        defaultAsideWidth={360}
        asideLabel="快速预览"
      />
    </>
  );
}
