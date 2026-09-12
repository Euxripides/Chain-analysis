import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { BackLink, Card, EmptyState, ErrorState, PageHeader, SkeletonRows, StatusBadge, useDialog } from "@chain/ui";
import { useApi } from "../sprint5/common";

/**
 * §21.5 / USER-009 全局搜索。
 * - 250ms debounce；输入变化触发 useApi 重建，旧请求由 AbortController 取消。
 * - 结果按服务端 groups 分组展示；点击结果跳转 hit.url。
 * - 错误 / 说明只走 Dialog，正文只保留字段标签、表头与空状态。
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

const SEARCH_TYPES = ["case", "investigation", "job", "report", "address", "transaction"] as const;
type SearchType = (typeof SEARCH_TYPES)[number];

const TYPE_LABEL: Record<SearchType, string> = {
  case: "案件",
  investigation: "调查",
  job: "任务",
  report: "报告",
  address: "地址",
  transaction: "交易",
};

function toneForType(type: string): "info" | "neutral" {
  return type === "address" || type === "transaction" ? "info" : "neutral";
}

export default function SearchPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const [input, setInput] = useState("");
  const [debounced, setDebounced] = useState("");
  const lastErrorRef = useRef("");

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
    return SEARCH_TYPES.map((type) => ({
      type,
      label: TYPE_LABEL[type],
      items: data.groups?.[type] ?? data.items.filter((hit) => hit.type === type),
    })).filter((group) => group.items.length > 0);
  }, [data]);

  const items = data?.items ?? [];
  const scopes = data?.scopes ?? [];
  const subtitle = data
    ? `共 ${items.length} 条${scopes.length > 0 ? ` · 范围 ${scopes.map((scope) => TYPE_LABEL[scope as SearchType] ?? scope).join("、")}` : ""}`
    : undefined;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setDebounced(input.trim());
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
      ],
    );
  };

  return (
    <>
      <BackLink to="/home" label="返回工作台" />
      <PageHeader
        title="全局搜索"
        subtitle={subtitle}
        actions={
          <button type="button" className="btn btn--sm" onClick={explain}>
            说明
          </button>
        }
      />
      <Card>
        <form className="toolbar" role="search" onSubmit={submit}>
          <input
            className="input"
            style={{ flex: "1 1 240px", maxWidth: 460, minWidth: 0 }}
            aria-label="搜索关键词"
            value={input}
            autoFocus
            onChange={(event) => setInput(event.target.value)}
            placeholder="案件 / 调查 / 任务 / 报告 / 地址 / 交易"
          />
          <button className="btn btn--primary" type="submit" disabled={input.trim().length < 2 || loading}>
            {loading ? "搜索中…" : "搜索"}
          </button>
        </form>
      </Card>
      {loading ? (
        <Card flush>
          <SkeletonRows rows={5} columns={3} />
        </Card>
      ) : activeError ? (
        <Card flush>
          <ErrorState title="搜索失败" message="加载失败，请重试。" onRetry={() => void reload()} />
        </Card>
      ) : data === null ? (
        <Card flush>
          <EmptyState title="输入关键词开始搜索" body="至少 2 个字符。" />
        </Card>
      ) : items.length === 0 ? (
        <Card flush>
          <EmptyState title="没有匹配结果" body="可以缩短关键词，或确认对象属于当前工作区。" />
        </Card>
      ) : (
        groups.map((group) => (
          <Card
            key={group.type}
            flush
            title={
              <>
                {group.label}
                <span className="badge badge--neutral" style={{ marginLeft: 8 }}>
                  {group.items.length}
                </span>
              </>
            }
          >
            <ul className="s4-list" style={{ listStyle: "none", margin: 0, padding: 0 }}>
              {group.items.map((hit) => (
                <li className="s4-list__item" key={`${hit.type}-${hit.id}-${hit.url}`}>
                  <div className="s4-list__main">
                    <a
                      className="s4-list__title"
                      href={hit.url}
                      onClick={(event) => {
                        event.preventDefault();
                        navigate(hit.url);
                      }}
                    >
                      {hit.title || hit.id}
                    </a>
                    <div className="s4-list__sub">
                      {hit.subtitle ? <span>{hit.subtitle}</span> : null}
                      {hit.chain ? (
                        <span className="mono" style={{ marginLeft: hit.subtitle ? 8 : 0 }}>
                          {hit.chain}
                        </span>
                      ) : null}
                    </div>
                    <div
                      className="mono"
                      style={{ fontSize: "var(--fs-11)", color: "var(--c-slate)", overflowWrap: "anywhere" }}
                    >
                      {hit.id}
                    </div>
                  </div>
                  <StatusBadge tone={toneForType(hit.type)}>{TYPE_LABEL[hit.type as SearchType] ?? hit.type}</StatusBadge>
                </li>
              ))}
            </ul>
          </Card>
        ))
      )}
    </>
  );
}
