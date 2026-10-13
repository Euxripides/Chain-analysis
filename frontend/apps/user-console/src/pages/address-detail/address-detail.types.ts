/**
 * 地址详情的类型与纯函数（用户端重构规范 V4 §43）
 *
 * 从 1595 行的 `AddressDetailPage.tsx` 原样抽出，**字段名、口径与派生规则一个都没改**：
 * 这些接口直接对应 overview / assets / activity / counterparties / semantic / flow-profile
 * 六个端点的响应，任何字段改名都等于改契约。
 *
 * 为什么先抽这一层（而不是先拆 tabs）：这里**没有任何 JSX**，
 * 搬运是纯位移，不会影响 9 个业务 Tab 的渲染；而它是 §43 目标结构里
 * `address-detail.types.ts` 的对应文件，也是后续 tabs/ 目录拆分的前置。
 */

export interface Asset {
  token_symbol?: string;
  /** 代币图标（平台自托管路径，CSP 只允许同源图；为空时前端用首字母兜底）。 */
  token_name?: string;
  token_address?: string;
  token_type?: string;
  decimals?: number | null;
  raw_amount?: string;
  display_amount?: string;
  amount_usd?: string;
  token_price?: string;
  snapshot_block?: number;
  observed_at?: string;
  source_provider?: string;
  source?: string;
  is_current_balance?: boolean;
}

export interface NetFlowAsset {
  asset?: string;
  token_symbol?: string;
  token_address?: string;
  decimals?: number | null;
  in_raw?: string;
  out_raw?: string;
  net_raw?: string;
  transaction_count?: number;
  source?: string;
  is_current_balance?: boolean;
}

export interface BalanceBlock<T> {
  source?: string;
  is_current_balance?: boolean;
  observed_at?: string | null;
  items?: T[];
  note?: string;
}

export interface CoverageItem {
  dataset?: string;
  from_block?: number | null;
  to_block?: number | null;
  from_timestamp?: string | null;
  to_timestamp?: string | null;
  updated_at?: string | null;
  complete_through?: string | null;
  status?: string;
  source?: string;
}

export interface OverviewData {
  subject?: { type?: string; chain?: string; address?: string; normalized_address?: string };
  summary?: {
    transaction_count?: number;
    token_transfer_count?: number;
    counterparty_count?: number;
    first_seen?: string | null;
    last_seen?: string | null;
    current_balance?: BalanceBlock<Asset>;
    historical_net_flow?: BalanceBlock<NetFlowAsset>;
  };
  coverage?: { items?: CoverageItem[]; source?: string };
  caliber?: Record<string, unknown>;
  source?: { provider?: string; tables?: string[]; as_of?: string };
}

export interface AssetsPayload {
  items?: { current_balance?: Asset[]; historical_net_flow?: NetFlowAsset[] };
}

export interface ActivityRow {
  chain?: string;
  address?: string;
  counterparty?: string;
  direction?: string;
  activity_type?: string;
  /** 交易方法名（transactions.method_name，活动读按本页哈希回填；为空前端显示 Transfer）。 */
  method_name?: string;
  method_id?: string;
  timestamp?: string;
  block_number?: number;
  tx_hash?: string;
  log_index?: number;
  token_address?: string;
  token_symbol?: string;
  /** 代币图标（平台自托管路径；CSP 只允许同源图，为空时前端用首字母兜底）。 */
  token_logo?: string;
  /**
   * 元数据是否通过平台审核（explorer enrich 的 token_verified）。
   * 未审核 / 未知的代币在列表里带一个警示三角，提示"这个符号不可全信"。
   */
  token_verified?: boolean;
  token_metadata_status?: string;
  /** NFT 转账的合集名（来自 nft_transfers.token_name）。 */
  token_name?: string;
  token_decimals?: number;
  amount_raw?: string;
  amount?: string;
  amount_usd?: string;
  /** NFT 转账读取面（/activity?asset_type=nft）：合约 / token_id / 标准 / 双方地址。 */
  contract_address?: string;
  token_id?: string;
  token_standard?: string;
  from_address?: string;
  to_address?: string;
  /** Solana 证据（ADR-023 D10）：主体/对手方的代币账户与被代扣的手续费；其他链为空。 */
  token_account?: string;
  counterparty_token_account?: string;
  fee_amount_raw?: string;
  dataset?: string;
  source_provider?: string;
  source_ingested_at?: string;
}

export interface CounterpartyRow {
  amount_state?: string;
  assets?: { dataset: string; asset?: string; activity_count?: number; token_address: string; token_symbol: string; token_decimals: number; in_raw?: string | null; out_raw?: string | null; net_raw?: string | null; amount_state?: string; missing_amount_rows?: number }[];
  counterparty?: string;
  transaction_count?: number;
  in_raw?: string;
  out_raw?: string;
  net_raw?: string;
  first_seen?: string;
  last_seen?: string;
}

export interface SemanticEvent {
  id?: string;
  type?: string;
  confidence?: number;
  payload?: unknown;
  provenance?: unknown;
  tx_hash?: string;
  algorithm_version?: string;
  occurred_at?: string;
  computed_at?: string;
}

export interface SemanticResult {
  events?: SemanticEvent[];
  counts?: Record<string, number>;
  algorithm_version?: string;
  data_version?: string;
  computed_at?: string;
  evidence_boundary?: string;
  [key: string]: unknown;
}

export interface AnalysisEdge {
  direction?: string;
  counterparty?: string;
  asset?: string;
  amount?: string;
  flow_ratio?: string;
  transactions?: number;
  first_seen?: string;
  last_seen?: string;
  evidence?: string[];
}

export interface AnalysisData {
  algorithm_version?: string;
  transaction_count?: number;
  unique_counterparties?: number;
  first_seen?: string;
  last_seen?: string;
  edges?: AnalysisEdge[];
  first_funder?: AnalysisEdge | null;
  cashout_candidates?: AnalysisEdge[];
  evidence_boundary?: string;
}

/**
 * 9 个业务 Tab 的**真实契约**（规范 §0.4.3 / §43）。
 *
 * key 是深链接的一部分，**9 个 key 一个都不许增删改**；界面文案按参考站点改成中文
 * （Token Transfer → 代币转账、Internal → 内部交易，2026-09-22 对齐参考站点截图）。
 */
export const TABS = [
  { key: "overview", label: "概览" },
  { key: "assets", label: "资产" },
  { key: "transactions", label: "普通交易" },
  { key: "transfers", label: "代币转账" },
  { key: "internal", label: "内部交易" },
  { key: "counterparties", label: "对手方" },
  { key: "statistics", label: "统计分析" },
  { key: "semantic", label: "语义事件" },
  { key: "analysis", label: "分析" },
  { key: "coverage", label: "Coverage" },
];

export const DIRECTION_LABEL: Record<string, string> = { in: "流入 in", out: "流出 out", self: "自转 self" };

export function decimalsOf(item?: { decimals?: number | null }): number | undefined {
  const value = item?.decimals;
  return typeof value === "number" ? value : undefined;
}

export function blockText(value?: number | null): string {
  return value === null || value === undefined ? "—" : String(value);
}

export function jsonText(value: unknown, max = 140): string {
  try {
    const text = JSON.stringify(value);
    if (!text) return "—";
    return text.length > max ? text.slice(0, max) + "…" : text;
  } catch {
    return "—";
  }
}

export function extractEvents(result: unknown): SemanticEvent[] {
  const candidate = Array.isArray(result)
    ? result
    : result && typeof result === "object" && Array.isArray((result as SemanticResult).events)
      ? (result as SemanticResult).events
      : [];
  return (candidate ?? []).filter((item): item is SemanticEvent => Boolean(item) && typeof item === "object");
}

export function hasSemanticResultShape(result: unknown): boolean {
  return Boolean(result) && typeof result === "object" && Array.isArray((result as SemanticResult).events);
}

export function directionTone(direction?: string): "ok" | "warn" | "neutral" {
  return direction === "in" ? "ok" : direction === "out" ? "warn" : "neutral";
}

export function amountDirection(direction?: string): "in" | "out" | undefined {
  return direction === "in" ? "in" : direction === "out" ? "out" : undefined;
}

/** 法币估值：接口没有给就显示 —（由 Amount 的 fiat 承担），不用 0 冒充。 */
export function fiatText(amountUsd?: string): string | null {
  return amountUsd ? "≈ " + amountUsd + " USD" : null;
}

export function addressPath(chain: string, value: string): string {
  return "/addresses/" + encodeURIComponent(chain) + "/" + encodeURIComponent(value);
}

export function transactionPath(chain: string, value: string): string {
  return "/transactions/" + encodeURIComponent(chain) + "/" + encodeURIComponent(value);
}
