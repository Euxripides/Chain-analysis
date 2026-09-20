/**
 * 代币选择器（对齐参考站点的"代币"筛选控件）。
 *
 * 数据全部来自**当前页已取到的行**，不新增接口：搜索按符号/全称/合约地址子串匹配，
 * 热门代币按本页出现频次排序，历史搜索存 localStorage（最近 5 条）。
 * 纯控件：确认时才把值交给父组件写 URL。
 */
import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@chain/ui";

import type { ActivityRow } from "./address-detail.types";

const HISTORY_KEY = "uc.address.token-history";
const HISTORY_MAX = 5;

function readHistory(): string[] {
  try {
    const raw = window.localStorage.getItem(HISTORY_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : [];
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((item): item is string => typeof item === "string").slice(0, HISTORY_MAX);
  } catch {
    return [];
  }
}

function writeHistory(value: string): string[] {
  const next = [value, ...readHistory().filter((item) => item !== value)].slice(0, HISTORY_MAX);
  try {
    window.localStorage.setItem(HISTORY_KEY, JSON.stringify(next));
  } catch {
    /* 隐私模式下写不进去：历史搜索退化为本次会话内有效。 */
  }
  return next;
}

export function TokenPicker({
  rows,
  value,
  onConfirm,
  onReset,
}: {
  rows: ActivityRow[];
  value: string;
  onConfirm: (token: string) => void;
  onReset: () => void;
}) {
  const detailsRef = useRef<HTMLDetailsElement>(null);
  const [draft, setDraft] = useState(value);
  const [history, setHistory] = useState<string[]>([]);

  useEffect(() => {
    setDraft(value);
  }, [value]);

  useEffect(() => {
    setHistory(readHistory());
  }, []);

  /** 本页代币候选：符号 + 全称 + 合约地址，按出现次数排序（"热门代币"）。 */
  const candidates = useMemo(() => {
    const counter = new Map<string, { token: string; label: string; hits: number }>();
    for (const row of rows) {
      const token = row.token_symbol || row.token_address || "";
      if (!token) continue;
      const key = token.toLowerCase();
      const current = counter.get(key);
      if (current) {
        current.hits += 1;
        continue;
      }
      counter.set(key, { token, label: row.token_name || row.token_address || token, hits: 1 });
    }
    return [...counter.values()].sort((left, right) => right.hits - left.hits).slice(0, 12);
  }, [rows]);

  const keyword = draft.trim().toLowerCase();
  const matched = keyword
    ? candidates.filter(
        (item) =>
          item.token.toLowerCase().includes(keyword) ||
          item.label.toLowerCase().includes(keyword) ||
          (item.token && item.token.toLowerCase().includes(keyword)),
      )
    : candidates;

  const confirm = (token: string) => {
    const next = token.trim();
    if (next) setHistory(writeHistory(next));
    onConfirm(next);
    if (detailsRef.current) detailsRef.current.open = false;
  };

  const reset = () => {
    setDraft("");
    onReset();
    if (detailsRef.current) detailsRef.current.open = false;
  };

  const rowStyle = { display: "flex", justifyContent: "space-between", gap: 8, width: "100%" } as const;

  return (
    <details ref={detailsRef} style={{ position: "relative", flex: "0 1 240px", minWidth: 0 }}>
      <summary
        className="btn btn--sm btn--ghost"
        style={{ cursor: "pointer", listStyle: "none", display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}
      >
        <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          {value ? "代币：" + value : "全部代币"}
        </span>
      </summary>
      <div
        style={{
          position: "absolute",
          zIndex: 30,
          top: "calc(100% + 4px)",
          left: 0,
          width: 320,
          padding: "var(--sp-3, 12px)",
          display: "flex",
          flexDirection: "column",
          gap: 10,
          background: "var(--bg-surface, #fff)",
          border: "1px solid var(--border-default, #e2e8f0)",
          borderRadius: 8,
          boxShadow: "0 12px 32px rgba(15, 23, 42, 0.16)",
        }}
      >
        <input
          className="input"
          aria-label="搜索代币"
          placeholder="搜索代币符号 / 全称 / 合约地址"
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              confirm(draft);
            }
          }}
        />
        {history.length > 0 ? (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            <span className="s4-list__sub">历史搜索</span>
            <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
              {history.map((item) => (
                <Button key={item} size="sm" variant="ghost" onClick={() => confirm(item)}>
                  {item}
                </Button>
              ))}
            </div>
          </div>
        ) : null}
        <div style={{ display: "flex", flexDirection: "column", gap: 4, maxHeight: 220, overflowY: "auto" }}>
          <span className="s4-list__sub">{keyword ? "搜索结果" : "热门代币（本页频次）"}</span>
          {matched.length === 0 ? (
            <span className="s4-list__sub">没有匹配的代币，可直接回车按输入值筛选。</span>
          ) : (
            matched.map((item) => (
              <button
                key={item.token}
                type="button"
                className="btn btn--sm btn--ghost"
                style={rowStyle}
                onClick={() => confirm(item.token)}
              >
                <span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{item.token}</span>
                <span className="s4-list__sub" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                  {item.label === item.token ? item.hits + " 笔" : item.label}
                </span>
              </button>
            ))
          )}
        </div>
        <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          <Button size="sm" variant="ghost" onClick={reset}>
            重置
          </Button>
          <Button size="sm" variant="primary" onClick={() => confirm(draft)}>
            确认
          </Button>
        </div>
      </div>
    </details>
  );
}
