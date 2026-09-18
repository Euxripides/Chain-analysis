// V1.3 Count Providers（§72-§74）：Provider/Chain/Dataset 运行视图（数据源 /api/v1/count-oracle/providers）。
import { useState } from "react";
import { useScopedQuery as useQuery } from "../../lib/sessionScope";
import { DatabaseZap, Gauge } from "lucide-react";
import { countOracleHttp } from "../../api/client";
import NoticeDialog from "../../components/NoticeDialog";

type CapRow = {
  provider_id: string;
  chain_id: number;
  dataset: string;
  enabled: boolean;
  runtime_status: string;
  supports_range: boolean;
  range_verified: boolean;
  supports_batch: boolean;
  max_batch_size: number;
  semantics: string;
  accuracy: string;
  reason: string;
};

type ProfileRow = {
  provider_id: string;
  chain_id: number;
  dataset: string;
  state: string;
  active: number;
  queue_depth: number;
  safe_concurrency: number;
  hard_ceiling: number;
  success_rps: number;
  valid_count_rps: number;
  p50_ms: number;
  p95_ms: number;
  success_rate: number;
  rate_limit_rate: number;
  timeout_rate: number;
  cost_per_1k_counts: number;
  requests: number;
  success_count: number;
  last_error: string;
  updated_at: string;
};

type ProvidersResp = {
  port_enabled: boolean;
  port_shadow: boolean;
  router_enabled: boolean;
  global_max_inflight: number;
  chainbase_tx_enabled: boolean;
  chainbase_token_enabled: boolean;
  chainbase_max_concurrency: number;
  chainbase_max_rps: number;
  mockparallel_enabled: boolean;
  registries: { provider_id: string; display_name: string; enabled: boolean; adapter_type: string }[];
  capabilities: CapRow[];
  profiles: ProfileRow[];
};

const chainName = (id: number) => (id === 0 ? "*" : String(id));
const pct1 = (v?: number) => (Number(v || 0) * 100).toFixed(1) + "%";
const num = new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 1 });

function StateChip({ value }: { value: string }) {
  const tone =
    value === "AVAILABLE" || value === "CLOSED"
      ? "ok"
      : value === "BLOCKED" || value === "OPEN"
        ? "bad"
        : value === "THROTTLED" || value === "DEGRADED" || value === "RECOVERING"
          ? "warn"
          : "idle";
  return <span className={"ac-state ac-state-" + tone}>{value || "—"}</span>;
}

export default function CountProviders() {
  const [dismissed, setDismissed] = useState(false);
  const q = useQuery({
    queryKey: ["count-providers"],
    queryFn: () => countOracleHttp<ProvidersResp>("/providers"),
    refetchInterval: 10_000,
  });
  const rows = (q.data?.profiles ?? []).sort((a, b) =>
    (a.provider_id + a.dataset).localeCompare(b.provider_id + b.dataset),
  );
  const capOf = (p: string, chain: number, ds: string): CapRow | undefined =>
    q.data?.capabilities.find(
      (c) => c.provider_id === p && c.chain_id === chain && c.dataset === ds,
    );
  return (
    <section className="ac-panel ac-table-panel">
      <div className="ac-panel-head">
        <div>
          <small>V1.3 Count Oracle Provider Port（§72-§74）</small>
          <h2>Count Providers</h2>
        </div>
        <div>
          <span className="ac-meta">
            Port " + (q.data?.port_enabled ? "ON" : "OFF") + " · Shadow " + (q.data?.port_shadow ? "ON" : "OFF") + " · Router " + (q.data?.router_enabled ? "ON" : "OFF") + " · 全局预算 " + (q.data?.global_max_inflight ?? 0)
          </span>
        </div>
      </div>
      {q.isPending && <div className="ac-skeleton"><i /><i /><i /></div>}
      {q.error && !dismissed && (
        <NoticeDialog
          title="Count Providers 数据加载失败"
          message={q.error.message}
          onClose={() => setDismissed(true)}
        />
      )}
      {!q.isPending && !q.error && !rows.length && <div className="ac-empty"><DatabaseZap size={24} /><b>暂无 Provider Runtime（等待 worker 心跳）</b></div>}
      {!q.isPending && !q.error && rows.length > 0 && (
        <>
          <div className="ac-table-scroll">
            <table className="ac-table">
              <thead>
                <tr>
                  <th>Provider</th><th>Chain</th><th>Dataset</th><th>Status</th>
                  <th>Active</th><th>Queue</th><th>SafeConc</th><th>Ceiling</th>
                  <th>SuccessRPS</th><th>ValidRPS</th><th>P50</th><th>P95</th>
                  <th>429率</th><th>Timeout率</th><th>成功率</th><th>$/1K</th><th>Range</th>
                </tr>
              </thead>
              <tbody>
                {rows.map((r) => {
                  const cap = capOf(r.provider_id, r.chain_id, r.dataset);
                  return (
                    <tr key={r.provider_id + r.chain_id + r.dataset}>
                      <td><b>{r.provider_id}</b></td>
                      <td>{chainName(r.chain_id)}</td>
                      <td>{r.dataset}</td>
                      <td><StateChip value={r.state} /></td>
                      <td>{r.active}</td><td>{r.queue_depth}</td>
                      <td>{r.safe_concurrency}</td><td>{r.hard_ceiling}</td>
                      <td>{num.format(r.success_rps || 0)}</td>
                      <td>{num.format(r.valid_count_rps || 0)}</td>
                      <td>{r.p50_ms || "—"}</td><td>{r.p95_ms || "—"}</td>
                      <td>{pct1(r.rate_limit_rate)}</td><td>{pct1(r.timeout_rate)}</td>
                      <td>{pct1(r.success_rate)}</td>
                      <td>{num.format(r.cost_per_1k_counts || 0)}</td>
                      <td>{cap ? (cap.range_verified ? "verified" : cap.supports_range ? "range" : "—") : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <footer className="ac-panel-foot">
            <span><DatabaseZap size={13} /> 请求 {rows.reduce((a, r) => a + r.requests, 0)} · 成功 {rows.reduce((a, r) => a + r.success_count, 0)}</span>
            <span><Gauge size={13} /> 每 Provider 独立 Queue / Executor / Rate Controller / Circuit（§30）</span>
          </footer>
        </>
      )}
    </section>
  );
}