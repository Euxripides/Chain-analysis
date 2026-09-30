import assert from "node:assert/strict";
import { createServer } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import test from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";

test("MCP export summary returns exact statistics and original details without a download resource", async () => {
  const jobID = "00000000-0000-4000-8000-000000000000";
  const payer = "0x1111111111111111111111111111111111111111";
  const precise = "900719925474099312.345678901234567893";
  const envelope = {
    success: true, error: null, meta: { source: "completed_export_file" },
    data: {
      job_id: jobID, total_records: 3, total_addresses: 2, total_amount: precise,
      data_cutoff_block: "99000000", has_more: true, next_cursor: "address.next",
      details_has_more: true, next_detail_cursor: "detail.next",
      addresses: [{ from_address: payer, record_count: 2, total_amount: precise }],
      details: [{ record_id: "token_transfers.csv:1", from_address: payer, amount: precise, amount_raw: "900719925474099312345678901234567893", fields: { log_index: "3", tx_hash: "original" } }],
    },
  };
  const received: URL[] = [];
  let fail = false;
  const rest = createServer((req, res) => {
    assert.equal(req.headers.authorization, "Bearer rest-test-secret");
    const url = new URL(req.url!, "http://localhost");
    assert.equal(url.pathname, "/api/v1/export/address-summary");
    received.push(url);
    res.setHeader("Content-Type", "application/json");
    res.statusCode = fail ? 409 : 200;
    res.end(JSON.stringify(fail ? { success: false, data: null, meta: {}, error: { code: "EXPORT_NOT_COMPLETED", message: "Only COMPLETED exports can be summarized" } } : envelope));
  });
  rest.listen(0, "127.0.0.1");
  await once(rest, "listening");
  const config: Config = {
    host: "127.0.0.1", allowedHosts: ["127.0.0.1"], port: 0,
    restBaseUrl: `http://127.0.0.1:${(rest.address() as AddressInfo).port}/api/v1`, restApiKey: "rest-test-secret",
    mcpKeyHashes: [sha256("mcp-test-secret")], oauthAccessCodeHashes: [sha256("mcp-test-secret")],
    oauthSigningKey: sha256("signing-test-secret"), publicBaseUrl: "https://aurore.online", publicMcpUrl: "https://aurore.online/mcp",
    requestTimeoutMs: 5000, maxConcurrent: 4, rateLimitPerMinute: 100,
  };
  const mcp = createServer(createApp(config));
  mcp.listen(0, "127.0.0.1");
  await once(mcp, "listening");
  const client = new Client({ name: "export-summary-test", version: "1.0.0" });
  try {
    await client.connect(new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${(mcp.address() as AddressInfo).port}/mcp`), { requestInit: { headers: { authorization: "Bearer mcp-test-secret" } } }));
    const definition = (await client.listTools()).tools.find(t => t.name === "get_export_address_summary");
    assert.ok(definition);
    assert.equal(definition.annotations?.readOnlyHint, true);
    const properties = definition.inputSchema.properties as Record<string, { maximum?: number }>;
    assert.equal(properties.page_size.maximum, 500);
    assert.equal(properties.detail_page_size.maximum, 500);
    const args = { job_id: jobID, token: "USDT", direction: "in", from_address: payer, page_size: 1, detail_page_size: 1, cursor: "address.current", detail_cursor: "detail.current" };
    const result = await client.callTool({ name: "get_export_address_summary", arguments: args });
    assert.equal(result.isError, undefined);
    assert.deepEqual(result.structuredContent, envelope);
    assert.equal(result.content.length, 1);
    assert.equal(result.content[0].type, "text");
    assert.match((result.content[0] as { text: string }).text, /3 matching records/);
    assert.match((result.content[0] as { text: string }).text, /900719925474099312\.345678901234567893/);
    for (const [key, value] of Object.entries(args)) assert.equal(received[0].searchParams.get(key), String(value));
    const before = received.length;
    const noToken = await client.callTool({ name: "get_export_address_summary", arguments: { job_id: jobID } });
    assert.equal(noToken.isError, true);
    assert.equal(received.length, before, "cross-asset request must not reach REST");
    fail = true;
    const incomplete = await client.callTool({ name: "get_export_address_summary", arguments: { job_id: jobID, token_contract: payer } });
    assert.equal(incomplete.isError, true);
    assert.match((incomplete.content[0] as { text: string }).text, /EXPORT_NOT_COMPLETED/);
  } finally {
    await client.close();
    await Promise.all([new Promise<void>(resolve => rest.close(() => resolve())), new Promise<void>(resolve => mcp.close(() => resolve()))]);
  }
});
