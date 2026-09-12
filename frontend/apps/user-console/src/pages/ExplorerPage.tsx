import { useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { BackLink, Card, EmptyState, ErrorState, PageHeader, StatusBadge, useDialog } from "@chain/ui";
import { formatTime, shortValue, useApi } from "../sprint5/common";

/**
 * U-EXP-01 地址与交易查询入口。
 * - 链列表来自 /api/v2/meta/chains，只展示 enabled。
 * - 输入框接受地址或交易哈希；交易哈希（0x+64hex / 64hex）跳交易详情，其余跳地址详情。
 * - 最近查询保存在 localStorage（最多 8 条）；清空走 ConfirmDialog。
 * - 所有说明/警告/确认只通过 Dialog 呈现。
 */

interface ChainItem {
  slug: string;
  name: string;
  type: string;
  enabled: boolean;
  supports_trace?: boolean;
}

interface ChainsData {
  items: ChainItem[];
}

interface RecentItem {
  chain: string;
  value: string;
  kind: "address" | "transaction";
  at: string;
}

const RECENT_KEY = "chainsight.explorer.recent.v1";
const MAX_RECENT = 8;
const TX_HASH_RE = /^(0x)?[0-9a-fA-F]{64}$/;
const EVM_ADDRESS_RE = /^0x[0-9a-fA-F]{40}$/;
const TRON_ADDRESS_RE = /^T[1-9A-HJ-NP-Za-km-z]{33}$/;

function readRecent(): RecentItem[] {
  try {
    const parsed = JSON.parse(window.localStorage.getItem(RECENT_KEY) ?? "[]") as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter(
        (item): item is RecentItem =>
          Boolean(item) &&
          typeof item === "object" &&
          typeof (item as RecentItem).chain === "string" &&
          typeof (item as RecentItem).value === "string" &&
          ((item as RecentItem).kind === "address" || (item as RecentItem).kind === "transaction"),
      )
      .slice(0, MAX_RECENT);
  } catch {
    return [];
  }
}

function writeRecent(items: RecentItem[]) {
  try {
    window.localStorage.setItem(RECENT_KEY, JSON.stringify(items.slice(0, MAX_RECENT)));
  } catch {
    // 隐私模式 / 配额不足时静默降级：查询本身仍然可用。
  }
}

function looksLikeAddress(value: string): boolean {
  return EVM_ADDRESS_RE.test(value) || TRON_ADDRESS_RE.test(value);
}

export default function ExplorerPage() {
  const navigate = useNavigate();
  const dialog = useDialog();
  const { data: chainsData, loading: chainsLoading, error: chainsError, reload: reloadChains } = useApi<ChainsData>("/api/v2/meta/chains");
  const chains = useMemo(() => (chainsData?.items ?? []).filter((item) => item.enabled), [chainsData]);
  const [chain, setChain] = useState("");
  const [input, setInput] = useState("");
  const [recent, setRecent] = useState<RecentItem[]>([]);
  const lastErrorRef = useRef("");

  useEffect(() => {
    setRecent(readRecent());
  }, []);

  useEffect(() => {
    if (!chain && chains.length > 0) setChain(chains[0].slug);
  }, [chain, chains]);

  useEffect(() => {
    if (!chainsError) {
      lastErrorRef.current = "";
      return;
    }
    if (lastErrorRef.current === chainsError) return;
    lastErrorRef.current = chainsError;
    dialog.notify("error", "链列表加载失败", chainsError);
  }, [chainsError, dialog]);

  const remember = (item: RecentItem) => {
    setRecent((prev) => {
      const next = [item, ...prev.filter((entry) => !(entry.chain === item.chain && entry.value === item.value))].slice(
        0,
        MAX_RECENT,
      );
      writeRecent(next);
      return next;
    });
  };

  const open = (rawValue: string, chainSlug?: string) => {
    const value = rawValue.trim();
    const slug = (chainSlug ?? chain).trim();
    if (!value) {
      dialog.notify("warning", "请输入地址或交易哈希");
      return;
    }
    if (!slug) {
      dialog.notify("warning", "请选择链", "链列表尚未加载，或当前没有已启用的链。");
      return;
    }
    if (TX_HASH_RE.test(value)) {
      const hash = value.replace(/^0x/i, "").toLowerCase();
      remember({ chain: slug, value, kind: "transaction", at: new Date().toISOString() });
      navigate(`/transactions/${encodeURIComponent(slug)}/${encodeURIComponent(hash)}`);
      return;
    }
    remember({ chain: slug, value, kind: "address", at: new Date().toISOString() });
    navigate(`/addresses/${encodeURIComponent(slug)}/${encodeURIComponent(value)}`);
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    open(input);
  };

  const removeOne = (target: RecentItem) => {
    setRecent((prev) => {
      const next = prev.filter((entry) => !(entry.chain === target.chain && entry.value === target.value));
      writeRecent(next);
      return next;
    });
  };

  const clearAll = async () => {
    if (recent.length === 0) return;
    const answer = await dialog.confirm({
      kind: "confirm",
      title: "清空最近查询？",
      body: "清空后本机不再保留这些查询记录。",
      details: [{ label: "条数", value: String(recent.length) }],
    });
    if (answer !== "confirm") return;
    setRecent([]);
    writeRecent([]);
  };

  const explain = () => {
    dialog.notify(
      "info",
      "查询说明",
      "地址 / 交易查询只读取本地索引，不会因打开页面而调用 Provider。Trace、深度解码等需要 Provider 的能力会在执行前单独确认，并需要 explorer:fetch 权限。地址形态由服务端做最终校验。",
      [
        { label: "链列表", value: "/api/v2/meta/chains" },
        { label: "本地记录", value: `localStorage · 最多 ${MAX_RECENT} 条` },
      ],
    );
  };

  const trimmed = input.trim();
  const shapeInvalid = trimmed.length > 0 && !looksLikeAddress(trimmed) && !TX_HASH_RE.test(trimmed);

  return (
    <>
      <BackLink to="/home" label="返回工作台" />
      <PageHeader
        title="地址与交易查询"
        subtitle={chains.length > 0 ? `已启用链：${chains.map((item) => item.slug).join(" / ")}` : undefined}
        actions={
          <button type="button" className="btn btn--sm" onClick={explain}>
            说明
          </button>
        }
      />
      {chainsError ? (
        <Card flush>
          <ErrorState title="链列表加载失败" message="加载失败，请重试。" onRetry={() => void reloadChains()} />
        </Card>
      ) : null}
      <Card title="查询">
        <form className="toolbar" onSubmit={submit}>
          <select
            className="select"
            style={{ flex: "0 1 200px", minWidth: 140 }}
            aria-label="选择链"
            value={chain}
            disabled={chainsLoading || chains.length === 0}
            onChange={(event) => setChain(event.target.value)}
          >
            {chains.length === 0 ? (
              <option value="">{chainsLoading ? "加载链…" : "没有已启用的链"}</option>
            ) : (
              chains.map((item) => (
                <option value={item.slug} key={item.slug}>
                  {item.name}（{item.slug}）
                </option>
              ))
            )}
          </select>
          <input
            className="input"
            style={{ flex: "1 1 260px", minWidth: 0 }}
            aria-label="地址或交易哈希"
            aria-invalid={shapeInvalid}
            value={input}
            onChange={(event) => setInput(event.target.value)}
            placeholder="0x 地址 / TRON 地址 / 交易哈希"
          />
          <button className="btn btn--primary" type="submit" disabled={trimmed.length === 0 || chains.length === 0}>
            查询
          </button>
        </form>
      </Card>
      <Card
        title="最近查询"
        flush
        actions={
          <button
            type="button"
            className="btn btn--sm btn--ghost"
            disabled={recent.length === 0}
            onClick={() => void clearAll()}
          >
            清空
          </button>
        }
      >
        {recent.length === 0 ? (
          <EmptyState title="暂无最近查询" body="查询记录只保存在本机浏览器，最多 8 条。" />
        ) : (
          <ul className="s4-list" style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {recent.map((entry) => (
              <li className="s4-list__item" key={`${entry.chain}-${entry.value}`}>
                <div className="s4-list__main">
                  <div className="mono" style={{ overflowWrap: "anywhere" }} title={entry.value}>
                    {shortValue(entry.value, 18, 12)}
                  </div>
                  <div className="s4-list__sub">
                    <StatusBadge tone={entry.kind === "transaction" ? "neutral" : "info"}>
                      {entry.kind === "transaction" ? "交易" : "地址"}
                    </StatusBadge>{" "}
                    <span className="mono">{entry.chain}</span> · {formatTime(entry.at)}
                  </div>
                </div>
                <div className="s4-list__actions">
                  <button type="button" className="btn btn--sm" onClick={() => open(entry.value, entry.chain)}>
                    打开
                  </button>
                  <button
                    type="button"
                    className="btn btn--sm btn--ghost"
                    aria-label="删除该条最近查询"
                    onClick={() => removeOne(entry)}
                  >
                    删除
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </>
  );
}
