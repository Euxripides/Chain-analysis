import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { RESTClient, type RESTEnvelope } from "./rest-client.js";
import type { ToolDefinition } from "./batch.js";
export declare const investigationTools: ToolDefinition[];
export declare function investigationFileResources(result: RESTEnvelope): CallToolResult["content"];
export declare function registerInvestigationResources(server: McpServer, client: RESTClient): void;
