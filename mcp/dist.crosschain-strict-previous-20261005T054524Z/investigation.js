import { z } from "zod";
import { ResourceTemplate } from "@modelcontextprotocol/sdk/server/mcp.js";
import { createHash } from "node:crypto";
import { EVM_CHAIN_VALUES } from "./chains.js";
const chain = z.enum(EVM_CHAIN_VALUES);
const address = z.string().regex(/^0x[0-9a-fA-F]{40}$/);
const hash = z.string().regex(/^0x[0-9a-fA-F]{64}$/);
const block = z.union([z.string().regex(/^(latest|safe|finalized|earliest|pending|0x[0-9a-fA-F]+|[0-9]+)$/), z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER)]).optional();
const timestamp = z.string().datetime({ offset: true }).optional();
const state = { block, blockTag: z.string().optional(), timestamp };
const timeFilters = { start_time: timestamp, end_time: timestamp };
const paging = { page: z.number().int().min(1).default(1), page_size: z.number().int().min(1).max(500).default(100), cursor: z.string().max(2048).optional() };
const abi = z.union([z.array(z.record(z.unknown())).max(10000), z.string().max(4 << 20)]).optional();
const token = { token: z.string().optional(), token_contract: address.optional() };
const readMethods = ["eth_call", "eth_getCode", "eth_getStorageAt", "eth_getTransactionByHash", "eth_getTransactionReceipt", "eth_getLogs", "eth_getBalance", "eth_blockNumber", "eth_getBlockByNumber"];
const summarize = (r) => JSON.stringify(r.data);
function tool(name, description, inputSchema) {
    return { name, title: name, description, path: `/${name.includes("export") ? "export" : "analysis"}/${name}`, method: "POST", readOnly: true, inputSchema, summarize };
}
export const investigationTools = [
    tool("batch_get_native_balances", "Read current or historical native balances for 1-200 addresses at one shared RPC block. Bounded concurrency and independent errors; stored net flow is never used.", { chain, addresses: z.array(address).min(1).max(200), ...state, max_concurrent: z.number().int().min(1).max(8).default(4) }),
    tool("batch_get_token_balances", "Read exact ERC20 balances for 1-200 addresses at one shared current/block/timestamp RPC snapshot. Contract is asset identity; monetary values are strings and individual failures remain visible.", { chain, addresses: z.array(address).min(1).max(200), token_contract: address, ...state, max_concurrent: z.number().int().min(1).max(8).default(4) }),
    tool("rpc_call", "Alias of read_rpc: safe read-only EVM allowlist, original params order, no custom endpoint, writes or state overrides.", { chain, method: z.enum(readMethods), params: z.array(z.unknown()).max(3) }),
    tool("resolve_proxy", "Alias of resolve_proxy_implementation: supported proxy slots/bytecode at selected block with chain evidence.", { chain, address, ...state }),
    tool("trace_fund_batch", "Attribute a confirmed outgoing ERC20 source batch. source_address must match the Transfer sender. Supply source_tx_hash/log_index, or start_time/end_time for indexed selection; ambiguous matches return candidates for explicit selection. actual_transfer_amount and attributed_amount are separate, with per-transfer balances, exact share fraction, FIFO fragment consumption and model comparison. Default BOTH models are accounting inference, not proven fungible identity.", { chain, source_address: address, source_tx_hash: hash.optional(), token_contract: address, amount: z.string().optional(), ...timeFilters, log_index: z.number().int().nonnegative().optional(), max_depth: z.number().int().min(1).max(5).default(5), attribution_model: z.enum(["proportional", "fifo", "both"]).default("both"), max_nodes: z.number().int().min(2).max(100).default(50), max_records: z.number().int().min(1).max(20000).default(5000) }),
    tool("get_native_balance", "Actual native coin state balance at current, numeric block or ISO8601 timestamp. Returns exact monetary strings, balance_source, as_of_block and is_current_balance. Historical archive failures are explicit.", { chain, address, ...state }),
    tool("get_token_balance", "Actual ERC20 balanceOf and decimals/symbol at the same pinned block. Use the exact token address in token or token_contract. Missing token metadata leaves formatted balance null while preserving exact balance_raw.", { chain, address, ...token, ...state }),
    tool("get_all_token_balances", "Native coin plus real ERC20 state balances, pinned to one block across cursor pages. Discover token contracts from indexed transfers or supply tokens. Discovery is explicitly incomplete; historical net flow is never presented as balance.", { chain, address, ...state, tokens: z.array(address).max(1000).optional(), page_size: z.number().int().min(1).max(100).default(20), cursor: paging.cursor }),
    tool("get_balance_at", "Actual native/ERC20 state at the last block at or before timestamp; token address optional for native.", { chain, address, ...token, timestamp: z.string().datetime({ offset: true }) }),
    tool("get_balance_history", "Paginated sampled actual archive state between start/end ISO8601 timestamps. interval_seconds is sampling granularity; results are not inferred from net flow.", { chain, address, ...token, start: z.string().datetime({ offset: true }), end: z.string().datetime({ offset: true }), interval_seconds: z.number().int().min(60).default(86400), ...paging, page_size: z.number().int().min(1).max(32).default(8) }),
    tool("read_rpc", "Safe read-only EVM JSON-RPC allowlist through the existing gateway. params preserve original JSON-RPC ordering. No custom endpoints, state overrides or write methods. eth_call bounded gas/zero value; logs require blockHash or explicit fixed range within chain limit.", { chain, method: z.enum(readMethods), params: z.array(z.unknown()).max(3) }),
    tool("eth_call", "Safe read-only contract simulation at blockTag/block; call accepts to/from/data/input/gas/zero value, bounded gas and no state overrides.", { chain, call: z.object({ to: address, from: address.optional(), data: z.string().optional(), input: z.string().optional(), gas: z.string().optional(), value: z.literal("0x0").optional() }).strict(), block, blockTag: state.blockTag }),
    tool("eth_getCode", "Read EVM bytecode at a selected block.", { chain, address, block, blockTag: state.blockTag }),
    tool("eth_getStorageAt", "Read one storage slot (hex quantity up to 256 bits), supporting historical blockTag.", { chain, address, position: z.string().regex(/^0x[0-9a-fA-F]{1,64}$/), block, blockTag: state.blockTag }),
    tool("eth_getTransactionByHash", "Read complete raw RPC transaction; hex quantities remain exact strings.", { chain, tx_hash: hash }),
    tool("eth_getTransactionReceipt", "Read raw receipt, logs and status from the chain; missing receipts remain null.", { chain, tx_hash: hash }),
    tool("eth_getLogs", "Read bounded logs. filter requires blockHash or fixed fromBlock/toBlock (hex/decimal strings), up to 20 addresses and four topic positions. Narrow ranges to configured chain limit.", { chain, filter: z.object({ address: z.union([address, z.array(address).min(1).max(20)]).optional(), topics: z.array(z.union([hash, z.null(), z.array(z.union([hash, z.null()])).max(20)])).max(4).optional(), fromBlock: z.string().optional(), toBlock: z.string().optional(), blockHash: hash.optional() }).strict() }),
    tool("eth_getBalance", "Read exact raw native balance as a hex quantity, supporting blockTag. Prefer get_native_balance for formatted source-qualified state.", { chain, address, block, blockTag: state.blockTag }),
    tool("eth_blockNumber", "Read chain head as an exact hex quantity.", { chain }),
    tool("eth_getBlockByNumber", "Read a block by blockTag or number, optionally including complete transactions.", { chain, block, blockTag: state.blockTag, full_transactions: z.boolean().default(false) }),
    tool("get_contract_abi", "Retrieve cached or verified Sourcify ABI; resolve supported proxies at selected state. ABI provenance and verification are explicit; caller ABI is unverified.", { chain, address, ...state, abi }),
    tool("decode_transaction_input", "Decode calldata using the target contract ABI and implementation at the transaction block. Supply tx_hash or address+input; optional abi. Unknown selectors return candidate signatures without treating them as verified.", { chain, address: address.optional(), tx_hash: hash.optional(), input: z.string().optional(), ...state, abi }),
    tool("decode_logs", "Decode up to 200 logs, preserving original evidence. Supply tx_hash for receipt or raw logs. Proxy ABI is selected at each log block. Exact integers use decimal strings.", { chain, address: address.optional(), tx_hash: hash.optional(), logs: z.array(z.record(z.unknown())).max(200).optional(), ...state, abi }),
    tool("resolve_proxy_implementation", "Resolve EIP1967 implementation/admin/beacon and canonical EIP1167 bytecode, at the selected block. Returns raw slot evidence and incomplete probe errors; unknown custom proxies remain unproven.", { chain, address, ...state }),
    tool("read_contract", "Typed safe eth_call with full ABI encoding/decoding, including tuples/arrays. function is ABI name or canonical signature. Integer args MUST use strings to preserve uint256 precision. Without ABI provide canonical signature and output_types. No transaction is submitted.", { chain, address, function: z.string().min(1), args: z.array(z.unknown()).max(1024).default([]), output_types: z.array(z.string()).max(128).optional(), abi, ...state }),
    tool("aggregate_transfers", "Server-side deduplicated ERC20 group-by over all matching indexed records. Cursor/page supports more than 200 groups; returns full totals/counts/cutoff, exact monetary strings and stable block/ingestion cursor. Assets are grouped separately. Raw evidence remains available via get_token_transfers or completed export summary.", { chain, address, ...token, direction: z.enum(["all", "in", "out"]).default("all"), counterparty: address.optional(), group_by: z.array(z.enum(["counterparty", "from_address", "to_address", "token_contract", "direction", "day"])).min(1).max(4).default(["counterparty"]), metrics: z.array(z.enum(["sum_amount", "tx_count", "record_count", "first_time", "last_time"])).optional(), order_by: z.enum(["sum_amount", "tx_count", "record_count", "first_time", "last_time"]).default("sum_amount"), ...timeFilters, ...paging, page_size: z.number().int().min(1).max(1000).default(100), ...state }),
    ...["get_export_file", "download_export"].map(name => tool(name, "Read completed export bytes directly under workspace authorization. Returns an embedded MCP blob resource, file SHA256 and independently hashed bounded chunk; follow next_offset for large files and verify whole SHA256. No public download URL required.", { job_id: z.string().uuid(), offset: z.number().int().min(0).max(Number.MAX_SAFE_INTEGER).default(0), chunk_size: z.number().int().min(1).max(4 << 20).default(1 << 20) })),
    tool("trace_funds", "Track a specific ERC20 source Transfer lot under BOTH proportional and FIFO models. amount is human decimal string; omit to use full selected transfer. Multiple matching logs require log_index. Returns exact raw attribution, conservation, archive baselines, full evidence, model assumptions and explicit coverage/truncation. Accounting models do not prove fungible unit identity.", { chain, source_tx: hash, ...token, attribution_model: z.enum(["proportional", "fifo", "both"]).default("both"), amount: z.string().optional(), log_index: z.number().int().nonnegative().optional(), max_hops: z.number().int().min(1).max(5).default(3), time_window: z.number().int().min(1).max(31622400).default(604800), max_nodes: z.number().int().min(2).max(100).default(50), max_records: z.number().int().min(1).max(20000).default(5000) }),
    tool("get_labels", "Source-qualified address/entity labels from a versioned partial public exchange registry, current workspace labels and onchain classification probes (token, pair, router, multisig, proxy, bridge candidates). label_source/confidence/first_seen are explicit, unknown first_seen is null. Coverage of exchange labels is incomplete.", { chain, address, ...state }),
    tool("detect_exchange_deposit", "Distinguish a directly labeled exchange wallet from an inferred intermediary/deposit path into a labeled exchange. Heuristics return confidence and evidence; transfer adjacency does not prove deposit ownership.", { chain, address, ...token, ...timeFilters, max_nodes: z.number().int().min(1).max(200).default(50) }),
    tool("trace_to_exchange", "Bounded indexed ERC20 paths to labeled exchange wallets with full per-hop aggregate evidence queries. max_nodes/max_hops and coverage/truncation are explicit. No path is not proof of absence.", { chain, source: address, ...token, ...timeFilters, max_hops: z.number().int().min(1).max(5).default(3), max_nodes: z.number().int().min(1).max(200).default(50) }),
    tool("get_contract_control", "Read owner/admin/proxy-admin/implementation, multisig signers, enumerable privileged roles and verified deployment evidence. Report upgrade/pause/withdraw/mint ABI candidates separately from proven authorization. Custom/non-enumerable controls remain incomplete with probe evidence.", { chain, address, ...state, abi }),
];
export function investigationFileResources(result) {
    const data = result.data;
    if (!data || typeof data.content_base64 !== "string" || typeof data.resource_uri !== "string")
        return [];
    const blob = data.content_base64;
    const raw = Buffer.from(blob, "base64");
    if (raw.length !== data.chunk_bytes || createHash("sha256").update(raw).digest("hex") !== data.chunk_sha256)
        throw new Error("EXPORT_INTEGRITY_FAILED: Invalid file chunk received");
    delete data.content_base64;
    return [{ type: "resource", resource: { uri: data.resource_uri, mimeType: String(data.mime_type), blob } }];
}
export function registerInvestigationResources(server, client) {
    server.registerResource("investigation-evidence-chunk", new ResourceTemplate("evidence://{bundle_id}/{offset}/{chunk_size}", { list: undefined }), { title: "Authenticated investigation evidence ZIP", description: "Scoped evidence chunk with SHA256; authorization is checked on every read", mimeType: "application/zip" }, async (uri, v, extra) => {
        const bundle_id = z.string().uuid().parse(v.bundle_id);
        const offset = z.coerce.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER).parse(v.offset);
        const chunk_size = z.coerce.number().int().min(1).max(4 << 20).parse(v.chunk_size);
        const result = await client.post("/export/get_evidence_bundle", { bundle_id, offset, chunk_size }, { signal: extra.signal });
        const content = investigationFileResources(result)[0];
        if (!content || content.type !== "resource")
            throw new Error("Evidence chunk unavailable");
        return { contents: [{ ...content.resource, uri: uri.href }] };
    });
    server.registerResource("completed-export-chunk", new ResourceTemplate("export://{job_id}/{offset}/{chunk_size}", { list: undefined }), { title: "Authenticated completed export bytes", description: "Bounded export file chunk; workspace authorization is checked on every read", mimeType: "application/octet-stream" }, async (uri, v, extra) => {
        const job_id = z.string().uuid().parse(v.job_id);
        const offset = z.coerce.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER).parse(v.offset);
        const chunk_size = z.coerce.number().int().min(1).max(4 << 20).parse(v.chunk_size);
        const result = await client.post("/export/get_export_file", { job_id, offset, chunk_size }, { signal: extra.signal });
        const content = investigationFileResources(result)[0];
        if (!content || content.type !== "resource")
            throw new Error("Export chunk unavailable");
        return { contents: [{ ...content.resource, uri: uri.href }] };
    });
}
//# sourceMappingURL=investigation.js.map