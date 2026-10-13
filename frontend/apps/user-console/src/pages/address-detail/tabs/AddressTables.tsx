/**
 * 地址详情的展示表格（用户端重构规范 V4 §43）
 *
 * 从 1434 行的 AddressDetailPage.tsx **原样抽出**，每个组件都是纯展示：
 * 只接收已经取好的行数据，不发请求、不改状态、不做业务判断。
 * 这样主页面只剩编排（取数 / URL 状态 / 检查器联动），表格可以独立评审与复用。
 *
 * 注意：这些组件是 9 个业务 Tab 的公共底座（概览与资产共用余额/净流量表，
 * 普通交易 / Token Transfer / Internal 共用 ActivityTable），因此放在共享模块里，
 * 而不是复制三份——Tab 的 key 与顺序仍由 address-detail.types.ts 的 TABS 单一来源决定。
 */
import { useState } from "react";
import { TokenIcon } from "../../explorer/AssetIcon";
import { Link } from "react-router-dom";
import { CopyButton, DataTable, EmptyState, HashValue, StatusBadge, statusToneFor, Card, PageSection, KeyValueList, MetricCard } from "@chain/ui";

import { formatRawAmount, formatTime, shortValue } from "../../../sprint5/common";
import { addressPath, blockText, decimalsOf, directionTone, jsonText, transactionPath } from "../address-detail.types";
import type { ActivityRow, AnalysisData, AnalysisEdge, Asset, CounterpartyRow, CoverageItem, NetFlowAsset, SemanticEvent } from "../address-detail.types";

export function EntityHash({
  value,
  to,
  label,
  head = 10,
  tail = 8,
}: {
  value?: string;
  to: string;
  label: string;
  head?: number;
  tail?: number;
}) {
  if (!value) return <span>—</span>;
  return (
    <span className="cs-hash">
      <Link className="cs-hash__text" to={to} title={value} onClick={(event) => event.stopPropagation()}>
        {shortValue(value, head, tail)}
      </Link>
      {/* 复制按钮不能顺带触发整行的"选中到检查器"。 */}
      <span onClick={(event) => event.stopPropagation()}>
        <CopyButton value={value} label={label} />
      </span>
    </span>
  );
}

/**
 * OKLink 风格排版助手（列表对齐参考站点：交易哈希 / 方法 / 时间 / 发送方 / 接收方 / 数量 / 代币）。
 * 纯展示函数，不含业务判断。
 */

/** 时间统一为 YYYY/MM/DD HH:mm:ss。 */
function okTime(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const part = (input: number) => String(input).padStart(2, "0");
  return (
    date.getFullYear() +
    "/" +
    part(date.getMonth() + 1) +
    "/" +
    part(date.getDate()) +
    " " +
    part(date.getHours()) +
    ":" +
    part(date.getMinutes()) +
    ":" +
    part(date.getSeconds())
  );
}

/** 整数千分位。 */
function groupDigits(value: string): string {
  return value.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

/**
 * 原始整数 + 精度 → 千分位文本。
 *
 * 小数最多展示 8 位：Solana 的 38 位精度尾巴只保留有效头部，避免撑破单元格。
 * 非纯数字（例如已经格式化好的 amount）原样返回。
 */
function amountText(raw?: string, decimals?: number): string {
  const digits = (raw ?? "").trim();
  if (!digits) return "";
  if (!/^\d+$/.test(digits)) return digits;
  const scale = Math.max(0, Math.min(decimals ?? 0, 36));
  const padded = digits.padStart(scale + 1, "0");
  const integer = padded.slice(0, padded.length - scale);
  const fraction = scale > 0 ? padded.slice(padded.length - scale).replace(/0+$/, "").slice(0, 8) : "";
  return groupDigits(integer) + (fraction ? "." + fraction : "");
}

/** 金额前缀：流入 +、流出 -（与参考站点一致，用 ASCII 连字符），自转不带符号。 */
function amountSign(direction?: string): string {
  if (direction === "in") return "+";
  if (direction === "out") return "-";
  return "";
}

/**
 * 参考站点的哈希缩略：0x + 前 14 位 + "..."（完整值放 title）。
 * 非 0x 开头的链（Solana 签名 / base58）按"前 16 字符 + ..."处理。
 */
function okHashText(value: string): string {
  return value.length <= 20 ? value : value.slice(0, 16) + "...";
}

/** 地址缩略：0x + 前 4 位 + "..." + 后 10 位（参考站点的交易对方列）。 */
function okAddressText(value: string): string {
  return value.length <= 20 ? value : value.slice(0, 6) + "..." + value.slice(-10);
}

/** 法币小字：参考站点的 "< $0.01" / "$1,234.56" 两种形态；没有估值就不显示。 */
function fiatLabel(amountUsd?: string): string {
  const text = (amountUsd ?? "").trim();
  if (!text) return "";
  const value = Number(text);
  if (!Number.isFinite(value)) return "$" + text;
  if (value === 0) return "$0";
  if (Math.abs(value) < 0.01) return "< $0.01";
  return "$" + value.toLocaleString("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
}

/** 转账双方：NFT 行直接带 from/to，活动行需要按方向推导。 */
function transferParties(row: ActivityRow): { from?: string; to?: string } {
  if (row.from_address || row.to_address) {
    return { from: row.from_address, to: row.to_address };
  }
  const own = row.address;
  const other = row.counterparty;
  if (row.direction === "out") return { from: own, to: other };
  if (row.direction === "in") return { from: other, to: own };
  return { from: other ?? own, to: own };
}

/** 代币图标：只认同源自托管路径（CSP img-src 不允许外链图），否则退回首字母。 */
function TokenLogo({ logo, symbol, size = 32 }: { logo?: string; symbol?: string; size?: number }) {
  const [broken, setBroken] = useState(false);
  const src = logo && logo.startsWith("/") ? logo : "";
  if (src && !broken) {
    return (
      <img
        src={src}
        alt=""
        width={size}
        height={size}
        loading="lazy"
        style={{ width: size, height: size, borderRadius: "50%", objectFit: "cover", flex: "0 0 auto" }}
        onError={() => setBroken(true)}
      />
    );
  }
  const letter = (symbol ?? "").trim().slice(0, 1).toUpperCase() || "?";
  return (
    <span
      aria-hidden="true"
      style={{
        width: size,
        height: size,
        borderRadius: "50%",
        flex: "0 0 auto",
        display: "inline-flex",
        alignItems: "center",
        justifyContent: "center",
        fontSize: Math.round(size * 0.55),
        fontWeight: 600,
        color: "var(--text-secondary, #64748b)",
        background: "var(--bg-subtle, #f1f5f9)",
      }}
    >
      {letter}
    </span>
  );
}

/** 主体地址所在侧的方向标签（Out / In），非主体侧不显示。 */
function SideTag({ direction, side }: { direction?: string; side: "from" | "to" }) {
  if (direction !== "in" && direction !== "out" && direction !== "self") return null;
  const subjectSide = direction === "self" || (direction === "out" && side === "from") || (direction === "in" && side === "to");
  if (!subjectSide) return null;
  const tone = direction === "in" ? "in" : direction === "out" ? "out" : "self";
  const label = direction === "in" ? "In" : direction === "out" ? "Out" : "Self";
  return (
    <span className="cs-okside" data-tone={tone}>
      {label}
    </span>
  );
}

/**
 * 发送方 / 接收方单元格：方向标签 + 地址（+ 复制）。
 *
 * 参考站点的画法：**主体侧地址不是链接**（当前就在这个地址页上），用正文色；
 * 对手方地址是链接色，可点进对方地址页。两侧都保留复制。
 */
function PartyCell({
  value,
  chain,
  side,
  direction,
  subject,
}: {
  value?: string;
  chain: string;
  side: "from" | "to";
  direction?: string;
  subject?: boolean;
}) {
  if (!value) return <span>—</span>;
  const label = side === "from" ? "发送方地址" : "接收方地址";
  return (
    <span className="cs-okparty">
      <SideTag direction={direction} side={side} />
      {subject ? (
        <span className="cs-okparty__self" title={value}>
          {okAddressText(value)}
        </span>
      ) : (
        <Link className="cs-okparty__link" to={addressPath(chain, value)} title={value} onClick={(event) => event.stopPropagation()}>
          {okAddressText(value)}
        </Link>
      )}
      <span onClick={(event) => event.stopPropagation()}>
        <CopyButton value={value} label={label} />
      </span>
    </span>
  );
}

/** 方法名：参考站点是灰底圆角标签，展示交易方法原文（不首字母大写）。 */
function MethodChip({ row, nft }: { row: ActivityRow; nft: boolean }) {
  const raw = (row.method_name ?? "").trim();
  const fallback = nft || row.activity_type !== "internal_transfer" ? "transfer" : "internal";
  const method = raw || fallback;
  return (
    <span className="cs-okmethod" title={raw ? method : "本地索引没有这笔交易的方法名，按活动类型显示"}>
      {method}
    </span>
  );
}

/**
 * 代币"可疑"判定 —— 列表警示框与警示三角的**唯一样子**。
 *
 * 口径只看**解析质量**，不看"是否人工审核过"：
 *   - conflict / partial / unknown（含压根没有元数据）→ 可疑，挂框；
 *   - complete → 正常，不挂框。未人工审核 ≠ 可疑：库里 36k 行里只有 1 行被人工标为
 *     verified，若按"未审核即警示"，整页都是虚线框，警示本身就没有信息量了；
 *   - token_verified=true（人工白名单）永远不挂；
 *   - 原生币（无合约地址）与 NFT 合集不参与。
 */
const SUSPICIOUS_TOKEN_STATUS = new Set(["conflict", "partial", "unknown"]);

/** 可疑原因 → 悬停说明（写清"为什么可疑"，而不是只给一个感叹号）。 */
const SUSPICIOUS_REASON: Record<string, string> = {
  conflict: "多个来源的元数据互相矛盾，符号/名称可能被篡改或重名",
  partial: "元数据只解析到一部分，名称或精度可能缺失",
  unknown: "尚未解析到元数据，符号与名称来自合约自报",
};

function tokenSuspicion(row: ActivityRow): string {
  if (!row.token_address || row.token_verified === true) return "";
  const status = (row.token_metadata_status ?? "").trim().toLowerCase();
  if (!status) return "unknown";
  return SUSPICIOUS_TOKEN_STATUS.has(status) ? status : "";
}

/**
 * 可疑代币警示标（列表里符号前的小三角）。
 *
 * 为什么需要：代币符号是**合约自报**的，任何人都能发一个叫 USDT 的合约；
 * 参考站点在可疑代币旁同时给"虚线警示框 + 三角"，我们也照做。
 */
function UnverifiedMark({ reason, symbol }: { reason?: string; symbol?: string }) {
  if (!reason) return null;
  const title =
    (symbol ? symbol + " " : "") +
    "代币元数据可疑：" +
    (SUSPICIOUS_REASON[reason] ?? "来源不明") +
    "。请核对合约地址。";
  return (
    <span title={title} aria-label={title} style={{ display: "inline-flex", flex: "0 0 auto", color: "#f59e0b" }}>
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" aria-hidden="true">
        <path d="M12 3.5 22 20.5H2L12 3.5Z" fill="currentColor" />
        <path d="M12 9.5v5" stroke="#fff" strokeWidth="2" strokeLinecap="round" />
        <circle cx="12" cy="17.4" r="1.2" fill="#fff" />
      </svg>
    </span>
  );
}

/** 代币单元格：图标 + 符号 +（代币全称 | NFT 的 #编号 · 标准）。 */
function TokenCell({ row, nft, chain }: { row: ActivityRow; nft: boolean; chain: string }) {
  const fallback = row.token_address ? shortValue(row.token_address, 6, 4) : "原生代币";
  const primary = nft
    ? row.token_name || row.token_symbol || shortValue(row.contract_address ?? row.token_address ?? "", 6, 4) || "NFT"
    : row.token_symbol || fallback;
  const secondary = nft
    ? "#" + (row.token_id || "—") + (row.token_standard ? " · " + row.token_standard : "")
    : row.token_name || "";
  // 可疑代币（解析冲突/不完整/未知）整格加虚线警示框（参考站点样式）。
  const suspicion = tokenSuspicion(row);
  const unverified = Boolean(suspicion);
  return (
    <span className="cs-oktoken" data-suspicious={unverified ? "true" : undefined}>
      <Link to={`/tokens/${encodeURIComponent(chain)}/${encodeURIComponent(row.contract_address || row.token_address || "")}`} onClick={event => event.stopPropagation()}><TokenIcon chain={chain} contract={row.contract_address || row.token_address} symbol={row.token_symbol || primary} size={28} /></Link>
      <span className="cs-oktoken__text">
        <span className="cs-oktoken__symbol">
          <UnverifiedMark reason={suspicion} symbol={row.token_symbol || primary} />
          <span title={primary}>{primary}</span>
        </span>
        {secondary ? (
          <span className="cs-oktoken__name" title={secondary}>
            {secondary}
          </span>
        ) : null}
      </span>
    </span>
  );
}

/**
 * 数量单元格：符号 + 千分位 + 法币小字。
 *
 * 与参考站点一致：数值本身用正文色（进出方向由 In/Out 标签表达），法币估值放在下一行小字，
 * 金额不足一分钱时显示 "< $0.01"。
 */
function AmountCell({ row, nft }: { row: ActivityRow; nft: boolean }) {
  const raw = nft ? row.amount_raw || "1" : row.amount_raw ?? row.amount;
  const text = amountText(raw, nft ? 0 : row.token_decimals);
  const sign = amountSign(row.direction);
  const fiat = fiatLabel(row.amount_usd);
  return (
    <span className="cs-okamount">
      <span className="cs-okamount__value" title={raw ?? ""}>
        {text ? sign + text : "—"}
      </span>
      {fiat ? <span className="cs-okamount__fiat">{fiat}</span> : null}
    </span>
  );
}


export function CurrentBalanceTable({ items, chain }: { items: Asset[]; chain: string }) {
  return (
    <DataTable<Asset>
      rows={items}
      rowKey={(row, index) => (row.token_address ?? "native") + "-" + (row.snapshot_block ?? "na") + "-" + index}
      empty={<EmptyState title="没有当前余额快照" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          width: "180px",
          render: (row) => (
            <div className="cs-balance-asset">
              <TokenIcon chain={chain} contract={row.token_address} symbol={row.token_symbol || row.token_name} size={28} />
              <div style={{ minWidth: 0 }}>
                <div className="cs-balance-asset__symbol">{row.token_symbol || row.token_name || "未知资产"}</div>
                <div className="s4-list__sub cs-balance-asset__name">
                  {row.token_name || (row.token_address ? "Token" : "原生资产")}
                </div>
              </div>
            </div>
          ),
        },
        {
          key: "balance",
          header: "余额",
          width: "170px",
          align: "right",
          render: (row) => {
            const value = row.display_amount || formatRawAmount(row.raw_amount, decimalsOf(row));
            return <span className="mono cs-balance-number" title={row.raw_amount}>{value || "—"}</span>;
          },
        },
        {
          key: "price",
          header: "价格 USD",
          width: "130px",
          align: "right",
          render: (row) => <span className="mono cs-balance-number">{row.token_price || "—"}</span>,
        },
        {
          key: "value",
          header: "估值 USD",
          width: "130px",
          align: "right",
          render: (row) => <span className="mono cs-balance-number">{row.amount_usd || "—"}</span>,
        },
        {
          key: "observed",
          header: "更新时间",
          width: "165px",
          render: (row) => <span>{formatTime(row.observed_at)}</span>,
        },
      ]}
    />
  );
}

export function NetFlowTable({ items, chain }: { items: NetFlowAsset[]; chain: string }) {
  const rawCell = (raw?: string, decimals?: number, color?: string) => (
    <div style={{ minWidth: 0 }}>
      <div className="mono" style={color ? { color } : undefined}>
        {formatRawAmount(raw, decimals)}
      </div>
      <div className="s4-list__sub mono" title={raw}>
        raw {raw ?? "—"}
      </div>
    </div>
  );
  return (
    <DataTable<NetFlowAsset>
      rows={items}
      rowKey={(row, index) => (row.token_address ?? row.asset ?? "native") + "-" + index}
      empty={<EmptyState title="没有历史净流量记录" />}
      columns={[
        {
          key: "asset",
          header: "资产",
          render: (row) => (
            <div style={{ minWidth: 0 }}>
              <div style={{ fontWeight: 600 }}>{row.token_symbol || row.asset || "未知资产"}</div>
              <div className="s4-list__sub">
                {row.token_address ? (
                  <HashValue value={row.token_address} label="Token 合约地址" head={10} tail={8} />
                ) : (
                  <span className="mono">{row.asset || "native"}</span>
                )}
                {" · decimals "}
                {row.decimals ?? "—"}
              </div>
            </div>
          ),
        },
        { key: "in", header: "入账", align: "right", render: (row) => rawCell(row.in_raw, decimalsOf(row)) },
        { key: "out", header: "出账", align: "right", render: (row) => rawCell(row.out_raw, decimalsOf(row)) },
        {
          key: "net",
          header: "净额",
          align: "right",
          render: (row) =>
            rawCell(
              row.net_raw,
              decimalsOf(row),
              (row.net_raw ?? "").startsWith("-") ? "var(--c-danger)" : "var(--c-green)",
            ),
        },
        {
          key: "count",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transaction_count ?? "—"}</span>,
        },
        {
          key: "source",
          header: "来源",
          width: "120px",
          render: (row) => <StatusBadge tone="neutral">{row.source || "—"}</StatusBadge>,
        },
      ]}
    />
  );
}

interface TransferTableProps {
  rows: ActivityRow[];
  chain: string;
  loading?: boolean;
  emptyTitle?: string;
  /** 行激活只负责"选中到检查器"；跳转仍由行内明确链接承担（蓝图 §8.4）。 */
  onSelectActivity?: (row: ActivityRow) => void;
  /**
   * 隐藏共享 DataTable 的"共 N 行 / 第 x/y 页 / DOM n 行（虚拟滚动）"页脚。
   *
   * 转账列表的分页由服务端 cursor 决定（页面右下角是"加载更多 / 加载全部"），
   * 窗口分页的摘要对用户没有意义——参考站点也没有这一行。
   */
  hidePager?: boolean;
}

/**
 * 转账/活动表主体（代币与 NFT 共用列形状，仅"代币"列语义不同）。
 *
 * 列顺序对齐参考站点：交易哈希 / 方法 / 时间 / 发送方 / 接收方 / 数量 / 代币。
 * 纯展示：不发请求、不改状态；来源与原始量在行检查器里查看。
 */
function TransferTable({
  rows,
  chain,
  loading,
  emptyTitle = "没有活动记录",
  onSelectActivity,
  hidePager,
  nft,
}: TransferTableProps & { nft: boolean }) {
  return (
    <DataTable<ActivityRow>
      rows={rows}
      loading={loading}
      keepPreviousData
      rowKey={(row, index) =>
        (row.tx_hash ?? "tx") +
        "-" +
        (row.log_index ?? index) +
        "-" +
        (row.activity_type ?? "") +
        "-" +
        (row.token_id ?? "") +
        "-" +
        (row.counterparty ?? "") +
        "-" +
        (row.token_address ?? "")
      }
      empty={<EmptyState title={emptyTitle} />}
      onRowActivate={onSelectActivity}
      rowActivateLabel={(row) =>
        "在检查器中查看 " + okTime(row.timestamp) + " 的 " + (row.activity_type || "活动") + " 详情"
      }
      columns={[
        {
          key: "tx",
          header: "交易哈希",
          width: "13.5%",
          render: (row) =>
            row.tx_hash ? (
              /* 参考站点这一列只有链接（完整值在 title 里，复制走行检查器），缩略 0x + 14 位 + "..."。 */
              <Link
                className="cs-okhash"
                to={transactionPath(chain, row.tx_hash)}
                title={row.tx_hash}
                onClick={(event) => event.stopPropagation()}
              >
                {okHashText(row.tx_hash)}
              </Link>
            ) : (
              <span>—</span>
            ),
        },
        { key: "method", header: "方法", width: "9%", render: (row) => <MethodChip row={row} nft={nft} /> },
        {
          key: "time",
          header: <span className="cs-okhead--sorted" title="默认按时间倒序（本地索引 cursor 顺序）">时间</span>,
          width: "15.5%",
          render: (row) => <span className="cs-oktime">{okTime(row.timestamp)}</span>,
        },
        {
          key: "from",
          header: "发送方",
          width: "16%",
          render: (row) => {
            const parties = transferParties(row);
            return (
              <PartyCell
                value={parties.from}
                chain={chain}
                side="from"
                direction={row.direction}
                subject={row.direction === "out" || row.direction === "self"}
              />
            );
          },
        },
        {
          key: "to",
          header: "接收方",
          width: "16%",
          render: (row) => {
            const parties = transferParties(row);
            return (
              <PartyCell
                value={parties.to}
                chain={chain}
                side="to"
                direction={row.direction}
                subject={row.direction === "in" || row.direction === "self"}
              />
            );
          },
        },
        { key: "amount", header: "数量", width: "15%", render: (row) => <AmountCell row={row} nft={nft} /> },
        { key: "token", header: "代币", width: "15%", render: (row) => <TokenCell row={row} nft={nft} chain={chain} /> },
      ]}
    />
  );
}

/** 代币转账表（读取面 /activity，asset_type=token）。 */
export function ActivityTable(props: TransferTableProps) {
  return <TransferTable {...props} nft={false} />;
}

/**
 * NFT 转账表（读取面 /activity?asset_type=nft）。
 *
 * 与代币表同列形状，差别只在"代币"列展示合集名 + #token_id + 标准。
 */
export function NftTransferTable(props: TransferTableProps) {
  return <TransferTable {...props} nft={true} />;
}

export function CounterpartyTable({
  rows,
  chain,
  onSelectCounterparty,
}: {
  rows: CounterpartyRow[];
  chain: string;
  onSelectCounterparty?: (row: CounterpartyRow) => void;
}) {
  return (
    <DataTable<CounterpartyRow>
      rows={rows}
      rowKey={(row, index) => (row.counterparty ?? "cp") + "-" + index}
      empty={<EmptyState title="没有对手方记录" />}
      onRowActivate={onSelectCounterparty}
      rowActivateLabel={(row) => "在检查器中查看对手方 " + (row.counterparty ?? "") + " 详情"}
      columns={[
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <EntityHash value={row.counterparty} to={addressPath(chain, row.counterparty)} label="对手方地址" />
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "count",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transaction_count ?? "—"}</span>,
        },
        { key: "in", header: "入账 raw", align: "right", render: (row) => <span className="mono">{row.in_raw ?? "—"}</span> },
        { key: "out", header: "出账 raw", align: "right", render: (row) => <span className="mono">{row.out_raw ?? "—"}</span> },
        {
          key: "net",
          header: "净额 raw",
          align: "right",
          render: (row) => (
            <span className="mono" style={{ color: (row.net_raw ?? "").startsWith("-") ? "var(--c-danger)" : undefined }}>
              {row.net_raw ?? "—"}
            </span>
          ),
        },
        { key: "first", header: "首次", width: "160px", render: (row) => <span>{formatTime(row.first_seen)}</span> },
        { key: "last", header: "最近", width: "160px", render: (row) => <span>{formatTime(row.last_seen)}</span> },
      ]}
    />
  );
}

export function SemanticTable({ events, chain }: { events: SemanticEvent[]; chain: string }) {
  return (
    <DataTable<SemanticEvent>
      rows={events}
      rowKey={(row, index) => (row.id ?? row.tx_hash ?? "event") + "-" + index}
      empty={<EmptyState title="没有语义事件" />}
      columns={[
        {
          key: "type",
          header: "类型",
          width: "170px",
          render: (row) => (
            <div>
              <StatusBadge tone="info">{row.type || "—"}</StatusBadge>
              <div className="s4-list__sub">confidence {row.confidence ?? "—"}</div>
            </div>
          ),
        },
        {
          key: "tx",
          header: "tx_hash",
          width: "170px",
          render: (row) =>
            row.tx_hash ? (
              <EntityHash value={row.tx_hash} to={transactionPath(chain, row.tx_hash)} label="交易哈希" />
            ) : (
              <span>—</span>
            ),
        },
        {
          key: "algorithm",
          header: "algorithm_version",
          width: "180px",
          render: (row) => <span className="mono">{row.algorithm_version || "—"}</span>,
        },
        {
          key: "payload",
          header: "payload",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.payload, 2000)}>
              {jsonText(row.payload, 120)}
            </span>
          ),
        },
        {
          key: "provenance",
          header: "provenance",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }} title={jsonText(row.provenance, 2000)}>
              {jsonText(row.provenance, 100)}
            </span>
          ),
        },
      ]}
    />
  );
}

export function EdgeTable({ edges, chain, emptyTitle }: { edges: AnalysisEdge[]; chain: string; emptyTitle: string }) {
  return (
    <DataTable<AnalysisEdge>
      rows={edges}
      rowKey={(row, index) => (row.direction ?? "") + "-" + (row.counterparty ?? "cp") + "-" + (row.asset ?? "") + "-" + index}
      empty={<EmptyState title={emptyTitle} />}
      columns={[
        {
          key: "direction",
          header: "方向",
          width: "90px",
          render: (row) => <StatusBadge tone={directionTone(row.direction)}>{row.direction || "—"}</StatusBadge>,
        },
        {
          key: "counterparty",
          header: "对手方",
          width: "180px",
          render: (row) =>
            row.counterparty ? (
              <EntityHash value={row.counterparty} to={addressPath(chain, row.counterparty)} label="对手方地址" />
            ) : (
              <span>—</span>
            ),
        },
        { key: "asset", header: "资产", width: "120px", render: (row) => <span className="mono">{row.asset || "—"}</span> },
        { key: "amount", header: "金额 raw", align: "right", render: (row) => <span className="mono">{row.amount ?? "—"}</span> },
        {
          key: "ratio",
          header: "flow_ratio",
          align: "right",
          width: "110px",
          render: (row) => <span className="mono">{row.flow_ratio ?? "—"}</span>,
        },
        {
          key: "transactions",
          header: "交易数",
          width: "90px",
          render: (row) => <span className="mono">{row.transactions ?? "—"}</span>,
        },
        { key: "first", header: "首次", width: "160px", render: (row) => <span>{formatTime(row.first_seen)}</span> },
        { key: "last", header: "最近", width: "160px", render: (row) => <span>{formatTime(row.last_seen)}</span> },
      ]}
    />
  );
}

/** Fund Flow / Analysis：全部来自服务端 flow-profile 的真实边，不做前端推算。 */
export function AnalysisView({ data, chain }: { data: AnalysisData; chain: string }) {
  const edges = data.edges ?? [];
  const cashout = data.cashout_candidates ?? [];
  const funder = data.first_funder ?? null;
  return (
    <>
      <PageSection title="分析指标">
        <div className="metric-grid cs-stagger">
          <MetricCard
            label="交易数"
            value={data.transaction_count ?? "—"}
            source="flow-profile.transaction_count"
            sourceNote="由服务端 flow-profile 聚合返回，界面不做二次计算。"
          />
          <MetricCard
            label="唯一对手方"
            value={data.unique_counterparties ?? "—"}
            source="flow-profile.unique_counterparties"
            sourceNote="由服务端 flow-profile 聚合返回，界面不做二次计算。"
          />
          <MetricCard
            label="首次出现"
            value={formatTime(data.first_seen)}
            source="flow-profile.first_seen"
            sourceNote="来自服务端 flow-profile.first_seen：参与这些资金流关系的最早时间。"
          />
          <MetricCard
            label="最近出现"
            value={formatTime(data.last_seen)}
            source="flow-profile.last_seen"
            sourceNote="来自服务端 flow-profile.last_seen：参与这些资金流关系的最新时间。"
          />
          <MetricCard
            label="算法版本"
            value={data.algorithm_version ?? "—"}
            source="flow-profile.algorithm_version"
            sourceNote="产生这些资金流边的服务端算法版本；版本变化会改变边的口径。"
          />
        </div>
      </PageSection>
      <PageSection title="首笔资金来源">
        <Card flush>
          {funder ? (
            <div style={{ padding: "var(--sp-4)" }}>
              <KeyValueList
                items={[
                  {
                    label: "counterparty",
                    value: funder.counterparty ? (
                      <EntityHash
                        value={funder.counterparty}
                        to={addressPath(chain, funder.counterparty)}
                        label="对手方地址"
                        head={14}
                        tail={10}
                      />
                    ) : (
                      "—"
                    ),
                    wide: true,
                  },
                  { label: "asset", value: <span className="mono">{funder.asset || "—"}</span> },
                  { label: "amount raw", value: <span className="mono">{funder.amount || "—"}</span> },
                  { label: "首次", value: formatTime(funder.first_seen) },
                ]}
              />
            </div>
          ) : (
            <EmptyState title="没有识别到首笔资金来源" />
          )}
        </Card>
      </PageSection>
      <PageSection title="资金流边" count={edges.length + " 条"}>
        <Card flush>
          {/* 宽资金关系表自带滚动容器，不能把页面撑宽（蓝图 §13）。 */}
          <div className="cs-chart-frame">
            <EdgeTable edges={edges} chain={chain} emptyTitle="没有资金流边" />
          </div>
        </Card>
      </PageSection>
      <PageSection title="现金流出候选" count={cashout.length + " 条"}>
        <Card flush>
          <div className="cs-chart-frame">
            <EdgeTable edges={cashout} chain={chain} emptyTitle="没有现金流出候选" />
          </div>
        </Card>
      </PageSection>
    </>
  );
}

export function CoverageTable({ items }: { items: CoverageItem[] }) {
  return (
    <DataTable<CoverageItem>
      rows={items}
      rowKey={(row, index) => (row.dataset ?? "dataset") + "-" + (row.source ?? "source") + "-" + index}
      empty={<EmptyState title="没有 Coverage 记录" />}
      columns={[
        { key: "dataset", header: "dataset", width: "160px", render: (row) => <span className="mono">{row.dataset || "—"}</span> },
        { key: "from", header: "from_block", width: "110px", render: (row) => <span className="mono">{blockText(row.from_block)}</span> },
        { key: "to", header: "to_block", width: "110px", render: (row) => <span className="mono">{blockText(row.to_block)}</span> },
        {
          key: "through",
          header: "complete_through",
          width: "180px",
          render: (row) => <span>{formatTime(row.complete_through)}</span>,
        },
        {
          key: "status",
          header: "status",
          width: "120px",
          render: (row) => <StatusBadge tone={statusToneFor(row.status ?? "")}>{row.status || "—"}</StatusBadge>,
        },
        {
          key: "source",
          header: "source",
          width: "200px",
          render: (row) => (
            <span className="mono" style={{ overflowWrap: "anywhere" }}>
              {row.source || "—"}
            </span>
          ),
        },
        { key: "updated", header: "updated_at", width: "165px", render: (row) => <span>{formatTime(row.updated_at)}</span> },
      ]}
    />
  );
}
