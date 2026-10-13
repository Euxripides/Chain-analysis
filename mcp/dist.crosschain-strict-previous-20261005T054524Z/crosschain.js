import { z } from "zod";
const chains = z.enum(["ethereum", "bsc", "tron", "polygon", "arbitrum", "optimism", "base", "avalanche", "solana"]);
const identifier = z.string().min(1).max(256).regex(/^[^\r\n\x00]+$/);
const amount = z.string().max(100).regex(/^\d+(\.\d+)?$/, "Use a plain decimal string; preserve precision");
const completeHash = (chain, hash) => chain === "solana" ? /^[1-9A-HJ-NP-Za-km-z]{64,88}$/.test(hash) : chain === "tron" ? /^(0x)?[0-9a-fA-F]{64}$/.test(hash) : /^0x[0-9a-fA-F]{64}$/.test(hash);
export const bridgeQuerySchema = z.object({
    bridge: z.enum(["auto", "lifi", "near_intents", "bridgers", "layerzero", "cbridge", "across", "relay", "wormhole", "cctp"]).default("auto"),
    chain: chains.optional(), tx_hash: identifier.optional(), order_id: identifier.optional(), transfer_id: identifier.optional(),
    deposit_address: identifier.optional(), deposit_memo: identifier.optional(), max_results: z.number().int().min(1).max(50).default(20),
}).strict().superRefine((value, ctx) => {
    const fail = (message) => ctx.addIssue({ code: z.ZodIssueCode.custom, message });
    if (!value.tx_hash && !value.order_id && !value.transfer_id && !value.deposit_address)
        fail("A transaction hash or protocol query identifier is required");
    if (value.tx_hash && !value.chain)
        fail("chain is required with tx_hash");
    if (value.bridge === "auto" && !value.tx_hash)
        fail("Specify bridge for an order/deposit/transfer identifier");
    if (value.bridge === "bridgers" && !value.order_id)
        fail("BRIDGERS requires an existing order_id");
    if (value.bridge === "cbridge" && !value.transfer_id)
        fail("cBridge requires transfer_id");
    if (["lifi", "layerzero", "across", "wormhole", "cctp"].includes(value.bridge) && !value.tx_hash)
        fail("This bridge requires tx_hash");
    if (value.bridge === "near_intents" && !value.tx_hash && !value.deposit_address)
        fail("NEAR requires tx_hash or deposit_address");
    if (value.bridge === "relay" && !value.tx_hash && !value.order_id)
        fail("Relay requires tx_hash or order_id");
    if (value.deposit_address && value.bridge !== "near_intents" || value.deposit_memo && !value.deposit_address)
        fail("deposit_address/memo require an explicit NEAR deposit query");
    if (value.transfer_id && (value.bridge !== "cbridge" || !/^(0x)?[0-9a-fA-F]{64}$/.test(value.transfer_id)))
        fail("cBridge requires a complete 32-byte transfer ID");
    if (value.order_id && ["auto", "near_intents", "cbridge", "layerzero"].includes(value.bridge))
        fail("order_id is unsupported for this query type");
    if (value.chain && !value.tx_hash)
        fail("chain requires tx_hash");
    if (value.tx_hash && value.chain && !completeHash(value.chain, value.tx_hash))
        fail("Complete chain-specific transaction hash required");
});
export const collisionSchema = z.object({
    anchor: z.object({ chain: chains, tx_hash: identifier, dataset: z.enum(["transactions", "token_transfers", "internal_transactions"]), event_index: z.number().int().min(0).max(4294967295).optional() }).strict(),
    search_chain: chains, search_addresses: z.array(identifier).min(1).max(10),
    search_direction: z.enum(["in", "out"]).describe("Direction relative to search_addresses: deposits into a bridge are in, payouts from a bridge are out"),
    direction: z.enum(["forward", "backward"]), asset_kind: z.enum(["native", "token"]), token_contract: identifier.optional(),
    asset_relation: z.enum(["same_asset", "swap"]).describe("Caller-declared relation; candidate search does not verify cross-chain token equivalence"),
    expected_amount: amount.optional().describe("Expected amount on the searched chain; required for swaps. This explicit condition is not historical exchange proof"),
    absolute_tolerance: amount.default("0"), relative_tolerance_bps: z.number().int().min(0).max(500).default(0),
    start_time: z.string().datetime({ offset: true }), end_time: z.string().datetime({ offset: true }), counterparty: identifier.optional(),
    max_parts: z.number().int().min(1).max(3).default(1), max_results: z.number().int().min(1).max(50).default(20),
    scan_limit: z.number().int().min(1).max(1000).default(500), max_combinations: z.number().int().min(1).max(20000).default(5000),
}).strict().superRefine((value, ctx) => {
    const fail = (message) => ctx.addIssue({ code: z.ZodIssueCode.custom, message });
    const duration = Date.parse(value.end_time) - Date.parse(value.start_time);
    if (!(duration > 0 && duration <= 86400000))
        fail("Search time range must be positive and at most 24 hours");
    if (value.search_chain === value.anchor.chain)
        fail("Search and anchor chains must differ");
    if (value.asset_kind === "token" && !value.token_contract)
        fail("Complete target token contract/mint required");
    if (value.asset_kind === "native" && value.token_contract)
        fail("Native asset must not have token_contract");
    if (value.asset_relation === "swap" && !value.expected_amount)
        fail("Swap candidates require expected_amount");
    if (value.anchor.dataset === "transactions" && value.anchor.event_index !== undefined)
        fail("Top-level anchor has no event_index");
    if (!completeHash(value.anchor.chain, value.anchor.tx_hash))
        fail("Complete anchor transaction hash required");
    if (value.expected_amount && !/[1-9]/.test(value.expected_amount))
        fail("expected_amount must be positive");
});
export function validateCrossChainTool(name, input) {
    if (name === "query_cross_chain_transfer")
        bridgeQuerySchema.parse(input);
    if (name === "find_cross_chain_candidates")
        collisionSchema.parse(input);
}
export const crossChainTools = [
    { name: "get_cross_chain_capabilities", title: "Get bridge query capabilities", description: "Inspect implemented bridge adapters, required identifiers, credential configuration and bounded collision limits before a cross-chain investigation. A provider being configured does not assert historical coverage or client-side refresh.", path: "/bridge/capabilities", inputSchema: {}, summarize: () => "Bridge query capabilities and server-side configuration retrieved." },
    { name: "query_cross_chain_transfer", title: "Query a cross-chain transfer", description: "Read bridge correlation from one chain and a source or destination transaction hash, or an existing order/deposit/transfer ID. Auto lookup supports LI.FI, LayerZero, Across, Wormhole, CCTP, NEAR 1Click and Relay; BRIDGERS and cBridge require explicit identifiers. Missing indexed events are read by exact hash from the configured chain providers. Inspect pairing_verified, on_chain_verified, message_only, verification/read_gaps and protocol_hints separately. Analysis-scoped credentials may also receive a bounded candidate_searches result derived from protocol beneficiary/asset/time constraints. Candidates are never promoted by a score. Creates no orders or dataset download jobs.", path: "/bridge/transfer", method: "POST", inputSchema: bridgeQuerySchema.innerType().shape, summarize: (result) => { const data = result.data; return `${data.transfers?.length ?? 0} protocol-linked record(s); ${data.transfers?.filter(t => t.on_chain_verified).length ?? 0} funding-verified; ${data.transfers?.filter(t => t.message_only).length ?? 0} message-only. ${data.attempts?.map(item => `${item.bridge}: ${item.status}`).join(", ") ?? "Inspect attempts"}.`; } },
    { name: "find_cross_chain_candidates", title: "Find bounded cross-chain candidates", description: "Use after protocol lookup when a direct bridge link is unavailable. Explicit anchor dataset/event, target chain, addresses, time range and target native/token asset are required. Exact decimal matching supports at most three same-party parts within five minutes, with a bounded combination budget. Swap searches require an explicit expected target amount. Results are candidates with reasons, ranking scores, signed time differences, coverage and truncation; never proof of bridge association or common ownership. No automatic downloads.", path: "/bridge/candidates", method: "POST", inputSchema: collisionSchema.innerType().shape, summarize: (result) => { const data = result.data; return `${data.candidates?.length ?? 0} cross-chain candidate(s); truncated=${Boolean(data.scan_truncated || data.result_truncated || data.combinations_truncated)}. Scores rank candidates and do not verify pairing.`; } },
];
//# sourceMappingURL=crosschain.js.map