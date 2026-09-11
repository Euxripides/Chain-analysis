import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createMcpExpressApp } from "@modelcontextprotocol/sdk/server/express.js";
import type { NextFunction, Request, Response } from "express";
import { pathToFileURL } from "node:url";
import { randomUUID } from "node:crypto";
import { authorized, loadConfig, type Config } from "./config.js";
import { RESTClient } from "./rest-client.js";
import { registerTools, toolDefinitions } from "./tools.js";

const instructions = "For a new address, call get_address_overview first, then get_flow_summary and get_counterparties. Use get_top_flows for largest sources or destinations. Historical net flow is not a verified current balance: inspect balance_source and is_current_balance from get_address_assets. Prefer aggregates over raw pages and never request more than 500 rows.";

export function createServer(config: Config): McpServer {
  const server = new McpServer(
    { name: "aurore-blockchain-analysis", version: "1.0.0" },
    { instructions },
  );
  registerTools(server, new RESTClient({
    baseUrl: config.restBaseUrl,
    apiKey: config.restApiKey,
    timeoutMs: config.requestTimeoutMs,
  }));
  return server;
}

export function createApp(config: Config) {
  const app = createMcpExpressApp({ host: config.host, allowedHosts: config.allowedHosts });
  const windows = new Map<string, { minute: number; count: number }>();
  let active = 0;

  app.get("/health", (_req, res) => res.json({ status: "ok", tools: toolDefinitions.length }));
  app.use("/mcp", (req: Request, res: Response, next: NextFunction) => {
    const started = performance.now();
    const requestId = req.header("x-request-id")?.slice(0, 128) || randomUUID();
    res.setHeader("x-request-id", requestId);
    res.once("finish", () => console.log(JSON.stringify({
      level: "info", event: "mcp_http_request", request_id: requestId,
      method: req.method, status: res.statusCode, duration_ms: Math.round(performance.now() - started),
    })));
    if (!authorized(req.header("authorization"), config.mcpKeyHashes)) {
      res.setHeader("WWW-Authenticate", 'Bearer realm="aurore-mcp"');
      res.status(401).json({ error: "unauthorized" });
      return;
    }
    const client = req.ip || "unknown";
    const minute = Math.floor(Date.now() / 60_000);
    const window = windows.get(client);
    const current = window?.minute === minute ? window : { minute, count: 0 };
    current.count += 1;
    windows.set(client, current);
    if (current.count > config.rateLimitPerMinute) {
      res.status(429).json({ error: "rate_limited" });
      return;
    }
    if (active >= config.maxConcurrent) {
      res.status(503).json({ error: "concurrency_limit_reached" });
      return;
    }
    active += 1;
    res.once("finish", () => { active -= 1; });
    res.once("close", () => { if (!res.writableFinished) active -= 1; });
    next();
  });

  app.post("/mcp", async (req: Request, res: Response) => {
    const server = createServer(config);
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
    try {
      await server.connect(transport);
      await transport.handleRequest(req, res, req.body);
    } catch (error) {
      console.error(JSON.stringify({ level: "error", event: "mcp_request_failed", error: error instanceof Error ? error.message : "unknown" }));
      if (!res.headersSent) res.status(500).json({ jsonrpc: "2.0", error: { code: -32603, message: "Internal server error" }, id: null });
    } finally {
      res.once("close", () => { void transport.close(); void server.close(); });
    }
  });
  app.get("/mcp", (_req, res) => res.status(405).json({ jsonrpc: "2.0", error: { code: -32000, message: "Method not allowed" }, id: null }));
  app.delete("/mcp", (_req, res) => res.status(405).json({ jsonrpc: "2.0", error: { code: -32000, message: "Method not allowed" }, id: null }));
  return app;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const config = loadConfig();
  const app = createApp(config);
  app.listen(config.port, config.host, (error?: Error) => {
    if (error) throw error;
    console.log(JSON.stringify({ level: "info", event: "mcp_listening", host: config.host, port: config.port, tools: toolDefinitions.length }));
  });
}
