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
const declaredEnvironment=z.object({chain_id:z.string().regex(/^[1-9][0-9]{0,77}$/).optional(),execution_address:address.optional(),evm_rules:z.enum(["shanghai","cancun","prague"]).optional()}).strict();
const submittedSource=z.object({language:z.literal("Solidity"),files:z.record(z.string().min(1).max(256),z.string()),
  compiler_version:z.string().regex(/^0\.[0-9]+\.[0-9]+\+commit\.[0-9a-f]{8}$/),settings:z.record(z.string(),z.unknown()).optional(),target:z.string().min(3).max(384)}).strict();
const submittedCode=z.discriminatedUnion("kind",[
  z.object({kind:z.literal("source"),source:submittedSource,bytecode:z.string().regex(/^0x(?:[0-9a-fA-F]{2}){1,49152}$/).optional(),code_phase:z.enum(["runtime","creation"]).optional(),environment:declaredEnvironment.optional()}).strict(),
  z.object({kind:z.literal("bytecode"),bytecode:z.string().regex(/^0x(?:[0-9a-fA-F]{2}){1,49152}$/),code_phase:z.enum(["runtime","creation"]),environment:declaredEnvironment.optional()}).strict(),
]);
export const contractAnalysisInputShape = {
  chain:chain.optional(), address:address.optional(), input:submittedCode.optional(),block: z.union([integer,z.enum(["latest","safe","finalized"])]).optional(),
  timestamp: z.string().datetime({ offset: true }).optional(), from_block: integer.optional(), to_block: integer.optional(),
  tx_hashes: z.array(tx).max(50).optional(), include_source: z.boolean().default(true),
  engines: z.array(z.enum(["whatsabi","sevm","solc-ast","heimdall","slither","gigahorse","vyper-ast"])).min(2).max(7).default(["whatsabi","sevm","solc-ast","heimdall","slither","gigahorse"]),
  budget, idempotency_key: z.string().max(128).optional(),
};
function tool(name:string,description:string,inputSchema:Record<string,z.ZodTypeAny>,readOnly=true):ToolDefinition {
  return { name,title:name,description,path:`/${name==="get_contract_analysis_artifact"?"export":"analysis"}/contracts/${name}`,method:"POST",readOnly,inputSchema,
    summarize: r => JSON.stringify(r.data) };
}
export const allContractAnalysisTools:ToolDefinition[] = [
  tool("analyze_program","Queue a separately versioned TRON TVM or Solana sBPF program analysis. Validate native address/signature formats, freeze observed program code and block/slot identity, preserve raw code/instructions and selected transaction metadata. Read resulting methods, sections and artifacts with the existing scoped contract-analysis tools. Historical program code, account prestate and VM replay availability remain explicit.",{
    schema_version:z.literal("contract-program-request-v1").default("contract-program-request-v1"),
    vm_family:z.enum(["tron_tvm","solana_sbf"]),address:z.string().min(32).max(128),
    tx_hashes:z.array(z.string().min(64).max(100)).max(50).optional(),budget,idempotency_key:z.string().max(128).optional(),
  },false),
  tool("get_contract_analysis_capabilities","Read the capabilities observed from the configured digest-pinned runtime: installed engines, source compiler versions, supported execution families and precise historical replay restrictions. Availability is separate from protocol certification. No blockchain requests.",{}),
  tool("analyze_contract","Queue EVM address, submitted Solidity source, or bytecode analysis. Keep legacy chain/address requests; alternatively input.kind=source with named files, exact compiler version/settings and file:Contract target, or input.kind=bytecode with full bytes and runtime/creation code_phase. Code-only input performs zero provider requests, retains symbolic state and declared environment separately, and never invents deployment addresses or blocks. Optional chain/address binds submitted runtime/source to observed deployment code. Compile failures preserve input/diagnostics; supplied runtime permits bytecode fallback. Creation input preserves disassembly and explicit runtime frontier. Read methods, flows, formulas, permissions, conclusions/analysis_gaps and evidence through the existing tools. State/transactions require deployment binding; selected transactions preserve execution/prestate/diffs. No on-chain writes.",contractAnalysisInputShape,false),
  tool("get_contract_analysis","Read scoped contract job status, immutable snapshot, exact section counts and completeness. With section, traverse every saved row using filter/snapshot-bound cursors; no top-200 truncation. Job completed and semantic coverage complete are independent.",{analysis_id,section:z.string().max(100).optional(),method_id:item.optional(),evidence_id:item.optional(),...page}),
  tool("list_contract_methods","List all discovered contract methods, stable method IDs, selectors, verified signatures versus candidates, body-derived Chinese purpose, behaviors and per-method coverage. Filter by text/selector or behavior and traverse all pages.",{analysis_id,search:z.string().max(200).optional(),behavior:z.string().max(100).optional(),...page}),
  tool("get_contract_method_analysis","Read the method model: parameters/outputs, conditions, ordered step references, storage/call/fund/expression/evidence IDs, formulas, dependencies and observed transaction links. Retrieve referenced sections with get_contract_analysis(method_id=...). Function names and comments do not prove behavior.",{analysis_id,method_id:item}),
  tool("get_contract_execution_flow","Read paginated typed reads/writes/guards/calls/transfers/delegates and cross-method state-dependency edges. Static transfer intents remain separate from executed_funds/receipt token_events; every edge retains evidence references.",{analysis_id,method_id:item.optional(),...page}),
  tool("explain_contract_transaction","Read a selected transaction's saved calldata, status, method links, exact native movements, receipt events, call tree, transaction prestate code and state diff. With chain/address/tx_hash and no analysis_id, queue a new analysis. Same-block historical code uses prestate; unavailable trace/archive is an explicit coverage gap.",{analysis_id:analysis_id.optional(),chain:chain.optional(),address:address.optional(),tx_hash:tx,budget},false),
  tool("compare_contract_versions","Compare two completed analyses of the same chain/entry within the authorized workspace. Paginated before/after code identities and method models preserve selector collisions; permission/fund details remain available via method IDs and sections.",{analysis_id,compare_analysis_id:analysis_id,...page}),
  tool("get_contract_analysis_artifact","Read authorized source, pseudocode, IR, runtime bytecode, method CSV, editable graph, full JSON, Markdown report or evidence ZIP as embedded resource chunks. Validate chunk SHA256 and whole artifact SHA256; offset must refer to the saved artifact. No client-selected storage paths or public download URL.",{analysis_id,artifact_id:item,offset:integer.default(0),chunk_size:z.number().int().min(1).max(4<<20).default(1<<20)}),
];
export const contractAnalysisTools = allContractAnalysisTools.filter(t=>t.name!=="analyze_program"||process.env.CONTRACT_PROGRAM_ANALYSIS_ENABLED==="1");
export function validateContractTool(name:string,input:Record<string,unknown>):void {
  if (!allContractAnalysisTools.some(t=>t.name===name)) return;
  if(name==="analyze_contract") {
    if(Buffer.byteLength(JSON.stringify(input))>2<<20)throw new Error("Contract arguments exceed 2 MiB");
    const material=input.input as z.infer<typeof submittedCode>|undefined;
    if(!material&&(!input.chain||!input.address))throw new Error("Provide chain/address or input source/bytecode");
    if(Boolean(input.chain)!==Boolean(input.address))throw new Error("Deployment binding requires chain and address together");
    if(material){
      const bound=Boolean(input.chain);
      if(!bound&&[input.block,input.timestamp,input.from_block,input.to_block,input.tx_hashes].some(x=>x!==undefined))throw new Error("Block, history and transactions require deployment binding");
      if(material.bytecode&&!material.code_phase)throw new Error("Bytecode requires explicit code_phase");
      if(bound&&material.code_phase==="creation")throw new Error("Creation code cannot be equated with deployed runtime");
      if(material.environment?.chain_id&&BigInt(material.environment.chain_id)>=(1n<<256n))throw new Error("Declared chain identity exceeds uint256");
      if(material.kind==="source"){
        const files=Object.entries(material.source.files);
        if(!files.length||files.length>256||files.reduce((n,[p,s])=>n+Buffer.byteLength(p)+Buffer.byteLength(s),0)>1536<<10)throw new Error("Source file budget exceeded");
        if(files.some(([p])=>{const logical=p.replace(/^(?:\.\/)+/,'');return !logical||/(?:^|\/)\.\.?($|\/)|[\\:\x00\r\n\t]|^\/|\/\//.test(logical)||logical.endsWith('/')}))throw new Error("Source paths must be relative and canonical");
        const split=material.source.target.lastIndexOf(':'),file=material.source.target.slice(0,split),target=material.source.target.slice(split+1);
        if(!Object.hasOwn(material.source.files,file)||!/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(target))throw new Error("Target must identify a supplied source contract");
        const settings=material.source.settings||{},allowed=new Set(['optimizer','viaIR','evmVersion','libraries','remappings','metadata','debug']);
        if(Object.keys(settings).some(k=>!allowed.has(k))||Buffer.byteLength(JSON.stringify(settings))>64<<10)throw new Error("Unsupported compiler settings");
      }
    }
  }
  if (name === "analyze_program") {
    const alphabet="123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";
    const decode=(value:string):Buffer=>{
      if (!value || value.length > 100) throw new Error("Invalid native identifier");
      let n=0n;
      for(const c of value){const digit=alphabet.indexOf(c);if(digit<0)throw new Error("Invalid Base58 identifier");n=n*58n+BigInt(digit)}
      const hex=n.toString(16),body=n===0n?Buffer.alloc(0):Buffer.from(hex.length%2?"0"+hex:hex,"hex");
      return Buffer.concat([Buffer.alloc(value.match(/^1*/)?.[0].length??0),body]);
    };
    const native=String(input.address??"");
    if(input.vm_family==="solana_sbf") {
      if(decode(native).length!==32)throw new Error("Complete 32-byte Solana program identity required");
      for(const signature of (input.tx_hashes as string[]|undefined)??[])if(decode(signature).length!==64)throw new Error("Complete 64-byte Solana signature required");
    } else if(input.vm_family==="tron_tvm") {
      if(!/^(0x)?41[0-9a-fA-F]{40}$/.test(native)) {
        const bytes=decode(native),payload=bytes.subarray(0,21);
        const checksum=createHash("sha256").update(createHash("sha256").update(payload).digest()).digest().subarray(0,4);
        if(bytes.length!==25||bytes[0]!==0x41||!checksum.equals(bytes.subarray(21)))throw new Error("TRON address checksum mismatch");
      }
      for(const hash of (input.tx_hashes as string[]|undefined)??[])if(!/^(0x)?[0-9a-fA-F]{64}$/.test(hash))throw new Error("Complete TRON transaction hash required");
    } else throw new Error("Unsupported program execution family");
  }
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
