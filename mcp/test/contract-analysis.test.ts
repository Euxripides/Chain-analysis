import test from "node:test";
import assert from "node:assert/strict";
import {createHash} from "node:crypto";
import {z} from "zod";
import {contractAnalysisTools,allContractAnalysisTools,validateContractTool,contractArtifactResources} from "../src/contract-analysis.js";
import {nextInvestigationTools} from "../src/next-investigation.js";
import {RESTClient} from "../src/rest-client.js";
import {createServer} from "node:http";
import {once} from "node:events";
const address="0x"+"1".repeat(40),tx="0x"+"2".repeat(64),job="00000000-0000-4000-8000-000000000001";
test("source and bytecode inputs preserve phase, strict source boundaries and deployment binding",()=>{
 const tool=contractAnalysisTools.find(t=>t.name==="analyze_contract")!,schema=z.object(tool.inputSchema as z.ZodRawShape).strict();
 const source={kind:"source",source:{language:"Solidity",files:{"Token.sol":"contract Token {}"},compiler_version:"0.8.30+commit.73712a01",target:"Token.sol:Token"}};
 for(const args of [{input:source},{input:{kind:"bytecode",bytecode:"0x6000",code_phase:"runtime"}},{input:{kind:"bytecode",bytecode:"0x6000",code_phase:"creation"}},{chain:"ethereum",address,input:source}])validateContractTool(tool.name,schema.parse(args));
 for(const args of [{},{chain:"ethereum"},{input:source,block:1},{input:source,chain:"ethereum"},
   {input:{...source,source:{...source.source,files:{"../Token.sol":"x"}}}},
   {input:{...source,source:{...source.source,settings:{modelChecker:{engine:"all"}}}}},
   {input:{kind:"bytecode",bytecode:"0x00",code_phase:"creation"},chain:"ethereum",address}]){
  assert.throws(()=>validateContractTool(tool.name,schema.parse(args)));
 }
 assert.equal(schema.safeParse({input:{...source,object_key:"private"}}).success,false);
 assert.equal(schema.safeParse({input:{kind:"bytecode",bytecode:"0x0",code_phase:"runtime"}}).success,false);
});
test("contract operation definitions constrain chains, budgets and exact identities",()=>{
 assert.equal(contractAnalysisTools.length,process.env.CONTRACT_PROGRAM_ANALYSIS_ENABLED==="1"?10:9);const tool=contractAnalysisTools.find(t=>t.name==="analyze_contract")!;
 const schema=z.object(tool.inputSchema as z.ZodRawShape).strict();const valid=schema.parse({chain:"ethereum",address,tx_hashes:[tx]});validateContractTool(tool.name,valid);
 for(const bad of [{chain:"solana",address},{chain:"tron",address},{chain:"ethereum",address:"0x123"},{chain:"ethereum",address,budget:{max_contracts:1000}}])assert.equal(schema.safeParse(bad).success,false);
 assert.throws(()=>validateContractTool(tool.name,{...valid,block:1,timestamp:"2026-01-01T00:00:00Z"}));
 assert.throws(()=>validateContractTool(tool.name,{...valid,from_block:1}));
 assert.throws(()=>validateContractTool(tool.name,{...valid,tx_hashes:Array(21).fill(tx)}));
 assert.equal(contractAnalysisTools.find(t=>t.name==="get_contract_analysis_artifact")?.readOnly,true);
 assert.equal(tool.readOnly,false);
 const start=nextInvestigationTools.find(t=>t.name==="start_analysis")!;assert.ok((start.inputSchema as z.ZodType).safeParse({operation:"analyze_contract",arguments:{chain:"ethereum",address}}).success);
});
test("native program identities preserve their execution-family format",()=>{
 const program=allContractAnalysisTools.find(t=>t.name==="analyze_program")!;
 const schema=z.object(program.inputSchema as z.ZodRawShape).strict();
 const solana=schema.parse({vm_family:"solana_sbf",address:"11111111111111111111111111111111"});
 validateContractTool(program.name,solana);
 validateContractTool(program.name,schema.parse({vm_family:"tron_tvm",address:"TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU7"}));
 assert.throws(()=>validateContractTool(program.name,{...solana,address:"1".repeat(33)}),/32-byte/);
 assert.throws(()=>validateContractTool(program.name,{...solana,tx_hashes:["1".repeat(63)]}),/64-byte/);
 assert.throws(()=>validateContractTool(program.name,{vm_family:"tron_tvm",address:"TLa2f6VPqDgRE67v1736s7bJ8Ray5wYjU8"}),/checksum/);
 assert.equal(schema.safeParse({vm_family:"solana_sbf",address:solana.address,block:1}).success,false);
 assert.equal(contractAnalysisTools.find(t=>t.name==="get_contract_analysis_capabilities")?.readOnly,true);
});
test("contract files expose verified embedded chunks and reject corrupt bytes",()=>{
 const bytes=Buffer.from("合约执行证据"),sha=createHash("sha256").update(bytes).digest("hex");
 const result={success:true,data:{content_base64:bytes.toString("base64"),resource_uri:`contract-analysis://${job}/${"a".repeat(40)}/0/1048576`,byte_length:bytes.length,chunk_sha256:sha,artifact:{media_type:"text/plain"}},meta:{},error:null};
 const files=contractArtifactResources(result);assert.equal(files[0].type,"resource");assert.equal("content_base64" in result.data,false);
 assert.throws(()=>contractArtifactResources({...result,data:{...result.data,content_base64:Buffer.from("corrupt").toString("base64")}}),/INTEGRITY_FAILED/);
});
test("feature REST routing leaves legacy tool requests on the deployed API",async()=>{
 const seen:string[]=[];
 const make=(name:string)=>createServer((req,res)=>{seen.push(name+req.url);res.setHeader("content-type","application/json");res.end(JSON.stringify({success:true,data:{name},meta:{},error:null}));});
 const legacy=make("legacy"),feature=make("feature");legacy.listen(0,"127.0.0.1");feature.listen(0,"127.0.0.1");await Promise.all([once(legacy,"listening"),once(feature,"listening")]);
 try{
  const port=(server:ReturnType<typeof createServer>)=>(server.address() as {port:number}).port;
  const client=new RESTClient({baseUrl:`http://127.0.0.1:${port(legacy)}`,contractBaseUrl:`http://127.0.0.1:${port(feature)}`,apiKey:"fixture",timeoutMs:1000});
  await client.post("/analysis/contracts/analyze_contract",{});await client.post("/export/contracts/get_contract_analysis_artifact",{});await client.get("/read/address/profile",{});await client.post("/analysis/start_analysis",{operation:"analyze_contract"});await client.post("/analysis/start_analysis",{operation:"fund_trace"});
  assert.deepEqual(seen,["feature/analysis/contracts/analyze_contract","feature/export/contracts/get_contract_analysis_artifact","legacy/read/address/profile","feature/analysis/start_analysis","legacy/analysis/start_analysis"]);
 }finally{await Promise.all([new Promise<void>(resolve=>legacy.close(()=>resolve())),new Promise<void>(resolve=>feature.close(()=>resolve()))]);}
});
