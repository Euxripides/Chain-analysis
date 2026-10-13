import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Space } from "antd";
import { useNavigate } from "react-router-dom";
import { useAuth } from "@chain/api-client";
import { useDialog } from "@chain/ui";
import type { DownloadBody, DownloadPreviewData } from "../downloads/download.types";

export type LookupKind = "address" | "transaction" | "block" | "token";
type Resolution = { chain: string; chain_name?: string; kind: LookupKind; value: string; status: "FOUND" | "PENDING" | "NOT_FOUND" | "UNCONFIRMED"; local_data?: boolean; needs_completion?: boolean; datasets?: string[]; coverage?: Array<{ dataset: string; reason: string; complete_through: string | null }> };
const DATASET_NAMES: Record<string, string> = { transactions: "普通交易", token_transfers: "代币转账", nft_transfers: "NFT 转账", internal_transactions: "内部调用" };

export function validateLookup(chain: string, input: string): string | null {
  const q = input.trim();
  if (!chain || !q) return "请选择链并输入查询内容";
  if ([...q].length > 160 || /[\u0000-\u001f\u007f]/.test(q)) return "查询内容过长或包含无效字符";
  if (/^\d+$/.test(q) && !(chain === "solana" && q.length >= 32)) return BigInt(q) > 18446744073709551615n ? "区块高度超出范围" : null;
  if (/^0x/i.test(q) && !/^0x([\da-f]{40}|[\da-f]{64})$/i.test(q)) return "地址或哈希格式不合法";
  if (chain === "tron" && /^0x[\da-f]{40}$/i.test(q)) return "TRON 地址需要使用 Base58 格式";
  if (q.length === 64 && chain !== "solana" && !/^[\da-f]{64}$/i.test(q)) return "交易哈希格式不合法";
  return null;
}

export function useExplorerLookup(onOpen?: (chain: string, value: string, kind: LookupKind) => void) {
  const { client, can, session } = useAuth(); const dialog = useDialog(); const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const flight = useRef<{ controller: AbortController; dismiss?: () => void } | null>(null);
  const cancel = useCallback(() => { flight.current?.controller.abort(); flight.current?.dismiss?.(); flight.current = null; setBusy(false); }, []);
  const scope = `${session?.organization_id ?? ""}/${session?.workspace_id ?? ""}`;
  useEffect(() => { cancel(); return cancel; }, [scope, cancel]);

  const search = async (chain: string, input: string) => {
    if (flight.current) return;
    const invalid = validateLookup(chain, input);
    if (invalid) { dialog.notify("warning", "查询内容不合法", invalid); return; }
    const current = { controller: new AbortController(), dismiss: undefined as (() => void) | undefined };
    flight.current = current; setBusy(true);
    const active = () => flight.current === current && !current.controller.signal.aborted;
    const open = (value: string, kind: LookupKind) => {
      if (!active()) return;
      onOpen?.(chain, value, kind);
      const segment = kind === "transaction" ? "transactions" : kind === "block" ? "blocks" : kind === "token" ? "tokens" : "addresses";
      const normalized = kind === "transaction" && chain !== "solana" ? value.replace(/^0x/i, "").toLowerCase() : value;
      navigate(`/${segment}/${encodeURIComponent(chain)}/${encodeURIComponent(normalized)}`);
    };
    try {
      const { data } = await client.get<Resolution>("/api/v2/explorer/resolve", { query: { chain, value: input.trim() }, signal: current.controller.signal });
      if (!active()) return;
      if (data.status === "NOT_FOUND") { dialog.notify("info", "未找到链上记录", "所选链没有返回这个哈希或区块的记录。", [{ label: "链", value: chain }, { label: "查询", value: data.value }]); return; }
      if (data.status === "UNCONFIRMED") { dialog.notify("info", "尚未确认链上记录", "输入格式合法，本地和当前链上读取能力尚未确认记录。可以核对链与输入后重试。", [{ label: "链", value: chain }, { label: "查询", value: data.value }]); return; }
      if (data.kind === "token" && !/^0x[\da-f]{40}$/i.test(data.value) && !/^T[1-9A-HJ-NP-Za-km-z]{33}$/.test(data.value)) {
        const response = await client.get<{ items: Array<{ contract_address: string; symbol: string; name: string }> }>("/api/v2/explorer/tokens", { query: { chain, q: data.value, limit: 20 }, signal: current.controller.signal });
        if (!active()) return;
        const items = response.data.items;
        if (!items.length) { dialog.notify("info", "没有匹配的代币", data.value); return; }
        if (items.length === 1) { open(items[0].contract_address, "token"); return; }
        let selected: string | null = null;
        const review = dialog.openSession({ kind: "info", title: "选择代币", body: <div className="cs-token-search-results">{items.map(item => <Button key={item.contract_address} block onClick={() => { selected = item.contract_address; review.dismiss(); }}><Space direction="vertical" size={0}><strong>{item.symbol} · {item.name}</strong><span className="mono">{item.contract_address}</span></Space></Button>)}</div>, actions: [{ label: "取消", result: "", variant: "default" }] });
        current.dismiss = review.dismiss; await review.result;
        if (selected) open(selected, "token"); return;
      }
      if (data.kind !== "address" || !data.needs_completion) { open(data.value, data.kind); return; }
      if (!can("download:create")) {
        const review = dialog.openSession({ kind: "info", title: "地址数据需要补全", body: "地址已有链上记录，当前账号没有创建下载任务的权限。", details: [{ label: "地址", value: data.value }], actions: data.local_data ? [{ label: "查看本地数据", result: "local", variant: "primary" }, { label: "关闭", result: "", variant: "default" }] : [{ label: "关闭", result: "", variant: "default" }] });
        current.dismiss = review.dismiss;
        const answer = await review.result;
        if (answer === "local") open(data.value, "address"); return;
      }
      const body: DownloadBody = { chain, addresses: [data.value], datasets: data.datasets ?? [], range: { type: "full" }, reuse_policy: "prefer_local", performance_mode: "safe", create_missing_meta: false };
      if (!body.datasets.length) { dialog.notify("info", "当前链没有可用的全量历史数据源"); return; }
      const preview = await client.post<DownloadPreviewData>("/api/v2/downloads/preview", body, { signal: current.controller.signal });
      if (!active()) return;
      const candidate = preview.data.reuse?.candidate;
      const reason = data.coverage?.some(item => item.reason === "historical_gap") ? "历史中间存在未覆盖区间" : data.coverage?.some(item => item.reason === "stale_tail") ? "尾部未更新已达到七天" : "历史覆盖尚未验证完整";
      const review = dialog.openSession({ kind: "confirm", title: candidate ? "复用地址补全任务" : "补全地址历史数据", body: reason, details: [
        { label: "链", value: data.chain_name ?? chain }, { label: "地址", value: data.value },
        { label: "范围", value: "全量历史，截止任务创建时固定的链头；复用已验证覆盖区间" },
        { label: "数据集", value: body.datasets.map(id => DATASET_NAMES[id] ?? id).join("、") },
        { label: "预计成本", value: preview.data.estimate?.estimated_cost_usd == null ? "暂无法估算" : `${preview.data.estimate.estimated_cost_usd} USD` },
        ...(candidate ? [{ label: "现有任务", value: `${candidate.job_no} · ${candidate.status}` }] : []),
      ], actions: [{ label: candidate ? "复用任务" : "补全数据", result: "complete", variant: "primary" }, ...(data.local_data ? [{ label: "查看本地数据", result: "local", variant: "default" as const }] : []), { label: "取消", result: "", variant: "default" }] });
      current.dismiss = review.dismiss;
      const answer = await review.result;
      if (!active()) return;
      if (answer === "local") { open(data.value, "address"); return; }
      if (answer !== "complete") return;
      const created = await client.post<{ job_id: string | null; job_no?: string }>("/api/v2/downloads", body, { idempotencyKey: crypto.randomUUID(), signal: current.controller.signal });
      if (!active()) return;
      if (created.data.job_id && can("job:read")) navigate(`/jobs/${encodeURIComponent(created.data.job_id)}?lookup_chain=${encodeURIComponent(chain)}&lookup_address=${encodeURIComponent(data.value)}`);
      else if (created.data.job_id) dialog.notify("success", "补全任务已提交", created.data.job_no ?? created.data.job_id);
      else open(data.value, "address");
    } catch (error) { if (active()) dialog.notify("error", "查询未完成", error instanceof Error ? error.message : String(error)); }
    finally { if (flight.current === current) { flight.current = null; setBusy(false); } }
  };
  return { search, busy, cancel };
}
