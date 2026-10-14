import { createHash } from "node:crypto";
import { McpServer, ResourceTemplate } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { z } from "zod";
import { EVM_CHAIN_VALUES } from "./chains.js";
import type { ToolDefinition } from "./batch.js";
import { RESTClient, type RESTEnvelope } from "./rest-client.js";

const chain = z.enum(EVM_CHAIN_VALUES);
const address = z.string().regex(/^0x[0-9a-fA-F]{40}$/).describe("Complete EVM contract address");
const tx = z.string().regex(/^0x[0-9a-fA-F]{64}$/);
const integer = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER);
const item = z.string().regex(/^[0-9a-f]{40}$/);
const analysis_id = z.string().uuid();
const page = { cursor: z.string().max(2048).optional(), page_size: z.number().int().min(1).max(200).default(100) };
const budget = z.object({
  max_contracts: z.number().int().min(1).max(50).optional(), max_transactions: z.number().int().min(1).max(50).optional(),
  max_state_reads: z.number().int().min(1).max(512).optional(), max_records: z.number().int().min(1).max(20000).optional(),
  max_rpc_requests: z.number().int().min(10).max(2000).optional(), max_credits: z.number().int().min(1).max(1000000).optional(),
  timeout_seconds: z.number().int().min(30).max(1800).optional(),
}).strict().default({});
export const contractAnalysisInputShape = {
  chain, address, block: z.union([integer,z.enum(["latest","safe","finalized"])]).optional(),
  timestamp: z.string().datetime({ offset: true }).optional(), from_block: integer.optional(), to_block: integer.optional(),
  tx_hashes: z.array(tx).max(50).optional(), include_source: z.boolean().default(true),
  engines: z.array(z.enum(["whatsabi","sevm","solc-ast","heimdall"])).min(2).max(4).default(["whatsabi","sevm","solc-ast","heimdall"]),
  budget, idempotency_key: z.string().max(128).optional(),
};
function tool(name:string,description:string,inputSchema:Record<string,z.ZodTypeAny>,readOnly=true):ToolDefinition {
  return { name,title:name,description,path:`/${name==="get_contract_analysis_artifact"?"export":"analysis"}/contracts/${name}`,method:"POST",readOnly,inputSchema,
    summarize: r => JSON.stringify(r.data) };
}
export const contractAnalysisTools:ToolDefinition[] = [
  tool("analyze_contract","Queue comprehensive EVM contract analysis at an immutable block/hash. Digest-pinned offline WhatsABI, symbolic sevm, verified/recompiled Solidity AST and Heimdall artifacts; custom method semantics preserve guards, steps, exact amount expressions, storage, calls and evidence. Standard proxy code/storage identities stay separate. Supply bounded history and up to 20 transaction hashes by default for observed execution/prestate/diffs. Unsupported paths, provider failures and dependency frontier remain explicit. No on-chain writes or automatic full historical downloads.",contractAnalysisInputShape,false),
  tool("get_contract_analysis","Read scoped contract job status, immutable snapshot, exact section counts and completeness. With section, traverse every saved row using filter/snapshot-bound cursors; no top-200 truncation. Job completed and semantic coverage complete are independent.",{analysis_id,section:z.string().max(100).optional(),method_id:item.optional(),evidence_id:item.optional(),...page}),
  tool("list_contract_methods","List all discovered contract methods, stable method IDs, selectors, verified signatures versus candidates, body-derived Chinese purpose, behaviors and per-method coverage. Filter by text/selector or behavior and traverse all pages.",{analysis_id,search:z.string().max(200).optional(),behavior:z.string().max(100).optional(),...page}),
  tool("get_contract_method_analysis","Read the method model: parameters/outputs, conditions, ordered step references, storage/call/fund/expression/evidence IDs, formulas, dependencies and observed transaction links. Retrieve referenced sections with get_contract_analysis(method_id=...). Function names and comments do not prove behavior.",{analysis_id,method_id:item}),
  tool("get_contract_execution_flow","Read paginated typed reads/writes/guards/calls/transfers/delegates and cross-method state-dependency edges. Static transfer intents remain separate from executed_funds/receipt token_events; every edge retains evidence references.",{analysis_id,method_id:item.optional(),...page}),
  tool("explain_contract_transaction","Read a selected transaction's saved calldata, status, method links, exact native movements, receipt events, call tree, transaction prestate code and state diff. With chain/address/tx_hash and no analysis_id, queue a new analysis. Same-block historical code uses prestate; unavailable trace/archive is an explicit coverage gap.",{analysis_id:analysis_id.optional(),chain:chain.optional(),address:address.optional(),tx_hash:tx,budget},false),
  tool("compare_contract_versions","Compare two completed analyses of the same chain/entry within the authorized workspace. Paginated before/after code identities and method models preserve selector collisions; permission/fund details remain available via method IDs and sections.",{analysis_id,compare_analysis_id:analysis_id,...page}),
  tool("get_contract_analysis_artifact","Read authorized source, pseudocode, IR, runtime bytecode, method CSV, editable graph, full JSON, Markdown report or evidence ZIP as embedded resource chunks. Validate chunk SHA256 and whole artifact SHA256; offset must refer to the saved artifact. No client-selected storage paths or public download URL.",{analysis_id,artifact_id:item,offset:integer.default(0),chunk_size:z.number().int().min(1).max(4<<20).default(1<<20)}),
];
export function validateContractTool(name:string,input:Record<string,unknown>):void {
  if (!contractAnalysisTools.some(t=>t.name===name)) return;
  if (input.block!==undefined&&input.timestamp!==undefined) throw new Error("block and timestamp are mutually exclusive");
  if ((input.from_block===undefined)!==(input.to_block===undefined)) throw new Error("from_block and to_block must be supplied together");
  if (typeof input.from_block==="number"&&typeof input.to_block==="number"&&(input.from_block>input.to_block||input.to_block-input.from_block>100000)) throw new Error("history range must span at most 100000 blocks");
  if (name==="explain_contract_transaction"&&!input.analysis_id&&(!input.chain||!input.address)) throw new Error("Provide analysis_id or chain/address");
  if (Array.isArray(input.engines)&&(!input.engines.includes("whatsabi")||!input.engines.includes("sevm")||new Set(input.engines).size!==input.engines.length)) throw new Error("Unique engines including whatsabi and sevm are required");
  const b=input.budget as {max_transactions?:number}|undefined;
  if (Array.isArray(input.tx_hashes)&&input.tx_hashes.length>(b?.max_transactions??20)) throw new Error("selected transactions exceed max_transactions");
}
export function contractArtifactResources(result:RESTEnvelope):CallToolResult["content"] {
  const data=result.data as Record<string,unknown>|null;if (!result.success||!data) return [];
  const artifact=data.artifact as {media_type?:string}|undefined;
  if (typeof data.content_base64!=="string"||typeof data.resource_uri!=="string") throw new Error("CONTRACT_ARTIFACT_INVALID: Missing file bytes");
  const bytes=Buffer.from(data.content_base64,"base64");
  if (bytes.length!==data.byte_length||createHash("sha256").update(bytes).digest("hex")!==data.chunk_sha256) throw new Error("CONTRACT_ARTIFACT_INTEGRITY_FAILED: Chunk length/checksum mismatch");
  const blob=data.content_base64;delete data.content_base64;
  return [{type:"resource",resource:{uri:data.resource_uri,mimeType:artifact?.media_type??"application/octet-stream",blob}}];
}
export function registerContractResources(server:McpServer,client:RESTClient):void {
  server.registerResource("contract-analysis-artifact",new ResourceTemplate("contract-analysis://{analysis_id}/{artifact_id}/{offset}/{chunk_size}",{list:undefined}),
    {title:"Scoped contract analysis artifact",description:"Saved artifact bytes; scope and whole checksum are revalidated on every read"},
    async(uri,values,extra)=>{
      const request={analysis_id:analysis_id.parse(values.analysis_id),artifact_id:item.parse(values.artifact_id),
        offset:z.coerce.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER).parse(values.offset),chunk_size:z.coerce.number().int().min(1).max(4<<20).parse(values.chunk_size)};
      const response=await client.post("/export/contracts/get_contract_analysis_artifact",request,{signal:extra.signal});
      const content=contractArtifactResources(response)[0];if (!content||content.type!=="resource") throw new Error("Contract artifact unavailable");
      return {contents:[{...content.resource,uri:uri.href}]};
    });
}
