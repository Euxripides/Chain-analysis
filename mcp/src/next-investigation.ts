import { z } from "zod";
import { contractAnalysisInputShape, validateContractTool } from "./contract-analysis.js";
import type { ToolDefinition } from "./batch.js";
import { EVM_CHAIN_VALUES } from "./chains.js";

const chain=z.enum(EVM_CHAIN_VALUES);
const address=z.string().regex(/^0x[0-9a-fA-F]{40}$/);
const hash=z.string().regex(/^0x[0-9a-fA-F]{64}$/);
const integer=z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const block=z.union([integer,z.string().regex(/^(latest|safe|finalized|earliest|0x[0-9a-fA-F]+|[0-9]+)$/)]).optional();
const state={block,timestamp:z.string().datetime({offset:true}).optional()};
const page={page_size:z.number().int().min(1).max(500).default(100),cursor:z.string().max(2048).optional()};
const budget={max_records:z.number().int().min(1).max(20000).default(5000)};
const execution={mode:z.enum(["inline","async"]).default("inline"),idempotency_key:z.string().max(128).optional()};
const range={from_block:integer,to_block:integer};
const readShape:Record<string,Record<string,z.ZodTypeAny>>={
 reconcile_address_ledger:{chain,address,token_contract:address,from_block:integer.min(1),to_block:integer.optional(),...state,...budget,repair:z.boolean().default(false),...execution},
 trace_source_set:{chain,token_contract:address,sources:z.array(z.object({tx_hash:hash,log_index:integer.optional(),amount_raw:z.string().regex(/^[0-9]{1,78}$/).optional(),source_id:z.string().min(1).max(128).optional()}).strict()).min(1).max(100),...state,to_block:integer.optional(),attribution_model:z.enum(["both","fifo","proportional"]).default("both"),max_hops:z.number().int().min(1).max(5).default(3),max_nodes:z.number().int().min(2).max(100).default(50),...budget,...execution},
 explain_transaction:{chain,tx_hash:hash,include_trace:z.boolean().default(true),...execution},
 match_bridge_transfer:{chain,tx_hash:hash,destination_chain:chain,destination_tx_hash:hash,protocol:z.literal("cctp_v1"),...execution},
 get_token_allowances:{chain,address,token_contract:address,spenders:z.array(address).max(200).optional(),discover:z.boolean().default(false),from_block:integer.optional(),to_block:integer.optional(),...state,...budget,...execution},
 get_control_history:{chain,address,...range,...budget,...execution},
};
function tool(name:string,description:string,inputSchema:Record<string,z.ZodTypeAny>,readOnly=true):ToolDefinition{return {name,title:name,description,path:`/${name.includes("evidence_bundle")?"export":"analysis"}/${name}`,method:"POST",readOnly,inputSchema,summarize:r=>JSON.stringify(r.data)}}
const descriptions:Record<string,string>={
 reconcile_address_ledger:"Reconcile exact ERC20 opening/closing RPC state against deduplicated indexed transfers. Separate balanced, coverage and reconciled flags; signed raw differences preserve precision. repair=true schedules only existing coverage gaps and requires a fresh reconciliation. Use async mode to save immutable inputs and read all evidence through cursor pages.",
 trace_source_set:"Attribute 1-100 confirmed ERC20 source receipts jointly on ONE ledger under FIFO and proportional models. Raw integer amounts; each source activates at its own log. Overlapping tracked funds are rejected, never counted twice. Returns per-source conservation, shared per-edge limits, frontier and evidence. Incomplete indexed history is explicit. Async freezes actual ledger/baselines and offers address/edge/evidence cursor pages.",
 explain_transaction:"Explain a confirmed transaction with committed ERC20 Transfer events, successful native call-frame movements, exact per-asset signed transfer deltas and gas separated. Reverted ancestors suppress child movements; DELEGATECALL/STATICCALL values are not transfers. Trace/ABI failures remain explicit. Event deltas are not exact per-transaction storage-state proof.",
 match_bridge_transfer:"Verify two CCTP V1 mainnet receipts using Circle's registered MessageTransmitter/TokenMessenger emitters, domains, nonce, sender, message body, mint recipient and amount. Supports Ethereum/Avalanche/Optimism/Arbitrum/Base/Polygon; requires both hashes. Exact-message proof is separate from field proof; replacement messages are not falsely called fresh burns.",
 get_token_allowances:"Read real ERC20 allowance(owner,spender) at one pinned current/block/timestamp snapshot. Explicit spenders or bounded Approval-event discovery, maximum 200. Events discover candidates only. Per-spender failures and incomplete discovery are explicit; allowance_raw is a string. Permit2/NFT/operator approvals require separate adapters.",
 get_control_history:"Read standard owner/admin/upgrade/role/pause events in a fixed range, plus contract-control state at both block boundaries. Raw log evidence remains available. Custom/silent changes and exact intra-transaction storage history remain unproven. Maximum 100000 blocks, with adaptive bounded RPC windows.",
};
const operationNames=Object.keys(readShape) as [string,...string[]];
const choices=operationNames.map(name=>z.object({operation:z.literal(name),arguments:z.object(readShape[name]).strict(),idempotency_key:z.string().max(128).optional()}).strict());
choices.push(z.object({operation:z.literal("analyze_contract"),arguments:z.object(contractAnalysisInputShape).strict(),idempotency_key:z.string().max(128).optional()}).strict());
const rule={chain,address,token_contract:address.optional(),spenders:z.array(address).max(200).optional(),name:z.string().min(1).max(400).refine(v=>Array.from(v).length<=200,"name must contain at most 200 Unicode characters"),rule_type:z.enum(["balance_change","allowance_change","control_change"]),interval_seconds:z.number().int().min(60).max(86400).default(300),confirmations:integer.min(2).max(1000).default(12),state:z.enum(["active","paused","disabled"]).default("paused")};
export const nextInvestigationTools:ToolDefinition[]=[
 tool("get_investigation_capabilities","Inspect installed investigation operations, EVM/bridge scope, exact balance sources, worker/record/page budgets and known limitations.",{chain}),
 ...operationNames.map(name=>tool(name,descriptions[name],readShape[name],false)),
 {name:"start_analysis",title:"Start durable investigation analysis",description:"Queue one validated investigation operation. Durable PostgreSQL leases, fenced publication and bounded attempts; immutable joint/reconciliation checkpoints survive worker restarts. Reusing idempotency_key with changed semantic arguments returns conflict. Allocation restarts from saved input; it does not checkpoint each individual transfer.",path:"/analysis/start_analysis",method:"POST",readOnly:false,inputSchema:z.discriminatedUnion("operation",choices as any),summarize:r=>JSON.stringify(r.data)},
 tool("get_analysis_status","Read scoped durable analysis status, stage, attempts, errors and input/result checksums. include_metadata=false omits heavy summaries but retains section counts.",{analysis_id:z.string().uuid(),include_metadata:z.boolean().default(true)}),
 tool("get_analysis_results","Read ALL saved analysis rows by section (evidence, edges, addresses, events, allowances, funds_legs, etc.) using authenticated snapshot-bound cursors. Metadata lists available sections/counts. Results publish atomically after completion; there is no top-200 cutoff. include_metadata=false returns compact pages with available_sections, returned_records and has_more.",{analysis_id:z.string().uuid(),include_metadata:z.boolean().default(true),section:z.string().min(1).max(100).default("evidence"),...page}),
 tool("cancel_analysis","Cancel queued/running analysis with lease fencing. Completed results remain readable.",{analysis_id:z.string().uuid()},false),
 tool("resume_analysis","Resume failed/cancelled analysis with original processor and immutable checkpoint when saved. Snapshot reorganization is rejected; a new request creates a new snapshot.",{analysis_id:z.string().uuid()},false),
 tool("create_evidence_bundle","Reuse a saved bundle when available, or build a deterministic ZIP from completed analysis request, materialized input when present, full result and per-entry SHA256 manifest. Returns scoped bundle ID; read bytes through get_evidence_bundle or evidence:// resource.",{analysis_id:z.string().uuid()},false),
 tool("get_evidence_bundle","Read authenticated evidence ZIP bytes directly as MCP embedded resource chunks. Returns whole SHA256, independent chunk checksum and next_offset; no public URL required.",{bundle_id:z.string().uuid(),offset:integer.default(0),chunk_size:z.number().int().min(1).max(4<<20).default(1<<20)}),
 tool("verify_evidence_bundle","Verify saved ZIP SHA256 and every declared entry's size, CRC and SHA256. Integrity does not prove provider accuracy, ledger completeness or beneficial ownership.",{bundle_id:z.string().uuid()}),
 tool("create_watch_rule","Create a durable confirmed-snapshot watch for balance, explicit allowances or standard control fields. Default paused. Active rules write changes into the workspace event inbox; no external notifications. First evaluation establishes baseline; changes between polls can be missed.",rule,false),
 tool("update_watch_rule","Replace watch configuration with optimistic revision check and reset its baseline.",{...rule,rule_id:z.string().uuid(),expected_revision:integer.min(1)},false),
 tool("set_watch_rule_state","Set active/paused/disabled under optimistic revision check; fences running evaluations.",{rule_id:z.string().uuid(),expected_revision:integer.min(1),state:z.enum(["active","paused","disabled"])},false),
 tool("list_watch_rules","List workspace watch rules with live sequence-keyset pagination and filter-bound cursors.",{state:z.enum(["active","paused","disabled"]).optional(),...page}),
 tool("get_watch_events","Read workspace watch events, including explicit reorg baseline replacement. tail_cursor supports later polling; no external notification is sent.",{rule_id:z.string().uuid().optional(),...page}),
];
export function validateNextTool(name:string,input:Record<string,unknown>):void {
 if(name==="start_analysis") {if(input.operation==="analyze_contract")validateContractTool("analyze_contract",input.arguments as Record<string,unknown>);else validateNextTool(String(input.operation),input.arguments as Record<string,unknown>);return}
 if (typeof input.from_block==="number"&&typeof input.to_block==="number"&&input.from_block>input.to_block)throw new Error("from_block must not exceed to_block");
 if (["get_control_history","get_token_allowances"].includes(name)&&typeof input.from_block==="number"&&typeof input.to_block==="number"&&input.to_block-input.from_block>100000)throw new Error("history range exceeds 100000 blocks");
 if (name==="get_token_allowances"&&!(Array.isArray(input.spenders)&&input.spenders.length)&&!input.discover)throw new Error("Provide spenders or discover=true");
 if (name==="get_token_allowances"&&input.discover&&(input.from_block===undefined||input.to_block===undefined))throw new Error("Discovery requires from_block/to_block");
 if (["create_watch_rule","update_watch_rule"].includes(name)&&input.rule_type==="allowance_change"&&(!input.token_contract||!(Array.isArray(input.spenders)&&input.spenders.length)))throw new Error("Allowance watch requires token_contract and explicit spenders");
}
