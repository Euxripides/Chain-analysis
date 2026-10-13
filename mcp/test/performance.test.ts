import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { createServer, type Server } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import { createHash } from "node:crypto";
import { z } from "zod";
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { InMemoryTransport } from "@modelcontextprotocol/sdk/inMemory.js";
import { RESTClient, type RESTEnvelope } from "../src/rest-client.js";
import { executeBatch, type ToolDefinition } from "../src/batch.js";
import { registerTools } from "../src/tools.js";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";

const address = "0x1111111111111111111111111111111111111111";
const precise = "900719925474099312.345678901234567893";
const job = "00000000-0000-4000-8000-000000000001";
const ok = (): RESTEnvelope => ({ success: true, data: { amount: precise }, meta: {}, error: null });
const operation: ToolDefinition = { name: "probe", title: "probe", description: "probe", path: "/probe", method: "POST",
  inputSchema: { address: z.string() }, summarize: () => "probe" };
const spec = { kind: "addresses" as const, operation, key: "address" as const };
const inputs = { addresses: ["fast", "slow", "queued-1", "queued-2", "queued-3"], max_concurrent: 1 };

async function listen(server: Server, t: TestContext) {
  server.listen(0, "127.0.0.1"); await once(server, "listening");
  t.after(async () => { server.closeAllConnections(); await new Promise<void>(resolve => server.close(() => resolve())); });
  return `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
}

async function slowREST(t: TestContext) {
  const received: string[] = [];
  let startedSlow!: () => void, closedSlow!: () => void;
  const started = new Promise<void>(resolve => { startedSlow = resolve; });
  const closed = new Promise<void>(resolve => { closedSlow = resolve; });
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", chunk => chunks.push(Buffer.from(chunk)));
    req.on("end", () => {
      const value = req.method === "POST" ? JSON.parse(Buffer.concat(chunks).toString()) : Object.fromEntries(new URL(req.url!, "http://localhost").searchParams);
      received.push(value.address);
      if (value.address !== "fast" && value.address !== address) {
        startedSlow();
        res.once("close", closedSlow);
        return; // The client deadline or disconnect must close this request.
      }
      res.setHeader("content-type", "application/json"); res.end(JSON.stringify(ok()));
    });
  });
  const url = await listen(server, t);
  return { url, received, started, closed };
}

test("batch deadline keeps completed monetary evidence and never dispatches queued requests", { timeout: 5000 }, async t => {
  const h = await slowREST(t);
  const client = new RESTClient({ baseUrl: h.url, apiKey: "test", timeoutMs: 2000 });
  // The deadline tests ongoing work, not Node/Undici's first-use startup under
  // the parallel test runner. Warm the actual connection before starting it.
  await client.post("/probe", { address: "fast" });
  h.received.length = 0;
  const begun = performance.now();
  const result = await executeBatch(client, spec, inputs, () => {}, { budgetMs: 250 });
  assert.ok(performance.now() - begun < 1500, "overall budget must abort a hanging upstream");
  // Assert dispatch before waiting for a close event. If startup consumed the
  // budget, no slow handler exists and its close promise cannot ever resolve.
  assert.deepEqual(h.received, ["fast", "slow"]);
  assert.equal(result.items[0].success, true);
  assert.equal((result.items[0].response!.data as any).amount, precise);
  assert.equal(result.items[1].error!.code, "BATCH_DEADLINE_EXCEEDED");
  assert.ok(result.items.slice(2).every(item => item.started === false && item.error!.code === "BATCH_DEADLINE_EXCEEDED"));
  assert.equal(result.envelope.meta.skipped, 3);
  assert.equal(result.envelope.meta.deadline_exceeded, true);
  assert.deepEqual((result.envelope.data as any).results.map((item: any) => item.index), [0, 1, 2, 3, 4]);
  let closeTimer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([h.closed, new Promise<void>((_resolve, reject) => { closeTimer = setTimeout(() => reject(new Error("deadline did not close the active REST socket")), 1000); })]);
  } finally { clearTimeout(closeTimer); }
});

test("parent request cancellation stops batch dispatch and closes the active REST request", { timeout: 5000 }, async t => {
  const h = await slowREST(t); const parent = new AbortController();
  const client = new RESTClient({ baseUrl: h.url, apiKey: "test", timeoutMs: 2000, signal: parent.signal });
  const pending = executeBatch(client, spec, inputs, () => {});
  await h.started; parent.abort();
  const result = await pending; await h.closed;
  assert.deepEqual(h.received, ["fast", "slow"]);
  assert.equal(result.items[0].success, true);
  assert.equal(result.items[1].error!.code, "REQUEST_CANCELLED");
  assert.ok(result.items.slice(2).every(item => item.started === false));
  assert.equal(result.envelope.meta.cancelled, true);
  assert.equal(result.envelope.meta.writes_retried, false);
});

test("already cancelled batch does not start writes", async () => {
  const parent = new AbortController(); parent.abort();
  let calls = 0;
  const client = { timeoutMs: 2000, post: async () => { calls++; return ok(); } } as unknown as RESTClient;
  const result = await executeBatch(client, spec, inputs, () => {}, { signal: parent.signal });
  assert.equal(calls, 0);
  assert.equal(result.envelope.meta.skipped, 5);
  assert.ok(result.items.every(item => item.error!.code === "REQUEST_CANCELLED"));
});

test("batch JSON budget rejects only oversized retained results and preserves later small successes", async () => {
  const big: RESTEnvelope = { ...ok(), data: { amount: precise, padding: "币".repeat(100) } };
  const bigBytes = Buffer.byteLength(JSON.stringify(big), "utf8");
  const smallBytes = Buffer.byteLength(JSON.stringify(ok()), "utf8");
  const held = new Set<number>();
  const client = { timeoutMs: 5000, post: async (_path: string, args: any) => args.address === "tiny" ? ok() : structuredClone(big) } as unknown as RESTClient;
  const result = await executeBatch(client, spec, { addresses: ["first", "second", "overflow", "tiny"], max_concurrent: 1 }, () => {}, {
    responseLimitBytes: bigBytes * 2 + smallBytes,
    processResult: item => { held.add(item.index); }, discardResult: item => { held.delete(item.index); },
  });
  assert.deepEqual(result.items.map(item => item.success), [true, true, false, true]);
  assert.equal((result.items[0].response!.data as any).amount, precise);
  assert.equal(result.items[2].error!.code, "BATCH_RESPONSE_LIMIT_EXCEEDED");
  assert.equal(result.items[2].response, undefined);
  assert.equal(result.items[2].input.address, "overflow");
  assert.equal(result.envelope.meta.response_bytes, bigBytes * 2 + smallBytes);
  assert.equal(result.envelope.meta.response_limited, 1);
  assert.deepEqual([...held], [0, 1, 3]);
});

test("REST counts actual streamed bytes, cancels oversized bodies, and returns a stable size error", { timeout: 5000 }, async t => {
  let sent = 0, close!: () => void;
  const closed = new Promise<void>(resolve => { close = resolve; });
  const server = createServer((_req, res) => {
    res.setHeader("content-type", "application/json");
    res.write('{"success":true,"data":{"padding":"'); // No Content-Length.
    const chunk = Buffer.alloc(256 << 10, 120);
    const timer = setInterval(() => {
      sent += chunk.length; res.write(chunk);
      if (sent >= (20 << 20)) { clearInterval(timer); res.end('"},"meta":{},"error":null}'); }
    }, 5);
    res.once("close", () => { clearInterval(timer); close(); });
  });
  const url = await listen(server, t);
  const client = new RESTClient({ baseUrl: url, apiKey: "test", timeoutMs: 2000 });
  await assert.rejects(client.get("/stream", {}), (error: unknown) => error instanceof Error && error.message === "REST_RESPONSE_TOO_LARGE: REST JSON response exceeds 8388608 bytes");
  await closed;
  assert.ok(sent < (12 << 20), "cancelled body must stop upstream streaming before the full 20 MiB is sent");
});

function file(bytes: Buffer, corrupt = false): RESTEnvelope {
  return { success: true, error: null, meta: {}, data: { content_base64: bytes.toString("base64"), chunk_bytes: bytes.length,
    chunk_sha256: corrupt ? "0".repeat(64) : createHash("sha256").update(bytes).digest("hex"),
    resource_uri: `export://${job}/0/${bytes.length}`, mime_type: "application/octet-stream" } };
}

async function inMemory(t: TestContext, rest: RESTClient) {
  const server = new McpServer({ name: "performance-test", version: "1" }); registerTools(server, rest);
  const client = new Client({ name: "performance-test", version: "1" });
  const [ct, st] = InMemoryTransport.createLinkedPair(); await server.connect(st); await client.connect(ct);
  t.after(async () => { await client.close(); await server.close(); });
  return client;
}

test("SDK cancellation is forwarded to REST and prevents queued batch dispatch", { timeout: 5000 }, async t => {
  const h = await slowREST(t);
  const client = await inMemory(t, new RESTClient({ baseUrl: h.url, apiKey: "test", timeoutMs: 2000 }));
  const controller = new AbortController();
  const pending = client.callTool({ name: "batch_get_transactions", arguments: { chain: "bsc", addresses: ["slow", "queued"], max_concurrent: 1 } }, undefined, { signal: controller.signal });
  const rejection = assert.rejects(pending);
  await h.started; controller.abort(); await rejection; await h.closed;
  assert.deepEqual(h.received, ["slow"]);
});

test("a corrupt file and invalid export metadata retain all other batch successes", async t => {
  const bytes = Buffer.from("precise," + precise);
  const rest = { timeoutMs: 5000, get: async (path: string) => path === "/export/status"
      ? { success: true, error: null, meta: {}, data: { status: "completed", files: [{ name: "bad", url: "ftp://invalid/file" }] } } : ok(),
    post: async (path: string) => file(bytes, path === "/export/download_export") } as unknown as RESTClient;
  const client = await inMemory(t, rest);
  const result = await client.callTool({ name: "batch_call_tools", arguments: { max_concurrent: 1, requests: [
    { tool: "get_address_overview", arguments: { chain: "bsc", address } },
    { tool: "get_export_file", arguments: { job_id: job } },
    { tool: "download_export", arguments: { job_id: job } },
    { tool: "get_export_status", arguments: { job_id: job } },
  ] } });
  assert.equal(result.isError, undefined);
  const data = (result.structuredContent as any).data;
  assert.deepEqual([data.succeeded, data.failed], [2, 2]);
  assert.equal(data.results[0].response.data.amount, precise);
  assert.equal(data.results[2].error.code, "EXPORT_INTEGRITY_FAILED");
  assert.equal(data.results[3].error.code, "EXPORT_RESOURCE_INVALID");
  assert.deepEqual(data.results[2].input, { job_id: job, offset: 0, chunk_size: 1 << 20 });
  assert.ok(data.results.slice(2).every((item: any) => item.response === undefined));
  assert.equal(result.content.filter(item => item.type === "resource").length, 1);
  assert.ok(!JSON.stringify(result.structuredContent).includes("content_base64"));
});

test("batch embedded resources have an 8 MiB aggregate limit with retryable per-item errors", async t => {
  const bytes = Buffer.alloc(4 << 20, 7);
  const rest = { timeoutMs: 5000, get: async () => ok(), post: async () => file(bytes) } as unknown as RESTClient;
  const client = await inMemory(t, rest);
  const requests = Array.from({ length: 3 }, () => ({ tool: "get_export_file", arguments: { job_id: job, chunk_size: 4 << 20 } }));
  const result = await client.callTool({ name: "batch_call_tools", arguments: { requests, max_concurrent: 1 } });
  const envelope = result.structuredContent as any;
  assert.equal(result.isError, undefined);
  assert.deepEqual([envelope.data.succeeded, envelope.data.failed], [2, 1]);
  assert.equal(envelope.meta.embedded_resource_bytes, 8 << 20);
  assert.equal(envelope.data.results[2].error.code, "BATCH_RESOURCE_LIMIT_EXCEEDED");
  assert.equal(envelope.data.results[2].input.chunk_size, 4 << 20);
  assert.equal(envelope.data.results[2].response, undefined);
  assert.equal(result.content.filter(item => item.type === "resource").length, 2);
  assert.ok(!JSON.stringify(result.structuredContent).includes("content_base64"));
});

test("JSON-budget rejection rolls back already extracted resources and their byte count", async t => {
  const bytes = Buffer.alloc(1 << 20, 7);
  const rest = { timeoutMs: 5000, post: async () => {
    const result = file(bytes); (result.data as any).padding = "x".repeat(6 << 20); return result;
  } } as unknown as RESTClient;
  const client = await inMemory(t, rest);
  const result = await client.callTool({ name: "batch_call_tools", arguments: {
    requests: Array.from({ length: 3 }, () => ({ tool: "get_export_file", arguments: { job_id: job } })), max_concurrent: 1,
  } });
  const envelope = result.structuredContent as any;
  assert.deepEqual([envelope.data.succeeded, envelope.data.failed], [2, 1]);
  assert.equal(envelope.data.results[2].error.code, "BATCH_RESPONSE_LIMIT_EXCEEDED");
  assert.equal(envelope.data.results[2].response, undefined);
  assert.equal(envelope.meta.embedded_resource_bytes, 2 << 20);
  assert.ok(envelope.meta.response_bytes < (16 << 20));
  assert.equal(result.content.filter(item => item.type === "resource").length, 2);
});

test("HTTP disconnect cancels upstream work, releases the slot, and completed responses remain valid", { timeout: 5000 }, async t => {
  const h = await slowREST(t);
  const config: Config = { host: "127.0.0.1", allowedHosts: ["127.0.0.1"], port: 0, restBaseUrl: h.url, restApiKey: "test",
    mcpKeyHashes: [sha256("mcp")], oauthAccessCodeHashes: [sha256("mcp")], oauthSigningKey: sha256("signing"),
    publicBaseUrl: "http://127.0.0.1", publicMcpUrl: "http://127.0.0.1/mcp", requestTimeoutMs: 2000, maxConcurrent: 1, rateLimitPerMinute: 100 };
  const url = await listen(createServer(createApp(config)), t);
  const headers = { authorization: "Bearer mcp", "content-type": "application/json", accept: "application/json, text/event-stream", "mcp-protocol-version": "2025-03-26" };
  const parent = new AbortController();
  const pending = fetch(url + "/mcp", { method: "POST", headers, signal: parent.signal,
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "batch_get_transactions", arguments: { chain: "bsc", addresses: ["slow", "queued"], max_concurrent: 1 } } }) }).then(response => response.text());
  // Attach rejection handling before abort to avoid an unhandled promise.
  const rejection = assert.rejects(pending);
  await h.started; parent.abort(); await rejection; await h.closed;
  const success = await fetch(url + "/mcp", { method: "POST", headers,
    body: JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/call", params: { name: "get_address_overview", arguments: { chain: "bsc", address } } }) });
  assert.equal(success.status, 200);
  assert.match(await success.text(), new RegExp(precise.replaceAll(".", "\\.")));
  assert.deepEqual(h.received, ["slow", address]);
});
