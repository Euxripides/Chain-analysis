import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Command, CornerDownLeft, FileText, FolderOpen, Hash, Plus, Search, SlidersHorizontal } from "lucide-react";
import { ApiError, useAuth } from "@chain/api-client";
import { useDialog } from "@chain/ui";
import { baseNavGroups, requiredPermission } from "../../app/navigation";
import { readRecentObjects } from "./recentObjects";

/**
 * 全局命令面板（蓝图 §5.3）
 *
 * 硬性要求逐条落地：
 *   - 快捷键 Ctrl+K / Cmd+K，**不替代**现有导航和页面操作。
 *   - 命令来源由权限和现有路由生成；无权限命令不出现。
 *   - 输入 250ms debounce；旧请求用 AbortController 取消。
 *   - 键盘上下选择、Enter 执行、Escape 关闭。
 *   - 结果分组显示类型、标题、完整标识摘要和目标页面。
 *   - 移动端用全屏 Dialog 呈现。
 *   - 搜索失败 / 无结果在**面板内部**呈现，不使用页面横幅。
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
  items: SearchHit[];
  groups: Record<string, SearchHit[]>;
  scopes: string[];
}

interface CommandItem {
  key: string;
  group: string;
  title: string;
  /** 完整标识摘要：不截断业务标识，只在视觉上省略。 */
  detail?: string;
  target?: string;
  icon?: React.ReactNode;
  run?: () => void;
}

const SEARCH_TYPES = ["case", "investigation", "job", "report", "address", "transaction"] as const;

const TYPE_LABEL: Record<string, string> = {
  case: "案件",
  investigation: "调查",
  job: "任务",
  report: "报告",
  address: "地址",
  transaction: "交易",
};

/** 高亮搜索词：**只做纯文本切分，绝不注入未清洗 HTML**（蓝图 §12.3）。 */
export function Highlight({ text, query }: { text: string; query: string }) {
  const q = query.trim();
  if (!q) return <>{text}</>;
  const lower = text.toLowerCase();
  const needle = q.toLowerCase();
  const parts: React.ReactNode[] = [];
  let cursor = 0;
  let index = lower.indexOf(needle);
  let guard = 0;
  while (index >= 0 && guard < 40) {
    if (index > cursor) parts.push(text.slice(cursor, index));
    parts.push(
      <mark className="cs-mark" key={"m" + index}>
        {text.slice(index, index + needle.length)}
      </mark>,
    );
    cursor = index + needle.length;
    index = lower.indexOf(needle, cursor);
    guard += 1;
  }
  if (cursor < text.length) parts.push(text.slice(cursor));
  return <>{parts}</>;
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const dialog = useDialog();
  const { client, can } = useAuth();
  const [query, setQuery] = useState("");
  const [debounced, setDebounced] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<string | null>(null);
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const previousFocus = useRef<Element | null>(null);

  /* 250ms debounce：输入停止后才发请求（§5.3）。 */
  useEffect(() => {
    const timer = window.setTimeout(() => setDebounced(query.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [query]);

  /* 打开时聚焦输入并记录原来的焦点，关闭时归还（§10.2 焦点恢复）。 */
  useEffect(() => {
    if (!open) return;
    previousFocus.current = document.activeElement;
    const timer = window.setTimeout(() => inputRef.current?.focus(), 0);
    return () => {
      window.clearTimeout(timer);
      if (previousFocus.current instanceof HTMLElement && document.contains(previousFocus.current)) {
        previousFocus.current.focus();
      }
    };
  }, [open]);

  useEffect(() => {
    if (!open) {
      setQuery("");
      setDebounced("");
      setHits([]);
      setSearchError(null);
      setActive(0);
    }
  }, [open]);

  /* 检索：仅在查询长度 ≥2 时发请求；旧请求随 effect 清理被 abort。 */
  useEffect(() => {
    if (!open) return;
    if (debounced.length < 2) {
      setHits([]);
      setSearchError(null);
      setSearching(false);
      return;
    }
    const controller = new AbortController();
    let alive = true;
    setSearching(true);
    setSearchError(null);
    void client
      .get<SearchData>("/api/v2/search", {
        query: { q: debounced, type: SEARCH_TYPES.join(",") },
        signal: controller.signal,
      })
      .then((res) => {
        if (!alive) return;
        setHits(res.data?.items ?? []);
      })
      .catch((err: unknown) => {
        if (!alive || controller.signal.aborted) return;
        /* 用户主动取消不算错误，只有真实失败才在面板内提示。 */
        if (err instanceof DOMException && err.name === "AbortError") return;
        setSearchError(err instanceof ApiError ? err.userMessage : "搜索请求失败");
        setHits([]);
      })
      .finally(() => {
        if (alive) setSearching(false);
      });
    return () => {
      alive = false;
      controller.abort();
    };
  }, [client, debounced, open]);

  /* 命令项：页面跳转 / 创建动作 / 最近访问。全部经过权限裁剪。 */
  const baseItems = useMemo<CommandItem[]>(() => {
    const items: CommandItem[] = [];
    for (const group of baseNavGroups) {
      for (const entry of group.items) {
        const permission = requiredPermission(entry.to);
        if (!permission || !can(permission)) continue;
        items.push({
          key: "nav:" + entry.to,
          group: "跳转页面",
          title: entry.label,
          detail: entry.to,
          target: entry.to,
          icon: <FolderOpen size={15} />,
          run: () => navigate(entry.to),
        });
      }
    }
    if (can("case:create")) {
      items.push({
        key: "create:case",
        group: "创建",
        title: "新建案件",
        detail: "/cases/new",
        icon: <Plus size={15} />,
        run: () => navigate("/cases/new"),
      });
    }
    if (can("investigation:create")) {
      items.push({
        key: "create:investigation",
        group: "创建",
        title: "发起调查",
        detail: "/investigations/new",
        icon: <SlidersHorizontal size={15} />,
        run: () => navigate("/investigations/new"),
      });
    }
    if (can("download:create")) {
      items.push({
        key: "create:download",
        group: "创建",
        title: "创建数据下载",
        detail: "/downloads/new",
        icon: <Plus size={15} />,
        run: () => navigate("/downloads/new"),
      });
    }
    for (const recent of readRecentObjects()) {
      if (!can(requiredPermission(recent.path) ?? "")) continue;
      items.push({
        key: "recent:" + recent.path,
        group: "最近访问",
        title: recent.label,
        detail: recent.path,
        icon: <FileText size={15} />,
        run: () => navigate(recent.path),
      });
    }
    return items;
  }, [can, navigate]);

  const commands = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return baseItems;
    return baseItems.filter((item) => (item.title + " " + (item.detail ?? "")).toLowerCase().includes(q));
  }, [baseItems, query]);

  const hitItems = useMemo<CommandItem[]>(() => {
    return hits.map((hit) => ({
      key: "hit:" + hit.type + ":" + hit.id,
      group: TYPE_LABEL[hit.type] ?? hit.type,
      title: hit.title || hit.id,
      /* 完整标识：命令面板里不截断底层值（蓝图 §2.5）。 */
      detail: hit.subtitle ? hit.subtitle + " · " + hit.id : hit.id,
      target: hit.url,
      icon: hit.type === "address" || hit.type === "transaction" ? <Hash size={15} /> : <FileText size={15} />,
      run: () => navigate(hit.url),
    }));
  }, [hits, navigate]);

  const flat = useMemo(() => [...hitItems, ...commands], [commands, hitItems]);
  const grouped = useMemo(() => {
    const order: string[] = [];
    const map = new Map<string, CommandItem[]>();
    for (const item of flat) {
      if (!map.has(item.group)) {
        map.set(item.group, []);
        order.push(item.group);
      }
      map.get(item.group)!.push(item);
    }
    return order.map((group) => ({ group, items: map.get(group)! }));
  }, [flat]);

  /* 结果变化后把高亮重置到第一项，避免高亮停在已消失的索引上。 */
  useEffect(() => {
    setActive(0);
  }, [debounced, query]);

  const execute = useCallback(
    (item: CommandItem | undefined) => {
      if (!item) return;
      onClose();
      item.run?.();
    },
    [onClose],
  );

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === "Escape") {
      event.preventDefault();
      onClose();
      return;
    }
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((i) => Math.min(flat.length - 1, i + 1));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((i) => Math.max(0, i - 1));
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      execute(flat[active]);
    }
  };

  /* 高亮项滚动进视野：只在需要时滚动，不做持续动画。 */
  useEffect(() => {
    const node = listRef.current?.querySelector<HTMLElement>('[data-active="true"]');
    node?.scrollIntoView({ block: "nearest" });
  }, [active, flat.length]);

  if (!open) return null;

  const empty = flat.length === 0;

  return (
    <div
      className="cs-command-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="cs-command" role="dialog" aria-modal="true" aria-label="命令面板" onKeyDown={onKeyDown}>
        <div className="cs-command__search">
          <Search size={18} aria-hidden="true" style={{ color: "var(--text-secondary)", flex: "none" }} />
          <input
            ref={inputRef}
            className="cs-command__input"
            role="combobox"
            aria-expanded={!empty}
            aria-controls="command-list"
            aria-activedescendant={flat[active] ? "command-item-" + active : undefined}
            aria-label="搜索或跳转"
            placeholder="搜索地址、交易哈希、案件、调查，或输入页面名称…"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            autoComplete="off"
          />
          {searching ? <span className="cs-section__count">搜索中…</span> : null}
        </div>

        <div className="cs-command__list" id="command-list" role="listbox" ref={listRef}>
          {searchError ? (
            <div className="cs-command__empty">
              搜索失败：{searchError}
              <div style={{ marginTop: "var(--sp-3)" }}>
                <button type="button" className="btn btn--sm" onClick={() => dialog.notify("error", "搜索失败", searchError)}>
                  查看详情
                </button>
              </div>
            </div>
          ) : null}
          {!searchError && empty ? (
            <div className="cs-command__empty">
              {debounced.length >= 2 ? "没有匹配结果。可以缩短关键词，或确认对象属于当前工作区。" : "没有可执行的命令。"}
            </div>
          ) : null}
          {!searchError
            ? grouped.map((group) => (
                <div key={group.group}>
                  <div className="cs-command__group">{group.group}</div>
                  {group.items.map((item) => {
                    const index = flat.indexOf(item);
                    return (
                      <button
                        key={item.key}
                        type="button"
                        id={"command-item-" + index}
                        role="option"
                        aria-selected={index === active}
                        className="cs-command__item"
                        data-active={index === active ? "true" : undefined}
                        onMouseEnter={() => setActive(index)}
                        onClick={() => execute(item)}
                      >
                        <span aria-hidden="true" style={{ color: "var(--text-secondary)", flex: "none" }}>
                          {item.icon}
                        </span>
                        <span className="cs-command__itemmain">
                          <span className="cs-command__itemtitle">
                            <Highlight text={item.title} query={query} />
                          </span>
                          {item.detail ? <span className="cs-command__itemsub">{item.detail}</span> : null}
                        </span>
                        {item.target ? <span className="cs-command__kind">{item.target}</span> : null}
                      </button>
                    );
                  })}
                </div>
              ))
            : null}
        </div>

        <div className="cs-command__foot">
          <span>
            <CornerDownLeft size={12} /> 执行
          </span>
          <span>↑ ↓ 选择</span>
          <span>Esc 关闭</span>
          <span style={{ marginLeft: "auto" }}>
            <Command size={12} /> 输入 ≥2 字符可搜索服务端对象
          </span>
        </div>
      </div>
    </div>
  );
}
