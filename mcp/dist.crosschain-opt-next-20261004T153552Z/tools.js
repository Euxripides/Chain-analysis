import { z } from "zod";
import { crossChainTools, validateCrossChainTool } from "./crosschain.js";
import { investigationTools, investigationFileResources, registerInvestigationResources } from "./investigation.js";
import { nextInvestigationTools, validateNextTool } from "./next-investigation.js";
import { createBatchTools, executeBatch } from "./batch.js";
export const CHAIN_VALUES = ["bsc", "ethereum", "eth", "tron", "polygon", "arbitrum", "optimism", "base", "avalanche", "solana", "fantom", "flare", "gnosis", "linea", "scroll", "somnia_mainnet", "story_mainnet", "taiko", "telos", "xlayer"];
export const DOWNLOAD_CHAIN_VALUES = CHAIN_VALUES;
const chain = z.enum(CHAIN_VALUES).describe("Blockchain network, including Solana. Use a lowercase chain value; preserve the case of Solana addresses and signatures.");
const downloadChain = z.enum(DOWNLOAD_CHAIN_VALUES).describe("Download-capable blockchain network, including Solana through dRPC JSON-RPC history.");
const address = z.string().trim().min(1).describe("Complete EVM 0x, TRON Base58Check, or case-sensitive Solana Base58 address. Never abbreviate it.");
const downloadAddress = z.string().min(1).describe("Complete EVM 0x, TRON Base58Check, or Solana base58 address. Never abbreviate it.");
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
const exportInputSchema = { chain, address: address.optional().describe("One address, or use addresses for a combined export; supply exactly one form."), addresses: z.array(address).min(1).max(100).optional().describe("1-100 addresses in one combined export; duplicates are normalized and removed by the server."), data_types: z.array(z.enum(["transactions", "token_transfers", "internal_transactions", "logs"])).min(1), ...tokenFilters, ...timeFilters, format: z.enum(["parquet", "csv"]), compression: z.enum(["zstd", "zip"]) };
const ensureRange = z.object({
    type: z.enum(["full", "block", "time"]),
    from_block: z.number().int().min(0).optional(), to_block: z.number().int().min(0).optional(),
    from: optionalTime, to: optionalTime,
});
const ensureRequest = z.object({
    chain: downloadChain,
    addresses: z.array(downloadAddress).min(1).max(200),
    datasets: z.array(z.enum(["transactions", "token_transfers", "internal_transactions", "logs", "balances"])).min(1),
    range: ensureRange,
    performance_mode: z.enum(["safe", "fast", "turbo"]).default("fast"),
});
export const toolDefinitions = [
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
        description: "Use this for filtered, paginated ERC-20/BEP-20/TRC-20 or Solana SPL transfer evidence. Solana native SOL movements are in internal transactions; use token=SOL for native flow aggregations. Never page through a massive history.",
        path: "/address/token-transfers",
        inputSchema: { ...listFilters, ...tokenFilters, min_amount: z.string().optional(), max_amount: z.string().optional(), status: z.enum(["success", "failed"]).optional() },
        summarize: (r) => `Token transfers retrieved (${String(r.meta?.page_size ?? "bounded page")}).`,
    },
    {
        name: "get_transactions",
        title: "List native transactions",
        description: "Use this for filtered, paginated top-level blockchain transactions. On Solana it lists address signatures; direction and a unique counterparty are not intrinsic to a Solana transaction. Use native/SPL movement rows for funding attribution.",
        path: "/address/transactions",
        inputSchema: { ...listFilters, min_value: z.string().optional(), max_value: z.string().optional(), method_id: z.string().optional(), status: z.enum(["success", "failed"]).optional() },
        summarize: () => "Top-level transactions retrieved as a bounded page.",
    },
    {
        name: "get_internal_transactions",
        title: "List internal transactions",
        description: "Use this for internal calls and native movements. On Solana, asset_type=SOL with call_type=system_transfer/system_create_account has exact source, destination and lamports; PROGRAM rows are CPI call nodes, not fund flows.",
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
        description: "Compare cumulative inflow, outflow, net flow, counts and counterparties for one asset. On Solana, token=SOL selects executed native System Program movements; an SPL mint uses token_contract. Historical net flow is not a current balance.",
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
        description: "Find first observed native and token funding. Solana native funding uses executed System Program instructions with exact source/destination and full signature; check meta.coverage before claiming complete history.",
        path: "/address/first-funding",
        inputSchema: { chain, address },
        summarize: () => "Earliest available funding evidence retrieved.",
    },
    {
        name: "get_transaction_detail",
        title: "Get transaction detail",
        description: "Read combined local transaction, transfer, internal movement and log evidence. Solana accepts a case-sensitive Base58 signature and returns native JSON-parsed transaction data rather than EVM ABI fields.",
        path: "/transaction/detail",
        inputSchema: { chain, tx_hash: txHash },
        summarize: () => "Transaction detail and available decoded evidence retrieved.",
    },
    {
        name: "get_solana_address_signatures",
        title: "List Solana address signatures",
        description: "Read a bounded page of finalized Solana signatures through dRPC. An optional dRPC archive endpoint may supply missing older records. A signature list is not full-history coverage; inspect meta.source and next_before.",
        path: "/solana/address/signatures",
        inputSchema: {
            address: z.string().min(32).max(44).describe("Complete case-sensitive Solana Base58 address."),
            before: z.string().min(64).max(88).optional().describe("Full signature cursor returned as next_before."),
            limit: z.number().int().min(1).max(100).default(20),
        },
        summarize: () => "Bounded Solana signature page retrieved; full historical coverage is not asserted.",
    },
    {
        name: "get_solana_transaction",
        title: "Read Solana transaction",
        description: "Read one finalized Solana transaction by complete Base58 signature through dRPC, including parsed message, instructions, inner instructions, token balances and logs when available. Inspect meta.source. A missing RPC result is not proof the transaction never existed.",
        path: "/solana/transaction",
        inputSchema: { signature: z.string().min(64).max(88).describe("Complete case-sensitive Solana transaction signature.") },
        summarize: () => "Solana source-chain transaction record retrieved from RPC.",
    },
    {
        name: "get_bridge_source_transaction",
        title: "Read bridge order source transaction",
        description: "Given a bridge order's known source chain and complete source transaction hash/signature, read that source-chain record. The order ID is only a correlation label: this tool does not independently prove that the transaction belongs to the order. An order ID alone is insufficient.",
        path: "/bridge/source-transaction",
        inputSchema: {
            source_chain: z.enum(DOWNLOAD_CHAIN_VALUES),
            source_tx_hash: txHash,
            bridge_order_id: z.string().max(256).optional(),
        },
        summarize: () => "Source-chain transaction read by hash; bridge-order association remains unverified without protocol evidence.",
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
        title: "Address downstream activity graph",
        description: "Address downstream activity graph: complete hops for each address path, with historical transfer aggregates. This is not attribution of a source batch. Use trace_fund_batch/trace_funds for FIFO/proportional attribution. Token Contract defines asset identity; known symbols resolve through the curated registry. Capped at depth 5 and 500 nodes.",
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
        description: "Create an asynchronous export for one address or up to 100 addresses in one combined export. Supply address or addresses, exactly one form. Every address and selected dataset is exported, with token/time filters applied to all addresses. Multi-address output is ZIP containing CSV or zstd-compressed Parquet entries. Returns a job_id immediately; use get_export_address_summary or batch_get_export_address_summary to read completed evidence directly on the server.",
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
        name: "get_export_address_summary",
        title: "Summarize completed export by payer address",
        description: "Read a COMPLETED token-transfer export on the server, filtered to one token, direction and optional payer. Returns every full payer address through cursor pagination, global matched record count, exact total amounts as decimal strings, highest observed exported block, and independently paginated original transfer details. Totals cover ALL matching records, not just this page. Use token_contract when a symbol is ambiguous. Direction is relative to address; a single exported address is inferred. Continue cursor for addresses and detail_cursor for details using the same filters. Cursors expire on API restart. No public download link is required. This is exported historical transfer evidence, not a current balance or a proof of full-chain coverage.",
        path: "/export/address-summary",
        inputSchema: {
            job_id: z.string().uuid(),
            ...tokenFilters,
            address: address.optional().describe("Direction reference address; inferred for a single-address export. Required for in/out on multi-address exports."),
            direction,
            from_address: address.optional().describe("Optional complete payer address filter; also useful to reconcile one address against its transfer details."),
            page_size: z.number().int().min(1).max(500).default(100),
            cursor: z.string().max(2048).optional().describe("next_cursor returned by a prior page with the same export and filters."),
            detail_page_size: z.number().int().min(1).max(500).default(100),
            detail_cursor: z.string().max(2048).optional().describe("next_detail_cursor returned by a prior detail page with the same export and filters."),
        },
        summarize: (result) => {
            const data = result.data;
            return `Completed export: ${data.total_records} matching records across ${data.total_addresses} payer addresses; exact total amount ${data.total_amount}. More address pages: ${data.has_more}; more detail pages: ${data.details_has_more}.`;
        },
    },
    {
        name: "ensure_chain_data",
        title: "Ensure chain data through the system downloader",
        description: "Use before analysis when Coverage is incomplete. Solana full, time and slot ranges use dRPC finalized signature pagination and parsed transactions; do not claim full coverage until the job and required datasets are completed. Created AI jobs are isolated from user_download jobs.",
        path: "/data/ensure", method: "POST", readOnly: false,
        inputSchema: {
            chain: downloadChain,
            addresses: z.array(downloadAddress).min(1).max(200),
            datasets: z.array(z.enum(["transactions", "token_transfers", "internal_transactions", "logs", "balances"])).min(1),
            range: ensureRange,
            performance_mode: z.enum(["safe", "fast", "turbo"]).default("fast"),
        },
        summarize: () => "Coverage checked through the system downloader; inspect status and download_job_id before querying evidence.",
    },
    {
        name: "ensure_chain_data_batch",
        title: "Start multiple chain downloads concurrently",
        description: "Submit 2-20 independent Coverage/download requests in one MCP call. The server starts them concurrently with a bounded fan-out, so ChatGPT does not have to serialize separate tool confirmations. Each result retains its own chain, status and download_job_id; identical requests may be reused by platform idempotency.",
        path: "/data/ensure", method: "POST", readOnly: false,
        inputSchema: {
            requests: z.array(ensureRequest).min(2).max(20),
            max_concurrent: z.number().int().min(1).max(8).default(4),
        },
        summarize: () => "Independent Coverage/download requests were submitted with bounded concurrency; inspect every result separately.",
    },
    {
        name: "get_ai_download_status",
        title: "Get AI download task status",
        description: "Poll only download jobs created by ChatGPT/MCP. It cannot read ordinary user_download jobs. Wait for completed/ready before treating newly acquired coverage as available.",
        path: "/data/download-status",
        inputSchema: { download_job_id: z.string().uuid() },
        summarize: () => "AI-managed system download status retrieved.",
    },
    {
        name: "retry_ai_download_failures",
        title: "Retry failed AI download parts",
        description: "Retry failed or partially failed portions of a ChatGPT/MCP download. Completed shards are preserved; unrecoverable failed pages are fetched again. Use bounded retries rather than repeatedly creating replacement full-history jobs.",
        path: "/data/download-retry", method: "POST", readOnly: false,
        inputSchema: { download_job_id: z.string().uuid() },
        summarize: (r) => `Failed portions queued for backfill (${String(r.data?.reset_shards ?? 0)} shard(s) reset).`,
    },
];
toolDefinitions.push(...crossChainTools);
toolDefinitions.push(...investigationTools);
toolDefinitions.push(...nextInvestigationTools);
toolDefinitions.push(...createBatchTools(toolDefinitions.slice()));
// Return an MCP file resource, rather than hiding the download URL in JSON.
export function exportFileResources(result) {
    const data = result.data;
    if (!result.success || !data || data.status !== "completed" || !Array.isArray(data.files))
        return [];
    return data.files.map((value) => {
        const file = value;
        if (!file || typeof file.url !== "string" || typeof file.name !== "string")
            throw new Error("Completed export file metadata is invalid");
        const url = new URL(file.url);
        if (url.protocol !== "https:" || url.username || url.password || !url.pathname.startsWith("/api/v1/download/"))
            throw new Error("Completed export download URL is invalid");
        const hostname = url.hostname.toLowerCase();
        if (hostname === "localhost" || hostname.endsWith(".localhost") || hostname === "[::1]" || /^127\./.test(hostname) || hostname === "0.0.0.0")
            throw new Error("Completed export download URL points to loopback");
        return {
            type: "resource_link",
            uri: file.url,
            name: file.name,
            mimeType: typeof file.mime_type === "string" ? file.mime_type : "application/octet-stream",
            ...(typeof file.size_bytes === "number" && Number.isSafeInteger(file.size_bytes) && file.size_bytes >= 0 ? { size: file.size_bytes } : {}),
            annotations: { audience: ["user", "assistant"] },
        };
    });
}
export function validateToolInput(name, input) {
    validateCrossChainTool(name, input);
    validateNextTool(name, input);
    if (["get_token_balance", "trace_funds", "trace_fund_batch", "aggregate_transfers"].includes(name) && !input.token && !input.token_contract)
        throw new Error("token or token_contract is required");
    if (input.timestamp && (input.block !== undefined || input.blockTag !== undefined))
        throw new Error("timestamp and block/blockTag are mutually exclusive");
    if (input.block !== undefined && input.blockTag !== undefined)
        throw new Error("use block or blockTag");
    if (name === "read_contract") {
        const check = (value) => { if (typeof value === "number" && !Number.isSafeInteger(value))
            throw new Error("ABI integers outside safe JSON range must be strings"); if (Array.isArray(value))
            value.forEach(check);
        else if (value && typeof value === "object")
            Object.values(value).forEach(check); };
        check(input.args);
    }
    if ((name === "find_fund_paths" || name === "build_address_network" || name === "get_export_address_summary") && !input.token && !input.token_contract) {
        throw new Error("token or token_contract is required; cross-asset amounts cannot be aggregated");
    }
    if (name === "create_large_export") {
        const single = typeof input.address === "string" && input.address.trim().length > 0;
        const multiple = Array.isArray(input.addresses) && input.addresses.length > 0;
        if (single === multiple)
            throw new Error("Provide address or addresses, exactly one form");
        const valid = (input.format === "parquet" && input.compression === "zstd") || (input.format === "csv" && input.compression === "zip");
        if (!valid)
            throw new Error("Supported export pairs are parquet+zstd and csv+zip");
    }
}
export function registerTools(server, client) {
    registerInvestigationResources(server, client);
    for (const definition of toolDefinitions) {
        server.registerTool(definition.name, {
            title: definition.title,
            description: definition.description,
            inputSchema: definition.inputSchema,
            annotations: { readOnlyHint: definition.readOnly !== false, destructiveHint: false, openWorldHint: false },
        }, async (args, extra) => {
            try {
                const input = args;
                if (definition.batch) {
                    const byIndex = new Map();
                    const bytesByIndex = new Map();
                    let resourceBytes = 0;
                    const processResult = (item) => {
                        const response = item.response;
                        const data = response.data;
                        const embedded = item.tool === "get_export_file" || item.tool === "download_export";
                        if (embedded && (typeof data?.content_base64 !== "string" || typeof data.resource_uri !== "string"))
                            throw new Error("EXPORT_RESOURCE_INVALID: Export chunk metadata is missing");
                        const bytes = embedded ? Buffer.byteLength(data.content_base64, "base64") : 0;
                        if (resourceBytes + bytes > (8 << 20))
                            throw new Error("BATCH_RESOURCE_LIMIT_EXCEEDED: Embedded export chunks exceed 8 MiB; retry this input separately or with a smaller chunk_size");
                        let files;
                        try {
                            files = item.tool === "get_export_status" ? exportFileResources(response) : embedded ? investigationFileResources(response) : [];
                        }
                        catch (error) {
                            if (error instanceof Error && /^[A-Z][A-Z0-9_]+:/.test(error.message))
                                throw error;
                            throw new Error(`EXPORT_RESOURCE_INVALID: ${error instanceof Error ? error.message : "Invalid export resource"}`);
                        }
                        resourceBytes += bytes;
                        byIndex.set(item.index, files);
                        bytesByIndex.set(item.index, bytes);
                    };
                    const discardResult = (item) => {
                        resourceBytes -= bytesByIndex.get(item.index) ?? 0;
                        bytesByIndex.delete(item.index);
                        byIndex.delete(item.index);
                    };
                    const { envelope, items } = await executeBatch(client, definition.batch, input, validateToolInput, { signal: extra.signal, processResult, discardResult });
                    const files = items.flatMap(item => item.success ? byIndex.get(item.index) ?? [] : []);
                    envelope.meta.embedded_resource_bytes = resourceBytes;
                    envelope.meta.embedded_resource_limit_bytes = 8 << 20;
                    const counts = envelope.data;
                    return { structuredContent: envelope, content: [{ type: "text", text: `${counts.succeeded} operation(s) succeeded; ${counts.failed} failed. Every result retains its own precision, evidence and pagination.` }, ...files] };
                }
                validateToolInput(definition.name, input);
                if (definition.name === "ensure_chain_data_batch") {
                    const requests = input.requests;
                    const operation = toolDefinitions.find(item => item.name === "ensure_chain_data");
                    const { envelope, items } = await executeBatch(client, { kind: "calls", operations: new Map([[operation.name, operation]]) }, { requests: requests.map(arguments_ => ({ tool: operation.name, arguments: arguments_ })), max_concurrent: input.max_concurrent }, validateToolInput, { signal: extra.signal });
                    const results = items.map(({ index, input: request, success, started, response, error }) => ({ index, request, success, started, ...(response ? { response } : {}), ...(error ? { error } : {}) }));
                    const started = results.filter((item) => item.success).length;
                    const failed = results.length - started;
                    const result = {
                        success: failed === 0,
                        data: { results, started, failed },
                        meta: envelope.meta,
                        error: failed ? { code: "BATCH_PARTIAL_FAILURE", message: `${failed} of ${results.length} requests failed` } : null,
                    };
                    return { structuredContent: result, content: [{ type: "text", text: `${started} download request(s) started; ${failed} failed during submission.` }] };
                }
                const result = definition.method === "POST" ? await client.post(definition.path, input, { signal: extra.signal }) : await client.get(definition.path, input, { signal: extra.signal });
                const files = definition.name === "get_export_status" ? exportFileResources(result) : investigationFileResources(result);
                return {
                    structuredContent: result,
                    content: [{ type: "text", text: definition.summarize(result) }, ...files],
                };
            }
            catch (error) {
                const message = error instanceof Error ? error.message : "Unknown MCP tool error";
                return { isError: true, content: [{ type: "text", text: message }] };
            }
        });
    }
}
//# sourceMappingURL=tools.js.map