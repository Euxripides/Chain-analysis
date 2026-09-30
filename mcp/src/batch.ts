import { z } from "zod";
import { RESTClient, type RESTEnvelope } from "./rest-client.js";

export type ToolDefinition = {
  name: string;
  title: string;
  description: string;
  path: string;
  method?: "GET" | "POST";
  readOnly?: boolean;
  inputSchema: Record<string, z.ZodTypeAny> | z.ZodTypeAny;
  summarize: (result: RESTEnvelope) => string;
  batch?: BatchSpec;
};

type BatchSpec =
  | { kind: "addresses"; operation: ToolDefinition; key: "address" | "source" | "tx_hash" }
  | { kind: "calls"; operations: Map<string, ToolDefinition> };

type PreparedCall = { definition: ToolDefinition; input: Record<string, unknown>; address?: string };
export type BatchItem = {
  index: number;
  tool: string;
  address?: string;
  input: Record<string, unknown>;
  success: boolean;
  response?: RESTEnvelope;
  error?: { code: string; message: string };
};

export function operationSchema(definition: ToolDefinition): z.ZodTypeAny {
  return definition.inputSchema instanceof z.ZodType
    ? definition.inputSchema
    : z.object(definition.inputSchema).strict();
}

const pageFields = ["page", "page_size", "cursor", "detail_cursor", "detail_page_size", "before"];
const addressOperations = [
  "get_token_transfers", "get_transactions", "get_internal_transactions", "get_counterparties",
  "get_flow_summary", "get_top_flows", "get_first_funding", "get_solana_address_signatures",
  "get_export_address_summary", "find_fund_paths", "get_all_token_balances", "get_contract_control", "get_labels", "eth_getTransactionReceipt", "eth_getTransactionByHash",
];

export function createBatchTools(base: ToolDefinition[]): ToolDefinition[] {
  const tools: ToolDefinition[] = [];
  for (const name of addressOperations) {
    const operation = base.find(item => item.name === name);
    if (!operation) throw new Error(`Missing batch operation ${name}`);
    const isHash = name === "eth_getTransactionReceipt" || name === "eth_getTransactionByHash";
    const key = isHash ? "tx_hash" : name === "find_fund_paths" ? "source" : "address";
    const shape = { ...(operation.inputSchema as Record<string, z.ZodTypeAny>) };
    delete shape[key];
    if (shape.page_size) shape.page_size = z.number().int().min(1).max(500).default(25);
    if (shape.detail_page_size) shape.detail_page_size = z.number().int().min(1).max(500).default(25);
    const overrides: Record<string, z.ZodTypeAny> = { address: z.string().trim().min(1) };
    for (const field of pageFields) if (shape[field]) overrides[field] = shape[field].optional();
    if (Object.keys(overrides).length > 1) {
      shape.pagination = z.array(z.object(overrides).strict()).max(20).optional().describe("Optional per-address page/cursor overrides. Use each result's pagination independently with the same filters.");
    }
    const summary = name === "get_export_address_summary";
    shape[isHash ? "tx_hashes" : "addresses"] = z.array(z.string().trim().min(1)).min(1).max(20).describe("1-20 complete addresses. Each result retains its own evidence, totals, errors and pagination; preserve Solana/TRON case.");
    shape.max_concurrent = z.number().int().min(1).max(summary ? 2 : 8).default(summary ? 2 : 4);
    tools.push({
      name: name === "get_all_token_balances" ? "batch_get_balances" : name === "eth_getTransactionReceipt" ? "batch_get_receipts" : name === "eth_getTransactionByHash" ? "batch_get_transaction_details" : `batch_${name}`,
      title: `Batch: ${operation.title}`,
      description: `Run ${name} for 1-20 addresses in one MCP call with bounded concurrency. Returns an independent full REST response for every address, preserving monetary strings and each address's pagination and evidence metadata. A failure does not discard successful results; retry only failed entries. ${operation.description}`,
      path: operation.path,
      method: operation.method,
      readOnly: operation.readOnly,
      inputSchema: shape,
      summarize: () => "Per-address batch results retrieved.",
      batch: { kind: "addresses", operation, key },
    });
  }
  // Existing optimized batch tools remain available directly. Nested batches
  // are excluded so one request can never multiply into unbounded fan-out.
  const singles = base.filter(item => !item.name.startsWith("batch_") && !item.name.endsWith("_batch"));
  const choices = singles.map(operation => z.object({
    tool: z.literal(operation.name), arguments: operationSchema(operation),
  }).strict());
  tools.push({
    name: "batch_call_tools",
    title: "Call multiple operations in one batch",
    description: "Run 1-20 existing single operations in one MCP call: address queries, transactions, analysis, exports, source-chain reads, data ensure and retries. Arguments are validated using each original tool schema before any operation starts. Results remain independent and in request order, with partial failures preserved. Monetary strings and per-item cursors are unchanged. Can create exports or download jobs; inspect each result and retry only failed entries. Nested batches are not allowed. For multiple addresses using identical filters, prefer the dedicated batch address tools; create_large_export also accepts up to 100 addresses in one combined export.",
    path: "",
    readOnly: false,
    inputSchema: {
      requests: z.array(z.discriminatedUnion("tool", choices as any)).min(1).max(20),
      max_concurrent: z.number().int().min(1).max(8).default(4),
    },
    summarize: () => "Independent operation results retrieved.",
    batch: { kind: "calls", operations: new Map(singles.map(item => [item.name, item])) },
  });
  return tools;
}

function addressKey(chain: unknown, address: string): string {
  const value = address.trim();
  if (typeof chain !== "string" || chain === "solana" || chain === "tron") return value;
  return value.toLowerCase();
}

function prepare(spec: BatchSpec, input: Record<string, unknown>): PreparedCall[] {
  if (spec.kind === "calls") {
    return (input.requests as Array<{ tool: string; arguments: Record<string, unknown> }>).map(item => {
      const definition = spec.operations.get(item.tool);
      if (!definition) throw new Error("Unsupported or nested batch operation");
      return { definition, input: operationSchema(definition).parse(item.arguments) };
    });
  }
  const { addresses, tx_hashes, pagination, max_concurrent: _limit, ...common } = input;
  const unique: string[] = [];
  const seen = new Set<string>();
  for (const address of (addresses ?? tx_hashes) as string[]) {
    const key = addressKey(common.chain, address);
    if (!seen.has(key)) { seen.add(key); unique.push(address.trim()); }
  }
  const overrides = new Map<string, Record<string, unknown>>();
  for (const page of (pagination ?? []) as Array<{ address: string } & Record<string, unknown>>) {
    const { address, ...values } = page;
    const key = addressKey(common.chain, address);
    if (!seen.has(key) || overrides.has(key)) throw new Error("Pagination must name a batch address exactly once");
    overrides.set(key, values);
  }
  return unique.map(address => ({
    definition: spec.operation,
    address,
    input: operationSchema(spec.operation).parse({ ...common, ...overrides.get(addressKey(common.chain, address)), [spec.key]: address }),
  }));
}

export async function executeBatch(
  client: RESTClient,
  spec: BatchSpec,
  input: Record<string, unknown>,
  validate: (name: string, input: Record<string, unknown>) => void,
): Promise<{ envelope: RESTEnvelope; items: BatchItem[] }> {
  const calls = prepare(spec, input);
  // Complete semantic validation first: no earlier write may run if a later
  // item has an invalid token selection, export pair or address input form.
  for (const call of calls) validate(call.definition.name, call.input);
  const heavy = calls.some(call => ["get_export_address_summary", "get_export_file", "download_export", "trace_funds", "get_balance_history"].includes(call.definition.name));
  const limit = Math.min(Number(input.max_concurrent) || 4, calls.length, heavy ? 2 : 8);
  const items: BatchItem[] = new Array(calls.length);
  let cursor = 0;
  await Promise.all(Array.from({ length: limit }, async () => {
    for (;;) {
      const index = cursor++;
      if (index >= calls.length) return;
      const call = calls[index];
      const item: BatchItem = { index, tool: call.definition.name, ...(call.address ? { address: call.address } : {}), input: call.input, success: false };
      try {
        item.response = call.definition.method === "POST"
          ? await client.post(call.definition.path, call.input)
          : await client.get(call.definition.path, call.input);
        item.success = true;
      } catch (error) {
        const message = error instanceof Error ? error.message : "Unknown operation failure";
        const match = /^([A-Z][A-Z0-9_]+):\s*(.*)$/s.exec(message);
        item.error = { code: match?.[1] ?? "REST_ERROR", message: match?.[2] ?? message };
      }
      items[index] = item;
    }
  }));
  const succeeded = items.filter(item => item.success).length;
  const failed = items.length - succeeded;
  return {
    items,
    envelope: {
      success: failed === 0,
      data: { results: items, requested: items.length, succeeded, failed },
      meta: { requested: items.length, max_concurrent: limit, result_order: "request_order", independent_results: true },
      error: failed ? { code: "BATCH_PARTIAL_FAILURE", message: `${failed} of ${items.length} operations failed; successful results are retained` } : null,
    },
  };
}
