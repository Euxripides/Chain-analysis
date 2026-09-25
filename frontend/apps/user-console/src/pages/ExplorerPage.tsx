import {
  useEffect,
  useMemo,
  useRef,
  useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowUpRight, History, Info, Search, Trash2 } from "lucide-react";
import { Button as AntButton, Input } from "antd";
import { Card,
  EmptyState,
  PageSection,
  StatusBadge,
  useDialog,
} from "@chain/ui";
import { CsPageHeader } from "@chain/user-ui";
import { formatTime, useApi } from "../sprint5/common";
import { addressChainFamily, chainFamilyOf, chainForFamily } from "../app/chain-address";
import { ChainSelect } from "./downloads/components/ChainSelect";
import { ChainIcon } from "./downloads/components/ChainIcon";
import "./explorer-polish.css";

/**
 * U-EXP-01 地址与交易查询（蓝图 §12.14）。
 *
 * 数据层与跳转语义完全不变：
 * - 链列表来自 /api/v2/meta/chains，只展示 enabled。
 * - 输入框接受地址或交易哈希；交易哈希（0x+64hex / 64hex）跳交易详情，其余跳地址详情。
 * - 最近查询保存在 localStorage（最多 8 条）；清空走 ConfirmDialog。
 * - 所有说明/警告/确认只通过 Dialog 呈现。
 *
 * 本次只改表现层：查询区成为主工作区入口（.cs-search-hero），最近查询改成紧凑可删除列表
 * （.cs-recentlist），并补上面包屑与元信息条。
 */

interface ChainItem {
  slug: string;
  name: string;
  type: string;
  enabled: boolean;
  supports_trace?: boolean;
  /** 只有下载取数、没有 Explorer 读路径的链（Solana）：必须排除，避免点进去后报错。 */
  download_only?: boolean;
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
  const chains = useMemo(
    () => (chainsData?.items ?? []).filter((item) => item.enabled && !item.download_only),
    [chainsData],
  );
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

  /*
   * 地址形状决定链：链选择的默认值是注册表第一条（arbitrum），而输入框接受
   * EVM / TRON / Solana 三种形状。形状与当前链不是同一族时**在输入时就切换**，
   * 否则提交后请求按错误的链发出（例如 arbitrum + T… → 400「EVM 地址不合法」），
   * 地址详情页只会显示"概览数据不可用"。多族混合串不猜，交由服务端报错。
   */
  useEffect(() => {
    const family = addressChainFamily(input);
    if (family === "" || chainFamilyOf(chain) === family) return;
    const matched = chainForFamily(chains, family);
    if (matched && matched !== chain) setChain(matched);
  }, [input, chain, chains]);

  useEffect(() => {
    if (!chainsError) {
      lastErrorRef.current = "";
      return;
    }
    if (lastErrorRef.current === chainsError) return;
    lastErrorRef.current = chainsError;
    void dialog.open({
      kind: "error",
      title: "链列表加载失败",
      body: chainsError,
      actions: [
        { label: "关闭", result: "", variant: "default" },
        { label: "重新加载", result: "reload", variant: "primary" },
      ],
    }).then((answer) => { if (answer === "reload") void reloadChains(); });
  }, [chainsError, dialog, reloadChains]);

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
    let slug = (chainSlug ?? chain).trim();
    /*
     * 兜底（与上面的 effect 同一规则）：粘贴后立刻回车时 effect 可能还没提交，
     * 这里再判一次族，避免"形状与链不匹配"的请求被送出去。
     */
    const family = addressChainFamily(value);
    if (family !== "" && chainFamilyOf(slug) !== family) {
      const matched = chainForFamily(chains, family);
      if (matched) slug = matched;
    }
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
        { label: "形态识别", value: "0x+40 位地址 / TRON 地址 / 64 位交易哈希；最终判定在服务端" },
      ],
    );
  };

  const trimmed = input.trim();
  const shapeInvalid = trimmed.length > 0 && !looksLikeAddress(trimmed) && !TX_HASH_RE.test(trimmed);

  return (
    <div className="cs-pro-page cs-pro-page--explorer">
      <CsPageHeader
        className="cs-explorer-header"
        title="地址与交易查询"
        breadcrumb={[{ label: "工作台", to: "/home" }, { label: "地址与交易查询" }]}
        meta={
          <div className="cs-explorer-header__meta">
            <span>已启用链 <strong>{chains.length}</strong></span>
            <span>最近查询 <strong>{recent.length} / {MAX_RECENT}</strong></span>
          </div>
        }
        secondaryAction={
          <button type="button" className="btn btn--sm cs-explorer-help" onClick={explain}>
            <Info size={15} aria-hidden="true" />
            说明
          </button>
        }
      />
      <PageSection className="cs-explorer-section cs-explorer-section--query">
        <Card title="查询" variant="flat" className="cs-explorer-query-card">
          <form className="cs-search-hero cs-explorer-query" role="search" onSubmit={submit}>
            <div className="cs-search-hero__row cs-explorer-query__row">
              <div className="cs-explorer-query__field cs-explorer-query__chain">
                <label htmlFor="explorer-chain">区块链</label>
                <ChainSelect
                  id="explorer-chain"
                  value={chain}
                  options={chains.map((item) => ({ value: item.slug, name: item.name, enabled: item.enabled }))}
                  loading={chainsLoading}
                  disabled={chains.length === 0}
                  ariaLabel="选择链"
                  popupMatchSelectWidth
                  onChange={setChain}
                />
              </div>
              <div className="cs-explorer-query__field cs-explorer-query__address">
                <label htmlFor="explorer-address">地址或交易哈希</label>
                <Input
                  id="explorer-address"
                  className="cs-explorer-query__input"
                  size="large"
                  prefix={<Search size={18} aria-hidden="true" />}
                  allowClear
                  aria-label="地址或交易哈希"
                  aria-invalid={shapeInvalid}
                  status={shapeInvalid ? "error" : undefined}
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                  placeholder="输入地址或交易哈希"
                  spellCheck={false}
                  autoComplete="off"
                />
              </div>
              <AntButton
                className="cs-explorer-query__submit"
                type="primary"
                size="large"
                htmlType="submit"
                icon={<Search size={17} aria-hidden="true" />}
                disabled={trimmed.length === 0 || chains.length === 0}
              >
                查询
              </AntButton>
            </div>
          </form>
        </Card>
      </PageSection>
      <PageSection
        className="cs-explorer-section cs-explorer-section--recent"
      >
        <Card
          flush
          variant="flat"
          className="cs-explorer-recent-card"
          title={
            <span className="cs-explorer-recent-card__title">
              最近查询
              <span>{recent.length} / {MAX_RECENT}</span>
            </span>
          }
          actions={
            <button
              type="button"
              className="btn btn--sm btn--ghost cs-explorer-clear"
              disabled={recent.length === 0}
              onClick={() => void clearAll()}
            >
              <Trash2 size={14} aria-hidden="true" />
              清空
            </button>
          }
        >
          {recent.length === 0 ? (
            <EmptyState title="暂无最近查询" icon={<History size={26} aria-hidden="true" />} />
          ) : (
            <div className="cs-recentlist cs-explorer-recent" role="list">
              {recent.map((entry) => (
                <div
                  className="cs-recentlist__item cs-explorer-recent__item"
                  role="listitem"
                  key={`${entry.chain}-${entry.value}`}
                >
                  <span className="cs-explorer-recent__icon" aria-hidden="true"><ChainIcon chain={entry.chain} size={22} /></span>
                  <span className="cs-explorer-recent__identity">
                    <span className="cs-recentlist__value" title={entry.value}>{entry.value}</span>
                    <span className="cs-recentlist__meta cs-explorer-recent__meta">
                      <StatusBadge tone={entry.kind === "transaction" ? "neutral" : "info"}>
                        {entry.kind === "transaction" ? "交易" : "地址"}
                      </StatusBadge>
                      <span className="cs-explorer-recent__chain">{entry.chain}</span>
                      <time dateTime={entry.at}>{formatTime(entry.at)}</time>
                    </span>
                  </span>
                  <span className="toolbar cs-explorer-recent__actions">
                    <button type="button" className="btn btn--sm cs-explorer-recent__open" onClick={() => open(entry.value, entry.chain)}>
                      打开
                      <ArrowUpRight size={14} aria-hidden="true" />
                    </button>
                    <button
                      type="button"
                      className="btn btn--sm btn--ghost cs-explorer-recent__remove"
                      aria-label="删除该条最近查询"
                      onClick={() => removeOne(entry)}
                    >
                      <Trash2 size={15} aria-hidden="true" />
                    </button>
                  </span>
                </div>
              ))}
            </div>
          )}
        </Card>
      </PageSection>
    </div>
  );
}
