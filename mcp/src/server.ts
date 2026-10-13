import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createMcpExpressApp } from "@modelcontextprotocol/sdk/server/express.js";
import type { NextFunction, Request, Response } from "express";
import { pathToFileURL } from "node:url";
import { randomUUID } from "node:crypto";
import { mcpAuthRouter } from "@modelcontextprotocol/sdk/server/auth/router.js";
import { authorized, loadConfig, type Config } from "./config.js";
import { ChainOAuthProvider } from "./oauth.js";
import { RESTClient } from "./rest-client.js";
import { registerTools, toolDefinitions } from "./tools.js";

const instructions = "For current wealth use get_native_balance/get_token_balance/get_all_token_balances, which pin actual state and source. Use aggregate_transfers with full cursor pagination for all counterparties. Read completed exports with get_export_file and resources/read; verify SHA256. For fund lots use trace_funds and compare both FIFO and proportional assumptions. Labels, inferred exchange deposits and control analysis must retain confidence, provenance and coverage. Use batch_call_tools or dedicated batches for multiple addresses; preserve every independent cursor and monetary string.";

export function createServer(config: Config, signal?: AbortSignal): McpServer {
  const server = new McpServer(
    { name: "aurore-blockchain-analysis", version: "1.0.0" },
    { instructions },
  );
  registerTools(server, new RESTClient({
    baseUrl: config.restBaseUrl,
    apiKey: config.restApiKey,
    timeoutMs: config.requestTimeoutMs,
    signal,
  }));
  return server;
}

export function createApp(config: Config) {
  const app = createMcpExpressApp({ host: config.host, allowedHosts: config.allowedHosts });
  const oauth = new ChainOAuthProvider(config);
  const issuer = new URL(config.publicBaseUrl);
  const resource = new URL(config.publicMcpUrl);
  const protectedResourceMetadata = {
    resource: resource.href,
    authorization_servers: [issuer.href],
    scopes_supported: ["mcp:tools"],
    resource_name: "Chain Data Platform",
  };
  app.get("/.well-known/oauth-protected-resource", (_req, res) => res.json(protectedResourceMetadata));
  app.use(mcpAuthRouter({
    provider: oauth,
    issuerUrl: issuer,
    resourceServerUrl: resource,
    scopesSupported: ["mcp:tools"],
    resourceName: "Chain Data Platform",
  }));
  const windows = new Map<string, { minute: number; count: number }>();
  let active = 0;

  app.get("/health", (_req, res) => res.json({ status: "ok", tools: toolDefinitions.length }));
  app.use("/mcp", async (req: Request, res: Response, next: NextFunction) => {
    const started = performance.now();
    const requestId = req.header("x-request-id")?.slice(0, 128) || randomUUID();
    res.setHeader("x-request-id", requestId);
    res.once("finish", () => console.log(JSON.stringify({
      level: "info", event: "mcp_http_request", request_id: requestId,
      method: req.method, status: res.statusCode, duration_ms: Math.round(performance.now() - started),
    })));
    const authHeader = req.header("authorization");
    let bearerAuthorized = authorized(authHeader, config.mcpKeyHashes);
    if (!bearerAuthorized && authHeader?.startsWith("Bearer ")) {
      try {
        const info = await oauth.verifyAccessToken(authHeader.slice(7).trim());
        bearerAuthorized = info.scopes.includes("mcp:tools") && info.resource?.href === config.publicMcpUrl;
      } catch {
        bearerAuthorized = false;
      }
    }
    if (!bearerAuthorized) {
      const metadata = `${config.publicBaseUrl}/.well-known/oauth-protected-resource/mcp`;
      res.setHeader("WWW-Authenticate", `Bearer realm="aurore-mcp", resource_metadata="${metadata}", scope="mcp:tools"`);
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
    let released = false;
    const release = () => { if (!released) { released = true; active -= 1; } };
    res.once("finish", release);
    res.once("close", release);
    next();
  });

  app.post("/mcp", async (req: Request, res: Response) => {
    const controller = new AbortController();
    const server = createServer(config, controller.signal);
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined });
    let closed = false;
    const cleanup = () => {
      if (closed) return;
      closed = true;
      req.removeListener("aborted", cleanup);
      res.removeListener("finish", cleanup);
      res.removeListener("close", cleanup);
      if (!res.writableFinished) controller.abort(new Error("REQUEST_CANCELLED: MCP HTTP client disconnected"));
      // McpServer.close closes its connected transport as well.
      void server.close().catch(() => {});
    };
    // Register before the first await: a disconnect during connect/handleRequest
    // must cancel in-flight REST calls and prevent further batch dispatch.
    req.once("aborted", cleanup);
    res.once("finish", cleanup);
    res.once("close", cleanup);
    if (req.aborted || res.destroyed) { cleanup(); return; }
    try {
      await server.connect(transport);
      if (closed) return;
      await transport.handleRequest(req, res, req.body);
    } catch (error) {
      if (closed) return;
      console.error(JSON.stringify({ level: "error", event: "mcp_request_failed", error: error instanceof Error ? error.message : "unknown" }));
      if (!res.headersSent) res.status(500).json({ jsonrpc: "2.0", error: { code: -32603, message: "Internal server error" }, id: null });
      else if (!res.writableEnded) res.end();
    } finally {
      if (res.destroyed || res.writableFinished) cleanup();
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
