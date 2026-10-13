import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { type Config } from "./config.js";
export declare function createServer(config: Config, signal?: AbortSignal): McpServer;
export declare function createApp(config: Config): import("express").Express;
