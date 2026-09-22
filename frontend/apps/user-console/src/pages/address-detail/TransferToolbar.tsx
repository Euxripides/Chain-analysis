/**
 * 地址详情 · 转账列表工具栏（对齐参考站点的单行胶囊控件）。
 *
 * 参考站点只有一行四个圆角胶囊（代币 / 数量 / 日期区间 / 发送方·接收方）+ 刷新图标；
 * 我们比它多的能力（方向、时间窗快捷项、代币-NFT、发送方与接收方分别指定）全部**折进胶囊**，
 * 不丢任何既有筛选，也不新增接口：每个回调最终都写进 URL（刷新可恢复、可分享）。
 *
 * 纯控件：不发请求、不持业务状态，草稿值只在"回车 / 应用"时向上提交。
 */
import { useEffect, useRef, useState } from "react";
import { ArrowRight, CalendarDays, ChevronDown, RefreshCw, X } from "lucide-react";

import { Button } from "@chain/ui";

import { TokenPill, useDismissDetails } from "./TokenPicker";
import type { ActivityRow } from "./address-detail.types";

export interface TransferToolbarProps {
  /** 当前页已取到的行：只用于"热门代币"候选与选中代币的小图标。 */
  rows: ActivityRow[];
  /** 代币胶囊只在代币转账 Tab 有意义（普通交易 / 内部交易没有代币维度）。 */
  showToken: boolean;
  /** 数量区间只对代币转账生效（后端同一个 Filter，但 NFT 与内部交易不按数量筛）。 */
  showAmount: boolean;
  assetType: "token" | "nft";
  onAssetType: (next: "token" | "nft") => void;
  token: string;
  onToken: (next: string) => void;
  windowDays: number;
  onWindowDays: (next: number) => void;
  fromDate: string;
  toDate: string;
  /** 未显式选日期时，日期胶囊里展示"当前时间窗"解析出来的起止日（YYYY-MM-DD）。 */
  windowFrom: string;
  windowTo: string;
  onDates: (next: { from: string | null; to: string | null }) => void;
  direction: string;
  onDirection: (next: string) => void;
  fromAddress: string;
  toAddress: string;
  onParty: (next: { from: string | null; to: string | null }) => void;
  minAmount: string;
  maxAmount: string;
  onAmount: (next: { min: string | null; max: string | null }) => void;
  onRefresh: () => void;
  onResetAll: () => void;
}

/** 时间窗快捷项：日期区间留空时按"最近 N 天"取数（与页面取数口径一致）。 */
const WINDOWS: Array<{ value: number; label: string }> = [
  { value: 7, label: "最近 7 天" },
  { value: 30, label: "最近 30 天" },
  { value: 90, label: "最近 90 天" },
  { value: 0, label: "全部时间" },
];

const DIRECTIONS: Array<{ value: string; label: string }> = [
  { value: "", label: "全部方向" },
  { value: "in", label: "流入 in" },
  { value: "out", label: "流出 out" },
  { value: "self", label: "自转 self" },
];

/** 地址缩略：0x + 前 6 位 + … + 后 4 位（胶囊里只放得下这么多）。 */
function shortAddress(value: string): string {
  return value.length > 14 ? value.slice(0, 8) + "…" + value.slice(-4) : value;
}

export function TransferToolbar(props: TransferToolbarProps) {
  const [partySide, setPartySide] = useState<"from" | "to">(props.fromAddress ? "from" : props.toAddress ? "to" : "from");
  const [partyDraft, setPartyDraft] = useState(props.fromAddress || props.toAddress || "");
  const [amountDraft, setAmountDraft] = useState({ min: props.minAmount, max: props.maxAmount });
  const amountRef = useRef<HTMLDetailsElement>(null);
  const partyRef = useRef<HTMLDetailsElement>(null);

  useEffect(() => {
    setPartyDraft(props.fromAddress || props.toAddress || "");
    setPartySide(props.fromAddress ? "from" : props.toAddress ? "to" : "from");
  }, [props.fromAddress, props.toAddress]);

  useEffect(() => {
    setAmountDraft({ min: props.minAmount, max: props.maxAmount });
  }, [props.minAmount, props.maxAmount]);

  useDismissDetails(amountRef);
  useDismissDetails(partyRef);

  const closeAmount = () => {
    if (amountRef.current) amountRef.current.open = false;
  };
  const closeParty = () => {
    if (partyRef.current) partyRef.current.open = false;
  };

  /** 提交发送方/接收方筛选：写选中那一侧，另一侧保留原值（两侧可同时生效）。 */
  const applyParty = () => {
    const value = partyDraft.trim();
    props.onParty(
      partySide === "from"
        ? { from: value || null, to: props.toAddress || null }
        : { from: props.fromAddress || null, to: value || null },
    );
    closeParty();
  };

  const partyParts: string[] = [];
  if (props.fromAddress) partyParts.push("发送方 " + shortAddress(props.fromAddress));
  if (props.toAddress) partyParts.push("接收方 " + shortAddress(props.toAddress));
  const partyLabel = partyParts.length > 0 ? partyParts.join(" · ") : "全部";
  const amountLabel = props.minAmount || props.maxAmount ? (props.minAmount || "0") + " ~ " + (props.maxAmount || "∞") : "全部";

  return (
    <div className="cs-okbar" role="group" aria-label="转账筛选">
      {props.showToken ? (
      <TokenPill
        rows={props.rows}
        value={props.token}
        assetType={props.assetType}
        onAssetType={props.onAssetType}
        onConfirm={(next) => props.onToken(next)}
        onReset={() => props.onToken("")}
      />
      ) : null}

      {props.showAmount ? (
      <details ref={amountRef} className="cs-okpillwrap">
        <summary className="cs-okpill">
          <span className="cs-okpill__label">数量:</span>
          <span className="cs-okpill__value">{amountLabel}</span>
          <ChevronDown size={14} className="cs-okpill__chevron" aria-hidden="true" />
        </summary>
        <div className="cs-okpill__panel" style={{ width: 280 }}>
          <div style={{ display: "flex", gap: 8 }}>
            <input
              className="input"
              aria-label="最小数量"
              placeholder="最小数量"
              value={amountDraft.min}
              onChange={(event) => setAmountDraft((prev) => ({ ...prev, min: event.target.value }))}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  props.onAmount({ min: amountDraft.min.trim() || null, max: amountDraft.max.trim() || null });
                  closeAmount();
                }
              }}
            />
            <input
              className="input"
              aria-label="最大数量"
              placeholder="最大数量"
              value={amountDraft.max}
              onChange={(event) => setAmountDraft((prev) => ({ ...prev, max: event.target.value }))}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  props.onAmount({ min: amountDraft.min.trim() || null, max: amountDraft.max.trim() || null });
                  closeAmount();
                }
              }}
            />
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setAmountDraft({ min: "", max: "" });
                props.onAmount({ min: null, max: null });
                closeAmount();
              }}
            >
              清除
            </Button>
            <Button
              size="sm"
              variant="primary"
              onClick={() => {
                props.onAmount({ min: amountDraft.min.trim() || null, max: amountDraft.max.trim() || null });
                closeAmount();
              }}
            >
              应用
            </Button>
          </div>
        </div>
      </details>
      ) : null}

      {/* 日期区间：左侧是"最近 N 天"快捷项，右侧是显式起止日期（显式日期优先）。 */}
      <div className="cs-okpill cs-okpill--date">
        <select
          aria-label="时间窗筛选"
          className="cs-okpill__select"
          value={String(props.windowDays)}
          onChange={(event) => props.onWindowDays(Number(event.target.value))}
        >
          {WINDOWS.map((item) => (
            <option key={item.value} value={String(item.value)}>
              {item.label}
            </option>
          ))}
        </select>
        <input
          type="date"
          aria-label="开始日期"
          className="cs-okpill__date"
          value={props.fromDate || props.windowFrom}
          onChange={(event) => props.onDates({ from: event.target.value || null, to: props.toDate || null })}
        />
        <ArrowRight size={14} className="cs-okpill__chevron" aria-hidden="true" />
        <input
          type="date"
          aria-label="结束日期"
          className="cs-okpill__date"
          value={props.toDate || props.windowTo}
          onChange={(event) => props.onDates({ from: props.fromDate || null, to: event.target.value || null })}
        />
        <CalendarDays size={15} className="cs-okpill__chevron" aria-hidden="true" />
        {props.fromDate || props.toDate || props.windowDays !== 30 ? (
          <button
            type="button"
            className="cs-okpill__clear"
            aria-label="清除日期筛选"
            title="清除日期筛选"
            onClick={() => {
              props.onDates({ from: null, to: null });
              props.onWindowDays(30);
            }}
          >
            <X size={13} aria-hidden="true" />
          </button>
        ) : null}
      </div>

      <div className="cs-okpill cs-okpill--select">
        <select
          aria-label="方向筛选"
          className="cs-okpill__select"
          value={props.direction}
          onChange={(event) => props.onDirection(event.target.value)}
        >
          {DIRECTIONS.map((item) => (
            <option key={item.value} value={item.value}>
              {item.label}
            </option>
          ))}
        </select>
      </div>

      {/* 发送方/接收方：一个胶囊里"选边 + 输地址"，回车即查（参考站点的同形态控件）。 */}
      <details ref={partyRef} className="cs-okpillwrap cs-okpillwrap--party">
        <summary className="cs-okpill">
          <span className="cs-okpill__label">发送方/接收方:</span>
          <span className="cs-okpill__value">{partyLabel}</span>
          <ChevronDown size={14} className="cs-okpill__chevron" aria-hidden="true" />
        </summary>
        <div className="cs-okpill__panel" style={{ width: 360 }}>
          <div style={{ display: "flex", gap: 8 }}>
            <select
              className="select"
              aria-label="对手方侧"
              style={{ flex: "0 0 120px" }}
              value={partySide}
              onChange={(event) => setPartySide(event.target.value === "to" ? "to" : "from")}
            >
              <option value="from">发送方</option>
              <option value="to">接收方</option>
            </select>
            <input
              className="input"
              aria-label="输入地址搜索"
              placeholder="输入地址搜索"
              value={partyDraft}
              onChange={(event) => setPartyDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault();
                  applyParty();
                }
              }}
            />
          </div>
          <div style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
            <Button
              size="sm"
              variant="ghost"
              onClick={() => {
                setPartyDraft("");
                props.onParty({ from: null, to: null });
                closeParty();
              }}
            >
              清除
            </Button>
            <Button size="sm" variant="primary" onClick={applyParty}>
              查询
            </Button>
          </div>
        </div>
      </details>

      <button type="button" className="cs-okpill cs-okpill--icon" aria-label="刷新" title="刷新" onClick={props.onRefresh}>
        <RefreshCw size={15} aria-hidden="true" />
      </button>

      <button type="button" className="cs-okreset" onClick={props.onResetAll}>
        重置
      </button>
    </div>
  );
}
