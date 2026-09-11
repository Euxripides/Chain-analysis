import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const url = process.env.MCP_URL;
const apiKey = process.env.MCP_API_KEY;
if (!url || !apiKey) throw new Error("MCP_URL and MCP_API_KEY are required");

const client = new Client({ name: "blockchain-platform-production-smoke", version: "1.0.0" });
const transport = new StreamableHTTPClientTransport(new URL(url), {
  requestInit: { headers: { authorization: `Bearer ${apiKey}` } },
});

await client.connect(transport);
const listed = await client.listTools();
const result = await client.callTool({
  name: "get_flow_summary",
  arguments: {
    chain: "tron",
    address: "T9ygmX26tszHxPiHvbPwjDb92UkfC4YXPU",
    token: "USDT",
  },
});
console.log(JSON.stringify({
  protocol: "initialized",
  tool_count: listed.tools.length,
  tool_names: listed.tools.map((tool) => tool.name),
  read_only_count: listed.tools.filter((tool) => tool.annotations?.readOnlyHint).length,
  call_is_error: result.isError ?? false,
  result: result.structuredContent,
}, null, 2));
await client.close();
