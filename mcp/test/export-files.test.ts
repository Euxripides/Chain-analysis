import assert from "node:assert/strict";
import { createServer as createHTTPServer } from "node:http";
import { once } from "node:events";
import type { AddressInfo } from "node:net";
import test from "node:test";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { createApp } from "../src/server.js";
import { sha256, type Config } from "../src/config.js";
import { exportFileResources } from "../src/tools.js";
import type { RESTEnvelope } from "../src/rest-client.js";

const jobID = "00000000-0000-4000-8000-000000000000";
const ticketID = "11111111-1111-4111-8111-111111111111";
const url = `https://aurore.online/api/v1/download/${ticketID}`;
const completed = (name = "export-00000000.parquet", mime = "application/vnd.apache.parquet"): RESTEnvelope => ({
  success: true, meta: {}, error: null,
  data: { job_id: jobID, status: "completed", download_url: url, files: [{ name, url, mime_type: mime, rows: 75554, size_bytes: 2840664, sha256: "digest" }] },
});

test("completed exports return a named MCP file resource with its exact size", () => {
  assert.deepEqual(exportFileResources(completed()), [{ type: "resource_link", uri: url, name: "export-00000000.parquet", mimeType: "application/vnd.apache.parquet", size: 2840664, annotations: { audience: ["user", "assistant"] } }]);
  const zip = exportFileResources(completed("export-00000000.zip", "application/zip"));
  assert.equal(zip[0].mimeType, "application/zip");
  assert.equal(zip[0].name, "export-00000000.zip");
});

test("processing, failed and empty statuses do not fabricate a file attachment", () => {
  for (const status of ["processing", "pending", "running", "failed"]) {
    const result = completed();
    (result.data as Record<string, unknown>).status = status;
    assert.deepEqual(exportFileResources(result), []);
  }
  const result = completed();
  (result.data as Record<string, unknown>).files = [];
  assert.deepEqual(exportFileResources(result), []);
});

test("MCP export resources reject internal or non-ticket download URLs", () => {
  for (const bad of ["https://127.0.0.1:8080/api/v1/download/ticket", "https://[::1]/api/v1/download/ticket", "https://localhost/api/v1/download/ticket", "http://aurore.online/api/v1/download/ticket", "https://secret@aurore.online/api/v1/download/ticket", "https://aurore.online/admin/"]) {
    const result = completed();
    ((result.data as Record<string, unknown>).files as Record<string, unknown>[])[0].url = bad;
    assert.throws(() => exportFileResources(result), /download URL/);
  }
});

test("live MCP protocol returns the export file resource alongside structured status", async () => {
  const rest = createHTTPServer((req, res) => {
    assert.equal(req.url, `/api/v1/export/status?job_id=${jobID}`);
    assert.equal(req.headers.authorization, "Bearer test-rest-secret");
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(completed()));
  });
  rest.listen(0, "127.0.0.1");
  await once(rest, "listening");
  const restPort = (rest.address() as AddressInfo).port;
  const config: Config = {
    host: "127.0.0.1", allowedHosts: ["127.0.0.1"], port: 0,
    restBaseUrl: `http://127.0.0.1:${restPort}/api/v1`, restApiKey: "test-rest-secret",
    mcpKeyHashes: [sha256("test-mcp-secret")], oauthAccessCodeHashes: [sha256("test-mcp-secret")],
    oauthSigningKey: sha256("test-signing-key"), publicBaseUrl: "https://aurore.online", publicMcpUrl: "https://aurore.online/mcp",
    requestTimeoutMs: 5000, maxConcurrent: 4, rateLimitPerMinute: 100,
  };
  const mcp = createHTTPServer(createApp(config));
  mcp.listen(0, "127.0.0.1");
  await once(mcp, "listening");
  const client = new Client({ name: "export-file-regression", version: "1.0.0" });
  try {
    await client.connect(new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${(mcp.address() as AddressInfo).port}/mcp`), { requestInit: { headers: { authorization: "Bearer test-mcp-secret" } } }));
    const result = await client.callTool({ name: "get_export_status", arguments: { job_id: jobID } });
    assert.equal(result.isError, undefined);
    assert.deepEqual(result.structuredContent, completed());
    assert.equal(result.content.length, 2);
    assert.equal(result.content[0].type, "text");
    assert.deepEqual(result.content[1], exportFileResources(completed())[0]);
  } finally {
    await client.close();
    await Promise.all([new Promise<void>((resolve) => rest.close(() => resolve())), new Promise<void>((resolve) => mcp.close(() => resolve()))]);
  }
});
