import assert from "node:assert/strict";
import { createServer as createHTTPServer, type Server } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import test, { after, before } from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";
import { toQuery } from "../src/rest-client.js";

const mcpKey = "test-mcp-secret";
const restKey = "test-rest-secret";
let restServer: Server;
let mcpServer: Server;
let mcpUrl: URL;
let lastRESTRequest = { url: "", authorization: "" };
let lastRESTBody = "";
let activeEnsureRequests = 0;
let peakEnsureRequests = 0;

before(async () => {
  restServer = createHTTPServer((req, res) => {
    lastRESTRequest = { url: req.url ?? "", authorization: req.headers.authorization ?? "" };
    const chunks: Buffer[] = [];
    req.on("data", (chunk) => chunks.push(Buffer.from(chunk)));
    req.on("end", () => {
      lastRESTBody = Buffer.concat(chunks).toString("utf8");
      res.setHeader("content-type", "application/json");
      const finish = () => res.end(JSON.stringify({ success: true, data: { chain: "tron", address: "TValid", download_job_id: "00000000-0000-4000-8000-000000000000" }, meta: {}, error: null }));
      if (req.url === "/api/v1/data/ensure") {
        activeEnsureRequests++;
        peakEnsureRequests = Math.max(peakEnsureRequests, activeEnsureRequests);
        setTimeout(() => { activeEnsureRequests--; finish(); }, 80);
      } else {
        finish();
      }
    });
  });
  restServer.listen(0, "127.0.0.1");
  await once(restServer, "listening");
  const restPort = (restServer.address() as AddressInfo).port;
  const config: Config = {
    host: "127.0.0.1",
    allowedHosts: ["127.0.0.1", "localhost"],
    port: 0,
    restBaseUrl: `http://127.0.0.1:${restPort}/api/v1`,
    restApiKey: restKey,
    mcpKeyHashes: [sha256(mcpKey)],
    oauthAccessCodeHashes: [sha256(mcpKey)],
    oauthSigningKey: sha256("test-oauth-signing-key"),
    publicBaseUrl: "http://127.0.0.1",
    publicMcpUrl: "http://127.0.0.1/mcp",
    requestTimeoutMs: 5_000,
    maxConcurrent: 4,
    rateLimitPerMinute: 100,
  };
  mcpServer = createHTTPServer(createApp(config));
  mcpServer.listen(0, "127.0.0.1");
  await once(mcpServer, "listening");
  mcpUrl = new URL(`http://127.0.0.1:${(mcpServer.address() as AddressInfo).port}/mcp`);
});

after(async () => {
  mcpServer.close();
  restServer.close();
  await Promise.all([once(mcpServer, "close"), once(restServer, "close")]);
});

test("query builder omits empty and all values", () => {
  assert.equal(toQuery({ chain: "tron", direction: "all", token: "", page: 1 }), "chain=tron&page=1");
});

test("MCP endpoint rejects missing bearer authentication", async () => {
  const response = await fetch(mcpUrl, { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
  assert.equal(response.status, 401);
  assert.match(response.headers.get("www-authenticate") ?? "", /^Bearer/);
});

test("health reports the actual registered tool count", async () => {
  const healthURL = new URL("/health", mcpUrl);
  const response = await fetch(healthURL);
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), { status: "ok", tools: 23 });
});

test("initialize, list tools and call a real REST-backed tool", async () => {
  const client = new Client({ name: "mcp-test", version: "1.0.0" });
  const transport = new StreamableHTTPClientTransport(mcpUrl, {
    requestInit: { headers: { authorization: `Bearer ${mcpKey}` } },
  });
  await client.connect(transport);
  const listed = await client.listTools();
  assert.equal(listed.tools.length, 23);
  assert.deepEqual(listed.tools.map((tool) => tool.name), [
    "get_address_overview", "get_address_assets", "get_token_transfers", "get_transactions",
    "get_internal_transactions", "get_counterparties", "get_flow_summary", "get_top_flows",
    "get_first_funding", "get_transaction_detail", "batch_get_address_overview",
    "batch_get_address_assets", "get_transaction_trace", "get_transaction_logs",
    "decode_transaction", "find_fund_paths", "build_address_network",
    "create_large_export", "get_export_status", "ensure_chain_data", "ensure_chain_data_batch", "get_ai_download_status", "retry_ai_download_failures",
  ]);
  assert.equal(listed.tools.filter((tool) => tool.annotations?.readOnlyHint === true).length, 19);
  assert.equal(listed.tools.find((tool) => tool.name === "create_large_export")?.annotations?.readOnlyHint, false);
  const analysisChains = (listed.tools.find((tool) => tool.name === "get_address_overview")?.inputSchema as any).properties.chain.enum;
  const downloadChains = (listed.tools.find((tool) => tool.name === "ensure_chain_data")?.inputSchema as any).properties.chain.enum;
  assert.ok(!analysisChains.includes("solana"), "query tools must not advertise Solana analysis before those contracts are complete");
  assert.ok(downloadChains.includes("solana"), "download tool must advertise the deployed Solana acquisition provider");
  for (const name of ["find_fund_paths", "build_address_network"]) {
    const schema = listed.tools.find((tool) => tool.name === name)?.inputSchema as { required?: string[]; properties?: Record<string, unknown> };
    assert.ok(schema.properties?.token, `${name} must advertise token in tools/list`);
    assert.ok(schema.properties?.token_contract, `${name} must advertise token_contract in tools/list`);
    assert.ok(!schema.required?.includes("token"), `${name} must allow contract-only requests`);
    assert.ok(!schema.required?.includes("token_contract"), `${name} must allow symbol-only requests`);
  }

  const result = await client.callTool({ name: "get_address_overview", arguments: { chain: "tron", address: "TValid" } });
  assert.equal(result.isError, undefined);
  assert.equal((result.structuredContent as { success: boolean }).success, true);
  assert.equal(lastRESTRequest.authorization, `Bearer ${restKey}`);
  assert.equal(lastRESTRequest.url, "/api/v1/address/overview?chain=tron&address=TValid");

  const representative: Record<string, Record<string, unknown>> = {
    get_address_assets: { chain: "tron", address: "TValid" },
    get_token_transfers: { chain: "tron", address: "TValid", page_size: 100 },
    get_transactions: { chain: "tron", address: "TValid", page_size: 100 },
    get_internal_transactions: { chain: "tron", address: "TValid", page_size: 100 },
    get_counterparties: { chain: "tron", address: "TValid", limit: 20 },
    get_flow_summary: { chain: "tron", address: "TValid", token: "USDT" },
    get_top_flows: { chain: "tron", address: "TValid", limit: 20 },
    get_first_funding: { chain: "tron", address: "TValid" },
    get_transaction_detail: { chain: "tron", tx_hash: "abc123" },
    batch_get_address_overview: { chain: "tron", addresses: ["TValid"] },
    batch_get_address_assets: { chain: "tron", addresses: ["TValid"] },
    get_transaction_trace: { chain: "tron", tx_hash: "abc123" },
    get_transaction_logs: { chain: "tron", tx_hash: "abc123" },
    decode_transaction: { chain: "tron", tx_hash: "abc123" },
    find_fund_paths: { chain: "tron", source: "TValid", token: "USDT", max_depth: 3, max_nodes: 200 },
    build_address_network: { chain: "tron", seed_addresses: ["TValid"], token: "USDT", max_depth: 3, max_nodes: 200 },
    create_large_export: { chain: "tron", address: "TValid", data_types: ["token_transfers"], format: "parquet", compression: "zstd" },
    get_export_status: { job_id: "00000000-0000-4000-8000-000000000000" },
    ensure_chain_data: { chain: "tron", addresses: ["TValid"], datasets: ["token_transfers"], range: { type: "time", from: "2026-09-01T00:00:00Z", to: "2026-09-02T00:00:00Z" }, performance_mode: "fast" },
    ensure_chain_data_batch: { requests: [
      { chain: "tron", addresses: ["TValid1"], datasets: ["token_transfers"], range: { type: "full" }, performance_mode: "fast" },
      { chain: "solana", addresses: ["11111111111111111111111111111111"], datasets: ["transactions"], range: { type: "full" }, performance_mode: "fast" },
    ], max_concurrent: 2 },
    get_ai_download_status: { download_job_id: "00000000-0000-4000-8000-000000000000" },
    retry_ai_download_failures: { download_job_id: "00000000-0000-4000-8000-000000000000" },
  };
  for (const [name, args] of Object.entries(representative)) {
    const called = await client.callTool({ name, arguments: args });
    assert.equal(called.isError, undefined, `${name} should return structured content`);
    assert.equal((called.structuredContent as { success: boolean }).success, true);
  }
  assert.equal(lastRESTRequest.url, "/api/v1/data/download-retry");
  assert.deepEqual(JSON.parse(lastRESTBody), { download_job_id: "00000000-0000-4000-8000-000000000000" });
  peakEnsureRequests = 0;
  const concurrent = await client.callTool({ name: "ensure_chain_data_batch", arguments: { requests: [
    { chain: "tron", addresses: ["T1"], datasets: ["token_transfers"], range: { type: "full" } },
    { chain: "bsc", addresses: ["0x0000000000000000000000000000000000000001"], datasets: ["transactions"], range: { type: "full" } },
    { chain: "avalanche", addresses: ["0x0000000000000000000000000000000000000002"], datasets: ["token_transfers"], range: { type: "full" } },
  ], max_concurrent: 3 } });
  assert.equal(concurrent.isError, undefined);
  assert.ok(peakEnsureRequests >= 2, `batch ensure should overlap REST submissions, peak=${peakEnsureRequests}`);
  const posted = await client.callTool({ name: "batch_get_address_overview", arguments: { chain: "tron", addresses: ["TValid"] } });
  assert.equal(posted.isError, undefined);
  assert.equal(lastRESTRequest.url, "/api/v1/addresses/batch-overview");
  assert.deepEqual(JSON.parse(lastRESTBody), { chain: "tron", addresses: ["TValid"] });
  const oversized = await client.callTool({
    name: "get_token_transfers",
    arguments: { chain: "tron", address: "TValid", page_size: 501 },
  });
  assert.equal(oversized.isError, true, "page_size above 500 must be rejected before REST access");
  const crossAssetPath = await client.callTool({ name: "find_fund_paths", arguments: { chain: "tron", source: "TValid" } });
  assert.equal(crossAssetPath.isError, true);
  assert.match((crossAssetPath.content[0] as { text: string }).text, /token or token_contract is required/);
  const contractOnlyPath = await client.callTool({ name: "find_fund_paths", arguments: { chain: "tron", source: "TValid", token_contract: "TContract" } });
  assert.equal(contractOnlyPath.isError, undefined, "contract-only path request must reach REST");
  assert.deepEqual(JSON.parse(lastRESTBody), { chain: "tron", source: "TValid", token_contract: "TContract", max_depth: 3, max_nodes: 200 });
  const requestBeforeInvalidExport = { ...lastRESTRequest, body: lastRESTBody };
  for (const args of [
    { chain: "tron", address: "TValid", data_types: ["token_transfers"], format: "parquet", compression: "zip" },
    { chain: "tron", address: "TValid", data_types: ["token_transfers"], format: "csv", compression: "zstd" },
  ]) {
    const invalidExport = await client.callTool({ name: "create_large_export", arguments: args });
    assert.equal(invalidExport.isError, true, `${args.format}+${args.compression} must be rejected`);
    assert.equal(lastRESTRequest.url, requestBeforeInvalidExport.url, "invalid export must not reach REST");
    assert.equal(lastRESTBody, requestBeforeInvalidExport.body, "invalid export must not change REST request body");
  }
  await client.close();
});
