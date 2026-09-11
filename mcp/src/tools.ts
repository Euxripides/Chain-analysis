import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { z } from "zod";
import { RESTClient, type RESTEnvelope } from "./rest-client.js";

export const CHAIN_VALUES = ["bsc", "ethereum", "tron", "polygon", "arbitrum", "optimism", "base"] as const;
const chain = z.enum(CHAIN_VALUES).describe("Blockchain network. Use exactly one supported lowercase value.");
const address = z.string().min(1).describe("Complete EVM 0x address or complete TRON Base58 address. Never abbreviate it.");
const txHash = z.string().min(1).describe("Complete transaction hash without abbreviation.");
const optionalTime = z.string().datetime({ offset: true }).optional().describe("ISO 8601 timestamp, for example 2026-09-10T01:30:00Z.");
const direction = z.enum(["all", "in", "out"]).default("all").describe("Transfer direction relative to the investigated address.");
const page = z.number().int().min(1).default(1);
const pageSize = z.number().int().min(1).max(500).default(100).describe("Maximum 500. Use large export instead of requesting huge result sets.");
const sort = z.enum(["asc", "desc"]).default("desc");
const tokenFilters = {
  token: z.string().optional().describe("Token symbol such as USDT or USDC."),
  token_contract: z.string().optional().describe("Complete token contract address."),
};
const timeFilters = { start_time: optionalTime, end_time: optionalTime };
const listFilters = {
  chain,
  address,
  direction,
  counterparty: z.string().optional().describe("Complete counterparty address."),
  ...timeFilters,
  page,
  page_size: pageSize,
  sort,
};
const pathBase = { chain, source: address, max_depth: z.number().int().min(1).max(5).default(3), min_amount: z.string().optional(), max_nodes: z.number().int().min(1).max(500).default(200), ...timeFilters, exclude_addresses: z.array(address).max(200).optional() };
const pathInputSchema = { ...pathBase, token: z.string().min(1).optional().describe("Token symbol such as USDT; optional when token_contract is provided."), token_contract: z.string().min(1).optional().describe("Complete token contract address; optional when token is provided.") };
const networkBase = { chain, seed_addresses: z.array(address).min(1).max(200), max_depth: z.number().int().min(1).max(5).default(3), min_total_amount: z.string().optional(), max_nodes: z.number().int().min(1).max(500).default(200), ...timeFilters };
const networkInputSchema = { ...networkBase, token: z.string().min(1).optional().describe("Token symbol such as USDT; optional when token_contract is provided."), token_contract: z.string().min(1).optional().describe("Complete token contract address; optional when token is provided.") };
const exportInputSchema = { chain, address, data_types: z.array(z.enum(["transactions", "token_transfers", "internal_transactions", "logs"])).min(1), ...tokenFilters, ...timeFilters, format: z.enum(["parquet", "csv"]), compression: z.enum(["zstd", "zip"]) };

type ToolDefinition = {
  name: string;
  title: string;
  description: string;
  path: string;
  method?: "GET" | "POST";
  readOnly?: boolean;
  inputSchema: Record<string, z.ZodTypeAny> | z.ZodTypeAny;
  summarize: (result: RESTEnvelope) => string;
};

export const toolDefinitions: ToolDefinition[] = [
  {
    name: "get_address_overview",
    title: "Get address overview",
    description: "Use this first when the user asks to investigate or analyze a blockchain address. Returns transaction counts, activity time range, address type, native balance availability and high-level statistics. Do not use it alone to claim current token holdings.",
    path: "/address/overview",
    inputSchema: { chain, address },
    summarize: () => "Address overview retrieved from the platform database.",
  },
  {
    name: "get_address_assets",
    title: "Get address assets",
    description: "Use this when investigating current fund retention or large token holdings. Inspect balance_source and is_current_balance: observed_net_flow is historical net flow, not a verified current on-chain balance. Never present unavailable USD valuation as zero.",
    path: "/address/assets",
    inputSchema: { chain, address },
    summarize: () => "Address asset evidence retrieved; verify each balance_source before describing current holdings.",
  },
  {
    name: "get_token_transfers",
    title: "List token transfers",
    description: "Use this for filtered, paginated ERC-20/BEP-20/TRC-20 transfer evidence. Prefer counterparties, flow summary or top flows for aggregate questions. Never page through a massive history; use export outside P0 when needed.",
    path: "/address/token-transfers",
    inputSchema: { ...listFilters, ...tokenFilters, min_amount: z.string().optional(), max_amount: z.string().optional(), status: z.enum(["success", "failed"]).optional() },
    summarize: (r) => `Token transfers retrieved (${String(r.meta?.page_size ?? "bounded page")}).`,
  },
  {
    name: "get_transactions",
    title: "List native transactions",
    description: "Use this for filtered, paginated top-level blockchain transactions, native value, status and contract method evidence. Do not use it as a substitute for token transfers or internal calls.",
    path: "/address/transactions",
    inputSchema: { ...listFilters, min_value: z.string().optional(), max_value: z.string().optional(), method_id: z.string().optional(), status: z.enum(["success", "failed"]).optional() },
    summarize: () => "Top-level transactions retrieved as a bounded page.",
  },
  {
    name: "get_internal_transactions",
    title: "List internal transactions",
    description: "Use this when the user needs contract-internal native value movements or call traces associated with an address. Results are paginated and evidence-bound.",
    path: "/address/internal-transactions",
    inputSchema: { ...listFilters, min_value: z.string().optional(), max_value: z.string().optional() },
    summarize: () => "Internal transactions retrieved as a bounded page.",
  },
  {
    name: "get_counterparties",
    title: "Analyze address counterparties",
    description: "Use this for funding sources, fund destinations, aggregation addresses, high-frequency counterparties, major inflows or outflows, and possible fund sinks. Prefer this over fetching large raw transaction datasets.",
    path: "/address/counterparties",
    inputSchema: {
      chain, address, ...tokenFilters, direction, ...timeFilters,
      min_total_amount: z.string().optional(), min_tx_count: z.number().int().min(1).optional(),
      sort_by: z.enum(["tx_count", "total_amount", "net_flow", "last_time"]).default("total_amount"),
      limit: z.number().int().min(1).max(200).default(50),
    },
    summarize: (r) => `Counterparty aggregation retrieved (${String(r.meta?.count ?? "bounded")} results).`,
  },
  {
    name: "get_flow_summary",
    title: "Get fund-flow summary",
    description: "Use this to compare cumulative inflow, outflow, net flow, counts, unique counterparties and activity times for one token. A positive historical net flow does not by itself prove current retention.",
    path: "/address/flow-summary",
    inputSchema: { chain, address, ...tokenFilters, ...timeFilters },
    summarize: () => "Fund-flow summary retrieved from database-side aggregation.",
  },
  {
    name: "get_top_flows",
    title: "Get largest fund flows",
    description: "Use this for the largest funding sources, largest destinations, largest transfers, or main fund exits. Use group_by=address for counterparties and group_by=transaction for individual transfers.",
    path: "/address/top-flows",
    inputSchema: {
      chain, address, direction, ...tokenFilters, ...timeFilters,
      group_by: z.enum(["address", "transaction"]).default("address"),
      limit: z.number().int().min(1).max(200).default(20),
    },
    summarize: () => "Largest fund flows retrieved using database-side ordering and limits.",
  },
  {
    name: "get_first_funding",
    title: "Get first funding source",
    description: "Use this to identify the earliest observed native funding, first gas source, or earliest token funding for an address. Treat it as earliest available platform evidence, subject to dataset coverage.",
    path: "/address/first-funding",
    inputSchema: { chain, address },
    summarize: () => "Earliest available funding evidence retrieved.",
  },
  {
    name: "get_transaction_detail",
    title: "Get transaction detail",
    description: "Use this when the user provides a transaction hash or needs one transaction's status, method, decoded input, token transfers, internal transactions and logs. Do not infer a recurring distribution rule from one transaction.",
    path: "/transaction/detail",
    inputSchema: { chain, tx_hash: txHash },
    summarize: () => "Transaction detail and available decoded evidence retrieved.",
  },
  {
    name: "batch_get_address_overview",
    title: "Batch address overview",
    description: "Use after counterparty ranking to compare up to 200 complete addresses without repeated single-address calls.",
    path: "/addresses/batch-overview", method: "POST",
    inputSchema: { chain, addresses: z.array(address).min(1).max(200) },
    summarize: () => "Batch address overviews retrieved.",
  },
  {
    name: "batch_get_address_assets",
    title: "Batch address assets",
    description: "Screen assets for up to 200 addresses. Inspect balance_source, is_current_balance and freshness before claiming current retention.",
    path: "/addresses/batch-assets", method: "POST",
    inputSchema: { chain, addresses: z.array(address).min(1).max(200), ...tokenFilters, min_balance: z.string().optional() },
    summarize: () => "Batch address asset evidence retrieved.",
  },
  {
    name: "get_transaction_trace",
    title: "Get transaction trace",
    description: "Use for the internal contract-call hierarchy of one transaction. A trace is evidence for that transaction only.",
    path: "/transaction/trace", inputSchema: { chain, tx_hash: txHash },
    summarize: () => "Transaction trace retrieved.",
  },
  {
    name: "get_transaction_logs",
    title: "Get transaction logs",
    description: "Use for event-log evidence emitted by one transaction. Pair with decode and trace for contract behavior analysis.",
    path: "/transaction/logs", inputSchema: { chain, tx_hash: txHash },
    summarize: () => "Transaction event logs retrieved.",
  },
  {
    name: "decode_transaction",
    title: "Decode transaction input",
    description: "Resolve the method selector and decoded input for one transaction through cached or bounded provider evidence.",
    path: "/transaction/decode", inputSchema: { chain, tx_hash: txHash },
    summarize: () => "Transaction input decoding retrieved.",
  },
  {
    name: "find_fund_paths",
    title: "Find bounded fund paths",
    description: "Use for multi-hop fund destinations. Provide either token or token_contract so amounts from different assets are never combined. Returns a context-safe graph capped at depth 5 and 500 nodes; use export for larger evidence sets.",
    path: "/analysis/path", method: "POST",
    inputSchema: pathInputSchema,
    summarize: () => "Bounded multi-hop fund paths retrieved.",
  },
  {
    name: "build_address_network",
    title: "Build bounded address network",
    description: "Build a time- and single-token-bounded network from seed addresses. Provide either token or token_contract. Capped at depth 5 and 500 nodes for AI context safety.",
    path: "/analysis/address-network", method: "POST",
    inputSchema: networkInputSchema,
    summarize: () => "Bounded address network retrieved.",
  },
  {
    name: "create_large_export",
    title: "Create large evidence export",
    description: "Create an asynchronous export for data too large for chat. Prefer parquet+zstd for analysis or csv+zip for compatibility; returns a job_id immediately.",
    path: "/export/create", method: "POST", readOnly: false,
    inputSchema: exportInputSchema,
    summarize: () => "Asynchronous export created; poll get_export_status with the returned job_id.",
  },
  {
    name: "get_export_status",
    title: "Get export status",
    description: "Poll an asynchronous export. A download URL is an artifact location, not evidence that the AI analyzed the file.",
    path: "/export/status", inputSchema: { job_id: z.string().uuid() },
    summarize: () => "Export status retrieved.",
  },
  {
    name: "ensure_chain_data",
    title: "Ensure chain data through the system downloader",
    description: "Use before analysis when Coverage is incomplete. Reuses the platform's optimized Coverage→Planner→Worker download pipeline and downloads only missing ranges. Created jobs are isolated with source=chatgpt_mcp and ai_managed=true, distinct from user_download jobs. Returns immediately with a download_job_id when acquisition is needed.",
    path: "/data/ensure", method: "POST", readOnly: false,
    inputSchema: {
      chain,
      addresses: z.array(address).min(1).max(200),
      datasets: z.array(z.enum(["transactions", "token_transfers", "internal_transactions", "logs"])).min(1),
      range: z.object({
        type: z.enum(["full", "block", "time"]),
        from_block: z.number().int().min(0).optional(), to_block: z.number().int().min(0).optional(),
        from: optionalTime, to: optionalTime,
      }),
      performance_mode: z.enum(["safe", "fast", "turbo"]).default("fast"),
    },
    summarize: () => "Coverage checked through the system downloader; inspect status and download_job_id before querying evidence.",
  },
  {
    name: "get_ai_download_status",
    title: "Get AI download task status",
    description: "Poll only download jobs created by ChatGPT/MCP. It cannot read ordinary user_download jobs. Wait for completed/ready before treating newly acquired coverage as available.",
    path: "/data/download-status",
    inputSchema: { download_job_id: z.string().uuid() },
    summarize: () => "AI-managed system download status retrieved.",
  },
];

export function registerTools(server: McpServer, client: RESTClient): void {
  for (const definition of toolDefinitions) {
    server.registerTool(
      definition.name,
      {
        title: definition.title,
        description: definition.description,
        inputSchema: definition.inputSchema as any,
        annotations: { readOnlyHint: definition.readOnly !== false, destructiveHint: false, openWorldHint: false },
      },
      async (args: unknown) => {
        try {
          const input = args as Record<string, unknown>;
          if ((definition.name === "find_fund_paths" || definition.name === "build_address_network") && !input.token && !input.token_contract) {
            throw new Error("token or token_contract is required; cross-asset amounts cannot be aggregated");
          }
          if (definition.name === "create_large_export") {
            const valid = (input.format === "parquet" && input.compression === "zstd") || (input.format === "csv" && input.compression === "zip");
            if (!valid) throw new Error("Supported export pairs are parquet+zstd and csv+zip");
          }
          const result = definition.method === "POST" ? await client.post(definition.path, input) : await client.get(definition.path, input);
          return {
            structuredContent: result,
            content: [{ type: "text" as const, text: definition.summarize(result) }],
          };
        } catch (error) {
          const message = error instanceof Error ? error.message : "Unknown MCP tool error";
          return { isError: true, content: [{ type: "text" as const, text: message }] };
        }
      },
    );
  }
}
