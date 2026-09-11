import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const url = process.env.MCP_URL;
const apiKey = process.env.MCP_API_KEY;
if (!url || !apiKey) throw new Error("MCP_URL and MCP_API_KEY are required");

const address = "T9ygmX26tszHxPiHvbPwjDb92UkfC4YXPU";
const txHash = "4a4aab38f76978bd05d55b1bcfadbd1673b7844b78241efee4f2416ca3735b8f";
const client = new Client({ name: "blockchain-v1-e2e-acceptance", version: "1.0.0" });
await client.connect(new StreamableHTTPClientTransport(new URL(url), {
  requestInit: { headers: { authorization: `Bearer ${apiKey}` } },
}));

const evidence = [];
async function call(test, name, args) {
  const started = performance.now();
  const result = await client.callTool({ name, arguments: args });
  const elapsed = Math.round(performance.now() - started);
  const structured = result.structuredContent ?? null;
  const encoded = JSON.stringify(structured);
  evidence.push({ test, name, args, elapsed_ms: elapsed, response_bytes: Buffer.byteLength(encoded), is_error: !!result.isError, structured });
  return structured;
}

await call(1, "get_address_overview", { chain: "tron", address });
await call(1, "get_address_assets", { chain: "tron", address });
await call(1, "get_flow_summary", { chain: "tron", address, token: "USDT" });
const counterparties = await call(1, "get_counterparties", { chain: "tron", address, token: "USDT", limit: 50 });

await call(2, "get_top_flows", { chain: "tron", address, token: "USDT", direction: "out", group_by: "address", limit: 20 });

await call(3, "get_counterparties", { chain: "tron", address, token: "USDT", limit: 50 });
await call(3, "get_top_flows", { chain: "tron", address, token: "USDT", group_by: "address", limit: 20 });
const cps = Array.isArray(counterparties?.data) ? counterparties.data.map(x => x.counterparty).filter(Boolean).slice(0, 20) : [];
if (cps.length) {
  await call(3, "batch_get_address_overview", { chain: "tron", addresses: cps });
  await call(4, "batch_get_address_assets", { chain: "tron", addresses: cps, token: "USDT", min_balance: "1000000" });
}

await call(5, "find_fund_paths", { chain: "tron", source: address, token: "USDT", max_depth: 3, max_nodes: 200 });

await call(6, "get_transaction_detail", { chain: "tron", tx_hash: txHash });
await call(6, "decode_transaction", { chain: "tron", tx_hash: txHash });
await call(6, "get_transaction_logs", { chain: "tron", tx_hash: txHash });
await call(6, "get_transaction_trace", { chain: "tron", tx_hash: txHash });

const created = await call(7, "create_large_export", { chain: "tron", address, data_types: ["token_transfers"], token: "USDT", format: "parquet", compression: "zstd" });
const jobId = created?.data?.job_id;
if (jobId) {
  for (let i = 0; i < 12; i++) {
    const status = await call(7, "get_export_status", { job_id: jobId });
    if (["completed", "failed"].includes(status?.data?.status)) break;
    await new Promise(resolve => setTimeout(resolve, 2500));
  }
}

const compact = evidence.map(e => ({
  test: e.test, tool: e.name, args: e.args, elapsed_ms: e.elapsed_ms,
  response_bytes: e.response_bytes, is_error: e.is_error,
  success: e.structured?.success,
  coverage_complete: e.structured?.meta?.coverage?.complete,
  freshness: e.structured?.meta?.freshness?.as_of_time,
  pagination: e.structured?.meta?.pagination,
  truncated: e.structured?.data?.truncated,
  node_limit_reached: e.structured?.data?.node_limit_reached,
  item_count: Array.isArray(e.structured?.data) ? e.structured.data.length : undefined,
  export_job_id: e.name === "create_large_export" ? e.structured?.data?.job_id : undefined,
  export_status: e.name === "get_export_status" ? e.structured?.data?.status : undefined,
}));
console.log(JSON.stringify({ address, tx_hash: txHash, calls: compact }, null, 2));
await client.close();
